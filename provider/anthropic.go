package provider

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/incantery/mote/tool"
)

// The two defaults the Messages API has no opinion about and every
// caller would otherwise have to have one about.
const (
	// DefaultAnthropicModel is what a Request with no Model gets.
	DefaultAnthropicModel = string(anthropic.ModelClaudeOpus5)
	// DefaultAnthropicMaxTokens is what a Request with no MaxTokens
	// gets. max_tokens is required by the API, and streaming is what
	// makes a large one safe to ask for.
	DefaultAnthropicMaxTokens = 64000
)

// Anthropic is the Messages API, through the official SDK.
//
// It honours all three of a Request's hints:
//
//   - CacheSystem puts an ephemeral cache_control breakpoint on the
//     last tool and on the last block of the system prompt, so the
//     stable prefix of every turn — tools, then prompt — is cached
//     and read back on the turns after it.
//   - Thinking adaptive sends the adaptive union and off sends the
//     disabled one. The zero value sends nothing, which is the right
//     answer for a Claude 4.6 or 5: those think adaptively already,
//     and a budget_tokens is the wrong way to ask a model that does.
//     ThinkingDisplay becomes thinking.display, which the API only
//     has a place for on the adaptive config — so asking for one
//     asks for the other.
//   - Effort becomes output_config.effort.
//
// It is also the reason Message.Raw exists. A turn that thought and
// called a tool has to come back with its thinking blocks and their
// signatures in front of the tool call, or the API refuses the
// conversation. Stream hands them out as one KindRaw event at the end
// of the turn; put that on the assistant Message and this reads it
// back. Nothing else has to know what a signature is.
type Anthropic struct {
	// Model is used when a Request does not name one.
	Model string
	// MaxTokens is used when a Request does not give one.
	MaxTokens int

	client anthropic.Client
}

// NewAnthropic is the base URL and the key, either of which may be
// empty: the SDK reads ANTHROPIC_BASE_URL and ANTHROPIC_API_KEY for
// itself, which is where a key that was not passed comes from.
func NewAnthropic(base, key string) *Anthropic {
	var opts []option.RequestOption
	if base != "" {
		opts = append(opts, option.WithBaseURL(base))
	}
	if key != "" {
		opts = append(opts, option.WithAPIKey(key))
	}
	return &Anthropic{
		Model:     DefaultAnthropicModel,
		MaxTokens: DefaultAnthropicMaxTokens,
		client:    anthropic.NewClient(opts...),
	}
}

