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
//   - Effort becomes output_config.effort.
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
		Messages:  messages(req.Messages),
		System:    system(req.System, req.CacheSystem),
		Tools:     tools(req.Tools, req.CacheSystem),
	}
	switch req.Thinking {
	case ThinkingAdaptive:
		params.Thinking = anthropic.ThinkingConfigParamUnion{
			OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{},
		}
	case ThinkingOff:
		params.Thinking = anthropic.ThinkingConfigParamUnion{
			OfDisabled: &anthropic.ThinkingConfigDisabledParam{},
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
			if b := ev.ContentBlock; b.Type == "tool_use" {
				open[ev.Index] = &Call{ID: b.ID, Name: b.Name}
			}

		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				fn(Delta(ev.Delta.Text))
			case "thinking_delta":
				fn(Thought(ev.Delta.Thinking))
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
	return used, nil
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

// messages is the conversation in the Messages API's shape. The one
// thing it does that a straight translation would not: tool results
// that ran together go back as tool_result blocks in a single user
// message, because that is what the API wants for calls made in
// parallel and it is not what a list of messages looks like.
func messages(in []Message) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(in))
	for i := 0; i < len(in); {
		switch m := in[i]; m.Role {
		case RoleAssistant:
			var blocks []anthropic.ContentBlockParamUnion
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