// Stream is one Messages call, streamed.
func (a *Anthropic) Stream(ctx context.Context, req Request, fn func(Event)) (Usage, error) {
	var used Usage

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(pick(req.Model, a.Model, DefaultAnthropicModel)),
		MaxTokens: int64(pickN(req.MaxTokens, a.MaxTokens, DefaultAnthropicMaxTokens)),
		Messages:  messages(req.Messages, req.Thinking != ThinkingOff),
		System:    system(req.System, req.CacheSystem),
		Tools:     tools(req.Tools, req.CacheSystem),
	}
	switch {
	case req.Thinking == ThinkingOff:
		params.Thinking = anthropic.ThinkingConfigParamUnion{
			OfDisabled: &anthropic.ThinkingConfigDisabledParam{},
		}
	case req.Thinking == ThinkingAdaptive || req.ThinkingDisplay != "":
		// display lives on the adaptive config and nowhere else, so a
		// caller who asked for one has asked for the other.
		params.Thinking = anthropic.ThinkingConfigParamUnion{
			OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{
				Display: anthropic.ThinkingConfigAdaptiveDisplay(req.ThinkingDisplay),
			},
		}
	}
	if req.Effort != "" {
		params.OutputConfig = anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffort(req.Effort),
		}
	}

	stream := a.client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	// A tool_use block arrives as a start with the id and name, then
	// its arguments as input_json_delta fragments, then a stop. It is
	// whole at the stop and not before, which is when it is delivered.
	open := map[int64]*Call{}
	// A thinking block arrives the same way — thinking_delta for the
	// words, signature_delta for the signature — and what is kept is
	// the whole of it, in the order the blocks came, because that is
	// the order the next request has to put them back in.
	thinking := map[int64]*kept{}
	var order []int64

	for stream.Next() {
		ev := stream.Current()
		switch ev.Type {
		case "message_start":
			m := ev.Message
			used.Model = string(m.Model)
			used.Input = int(m.Usage.InputTokens)
			used.Output = int(m.Usage.OutputTokens)
			used.CacheRead = int(m.Usage.CacheReadInputTokens)
			used.CacheWrite = int(m.Usage.CacheCreationInputTokens)

		case "content_block_start":
			switch b := ev.ContentBlock; b.Type {
			case "tool_use":
				open[ev.Index] = &Call{ID: b.ID, Name: b.Name}
			case "thinking":
				thinking[ev.Index] = &kept{Type: b.Type, Thinking: b.Thinking, Signature: b.Signature}
				order = append(order, ev.Index)
			case "redacted_thinking":
				// Nothing to show — the words are the API's to keep —
				// but the block still has to go back.
				thinking[ev.Index] = &kept{Type: b.Type, Data: b.Data}
				order = append(order, ev.Index)
			}

		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				fn(Delta(ev.Delta.Text))
			case "thinking_delta":
				if k := thinking[ev.Index]; k != nil {
					k.Thinking += ev.Delta.Thinking
				}
				fn(Thought(ev.Delta.Thinking))
			case "signature_delta":
				if k := thinking[ev.Index]; k != nil {
					k.Signature += ev.Delta.Signature
				}
			case "input_json_delta":
				if c := open[ev.Index]; c != nil {
					c.Arguments += ev.Delta.PartialJSON
				}
			}

		case "content_block_stop":
			if c := open[ev.Index]; c != nil {
				delete(open, ev.Index)
				args := arguments(c.Arguments)
				if !json.Valid([]byte(args)) {
					// A call whose arguments did not parse is not a
					// call: running it would be running something
					// nobody can read.
					fn(Failf("%s: the tool call arguments were not JSON", c.Name))
					break
				}
				fn(Calling(c.ID, c.Name, args))
			}

		case "message_delta":
			// Cumulative, and the last word on what the turn cost.
			if n := int(ev.Usage.InputTokens); n > 0 {
				used.Input = n
			}
			if n := int(ev.Usage.OutputTokens); n > 0 {
				used.Output = n
			}
			if n := int(ev.Usage.CacheReadInputTokens); n > 0 {
				used.CacheRead = n
			}
			if n := int(ev.Usage.CacheCreationInputTokens); n > 0 {
				used.CacheWrite = n
			}
			if r := ev.Delta.StopReason; r != "" {
				used.StopReason = string(r)
				if r == anthropic.StopReasonRefusal {
					fn(Fail(refusal(ev.Delta.StopDetails)))
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		return used, err
	}
	// Last, and only when there was something to keep: what this turn
	// has to hand back on the next one.
	if len(order) > 0 {
		blocks := make([]kept, 0, len(order))
		for _, i := range order {
			blocks = append(blocks, *thinking[i])
		}
		if raw, err := json.Marshal(blocks); err == nil {
			fn(Keeping(raw))
		}
	}
	return used, nil
}

// kept is a thinking block as it goes into Message.Raw and comes back
// out. It is the wire's own shape, so what a harness writes to a
// session file and reads back a week later is what the API said.
//
// A harness never reads this. It is here rather than in the SDK's
// param union because a param type is written to be marshalled, and
// this has to survive the round trip in both directions.
type kept struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"`
}

// thought is the blocks a previous assistant turn kept, as content
// blocks for the next request. They go in front of everything else in
// the message, which is the order they came in and the order the API
// wants them.
func thought(raw json.RawMessage) []anthropic.ContentBlockParamUnion {
	if len(raw) == 0 {
		return nil
	}
	var blocks []kept
	if json.Unmarshal(raw, &blocks) != nil {
		// Somebody else's Raw, or a file that was edited. The turn is
		// still a turn; it is the thinking that is lost.
		return nil
	}
	out := make([]anthropic.ContentBlockParamUnion, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "thinking":
			out = append(out, anthropic.NewThinkingBlock(b.Signature, b.Thinking))
		case "redacted_thinking":
			out = append(out, anthropic.NewRedactedThinkingBlock(b.Data))
		}
	}
	return out
}

// refusal is the sentence a refused turn puts on the error event. The
// category is the part that is stable enough to act on; the
// explanation is not guaranteed to be, and is included when there is
// one because a person reading it wants both.
func refusal(d anthropic.RefusalStopDetails) string {
	s := "the model refused to answer"
	if d.Category != "" {
		s += ": " + string(d.Category)
	}
	if d.Explanation != "" {
		s += " — " + d.Explanation
	}
	return s
}

// system is the prompt as content blocks. One block, with the cache
// breakpoint on it when asked for — "the last block" and "the only
// block" are the same block until a caller has a reason for two.
func system(prompt string, cache bool) []anthropic.TextBlockParam {
	if prompt == "" {
		return nil
	}
	block := anthropic.TextBlockParam{Text: prompt}
	if cache {
		block.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}
	return []anthropic.TextBlockParam{block}
}

// tools is the registry's definitions in the Messages API's shape,
// with the cache breakpoint on the last one: tools come before the
// system prompt in the prefix, so caching there caches them too.
func tools(defs []tool.Definition, cache bool) []anthropic.ToolUnionParam {
	if len(defs) == 0 {
		return nil
	}
	out := make([]anthropic.ToolUnionParam, 0, len(defs))
	for i, d := range defs {
		t := anthropic.ToolParam{
			Name:        d.Function.Name,
			Description: anthropic.String(d.Function.Description),
			InputSchema: inputSchema(d.Function.Parameters),
		}
		if cache && i == len(defs)-1 {
			t.CacheControl = anthropic.NewCacheControlEphemeralParam()
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &t})
	}
	return out
}

// inputSchema takes a tool's JSON Schema apart into the two fields
// the SDK names and keeps the rest — a tool that wrote
// additionalProperties meant it.
func inputSchema(raw json.RawMessage) anthropic.ToolInputSchemaParam {
	schema := anthropic.ToolInputSchemaParam{Properties: map[string]any{}}
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil {
		return schema
	}
	for k, v := range fields {
		switch k {
		case "type":
			// Always "object"; the SDK writes it.
		case "properties":
			var props any
			if json.Unmarshal(v, &props) == nil && props != nil {
				schema.Properties = props
			}
		case "required":
			var req []string
			if json.Unmarshal(v, &req) == nil {
				schema.Required = req
			}
		default:
			var extra any
			if json.Unmarshal(v, &extra) != nil {
				continue
			}
			if schema.ExtraFields == nil {
				schema.ExtraFields = map[string]any{}
			}
			schema.ExtraFields[k] = extra
		}
	}
	return schema
}

// messages is the conversation in the Messages API's shape. Two
// things it does that a straight translation would not: tool results
// that ran together go back as tool_result blocks in a single user
// message, because that is what the API wants for calls made in
// parallel and it is not what a list of messages looks like; and an
// assistant turn's kept thinking blocks go back in front of its text
// and its tool calls, which is where they were and where they have to
// be.
func messages(in []Message, thinking bool) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(in))
	for i := 0; i < len(in); {
		switch m := in[i]; m.Role {
		case RoleAssistant:
			var blocks []anthropic.ContentBlockParamUnion
			if thinking {
				// First, and only while thinking is on: a request
				// that turned it off must not carry them.
				blocks = append(blocks, thought(m.Raw)...)
			}
			if strings.TrimSpace(m.Text) != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Text))
			}
			for _, c := range m.Calls {
				blocks = append(blocks, anthropic.NewToolUseBlock(c.ID, object(c.Arguments), c.Name))
			}
			if len(blocks) > 0 {
				out = append(out, anthropic.NewAssistantMessage(blocks...))
			}
			i++
		case RoleTool:
			var blocks []anthropic.ContentBlockParamUnion
			for ; i < len(in) && in[i].Role == RoleTool; i++ {
				t := in[i]
				blocks = append(blocks, anthropic.NewToolResultBlock(t.CallID, t.Text, t.Error))
			}
			out = append(out, anthropic.NewUserMessage(blocks...))
		default:
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Text)))
			i++
		}
	}
	return out
}

// pick is the first of these that was set.
func pick(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func pickN(n ...int) int {
	for _, v := range n {
		if v > 0 {
			return v
		}
	}
	return 0
}
