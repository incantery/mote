package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/incantery/mote/tool"
)

// OpenAI is any OpenAI-compatible chat-completions endpoint: OpenAI
// itself, or one of the many things that speak its shape.
//
// It streams with stream_options.include_usage, because without that
// the stream simply ends and the token counts — which is to say the
// cost — are never reported at all.
//
// Of a Request's hints it honours one, and only where the endpoint
// has somewhere to put it: Effort becomes reasoning_effort. Thinking,
// ThinkingDisplay, Message.Raw and CacheSystem are ignored — OpenAI
// caches long prompts by itself and has no way to be asked, and the
// rest is the Messages API's vocabulary, not this one's.
//
// reasoning_effort is sent only when Effort was set, because the
// value that would otherwise go in it is the problem: verad sent
// "none" for Thinking off, and an OpenAI-compatible endpoint answers
// that with `400: Unsupported value: 'reasoning_effort' does not
// support 'none'` for models that have no way to turn reasoning off.
// A hint that 400s is worse than a hint that was not taken. An
// endpoint that does want a particular word — "none" included — is
// asked for it by name: an Effort this package does not recognise is
// passed through as the caller wrote it.
type OpenAI struct {
	// Model is used when a Request does not name one.
	Model string
	// HTTP is the client to send with. Nil is http.DefaultClient.
	HTTP *http.Client

	base, key string
}

// NewOpenAI is the base URL and the key. An empty base is OpenAI's
// own; an empty key sends no Authorization header, which is what a
// model running on this machine usually wants.
func NewOpenAI(base, key string) *OpenAI {
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return &OpenAI{base: strings.TrimRight(base, "/"), key: key}
}

// chatRequest is the body, and the tools go into it in exactly the
// shape tool.Registry already hands out.
type chatRequest struct {
	Model           string            `json:"model"`
	Stream          bool              `json:"stream"`
	StreamOptions   streamOptions     `json:"stream_options"`
	Messages        []chatMessage     `json:"messages"`
	Tools           []tool.Definition `json:"tools,omitempty"`
	MaxTokens       int               `json:"max_completion_tokens,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// chatMessage is one message, including the two shapes a tool round
// trip needs: an assistant turn that asked for tools, and a tool turn
// that answered one.
type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []chatCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type chatCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name string `json:"name"`
	// Arguments is JSON, as a string, and on the way back it arrives
	// one fragment at a time across many chunks.
	Arguments string `json:"arguments"`
}

// effort is a Request's dial in the words this endpoint has:
// minimal, low, medium, high. The two above high are high — the
// endpoint has no word for them and refusing the request over it
// would be worse than working slightly less hard. An empty Effort
// sends no field at all, and anything else is the caller's own word
// for their own endpoint, passed through.
func effort(e Effort) string {
	switch e {
	case "":
		return ""
	case EffortXHigh, EffortMax:
		return string(EffortHigh)
	}
	return string(e)
}

// Stream is one chat-completions call.
func (o *OpenAI) Stream(ctx context.Context, req Request, fn func(Event)) (Usage, error) {
	var used Usage

	body := chatRequest{
		Model:         o.model(req),
		Stream:        true,
		StreamOptions: streamOptions{IncludeUsage: true},
		Messages:      o.messages(req),
		Tools:         req.Tools,
		MaxTokens:     req.MaxTokens,
	}
	body.ReasoningEffort = effort(req.Effort)
	buf, err := json.Marshal(body)
	if err != nil {
		return used, err
	}

	r, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return used, err
	}
	r.Header.Set("Content-Type", "application/json")
	if o.key != "" {
		r.Header.Set("Authorization", "Bearer "+o.key)
	}

	client := o.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(r)
	if err != nil {
		return used, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return used, fmt.Errorf("the model answered %d: %s", res.StatusCode, strings.TrimSpace(string(detail)))
	}
	return o.read(res.Body, fn)
}

func (o *OpenAI) model(req Request) string {
	if req.Model != "" {
		return req.Model
	}
	return o.Model
}

// messages is the conversation in the wire's shape. The system prompt
// is a message here, which is the one place this API differs from the
// other in a way a caller would otherwise have to know about.
func (o *OpenAI) messages(req Request) []chatMessage {
	out := make([]chatMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		out = append(out, chatMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleAssistant:
			msg := chatMessage{Role: "assistant", Content: m.Text}
			for _, c := range m.Calls {
				msg.ToolCalls = append(msg.ToolCalls, chatCall{
					ID:   c.ID,
					Type: "function",
					Function: chatFunction{
						Name:      c.Name,
						Arguments: arguments(c.Arguments),
					},
				})
			}
			out = append(out, msg)
		case RoleTool:
			// Nothing marks a tool result as a failure here, so the
			// text says it. tool.Refused already writes that sentence.
			out = append(out, chatMessage{Role: "tool", ToolCallID: m.CallID, Content: m.Text})
		default:
			out = append(out, chatMessage{Role: "user", Content: m.Text})
		}
	}
	return out
}

// chunk is one server-sent event's payload.
type chunk struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Delta        struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning_content"`
			ToolCalls []struct {
				Index    int          `json:"index"`
				ID       string       `json:"id"`
				Type     string       `json:"type"`
				Function chatFunction `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// read is the stream: deltas as they come, tool calls reassembled
// from fragments keyed by index, and the usage chunk — which arrives
// last, carrying no choices — read for what the turn cost.
func (o *OpenAI) read(body io.Reader, fn func(Event)) (Usage, error) {
	var used Usage

	pending := map[int]*Call{}
	var order []int

	scan := bufio.NewScanner(body)
	scan.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scan.Scan() {
		payload, ok := strings.CutPrefix(strings.TrimSpace(scan.Text()), "data:")
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "[DONE]" {
			break
		}
		var c chunk
		if json.Unmarshal([]byte(payload), &c) != nil {
			continue
		}
		if c.Model != "" {
			used.Model = c.Model
		}
		if c.Usage != nil {
			cached := c.Usage.PromptTokensDetails.CachedTokens
			// prompt_tokens counts the cached ones too; Usage says
			// each token once.
			used.Input = c.Usage.PromptTokens - cached
			used.CacheRead = cached
			used.Output = c.Usage.CompletionTokens
		}
		for _, choice := range c.Choices {
			if choice.FinishReason != "" {
				used.StopReason = choice.FinishReason
			}
			if choice.Delta.Reasoning != "" {
				fn(Thought(choice.Delta.Reasoning))
			}
			if choice.Delta.Content != "" {
				fn(Delta(choice.Delta.Content))
			}
			for _, frag := range choice.Delta.ToolCalls {
				call, seen := pending[frag.Index]
				if !seen {
					call = &Call{}
					pending[frag.Index] = call
					order = append(order, frag.Index)
				}
				if frag.ID != "" {
					call.ID = frag.ID
				}
				if frag.Function.Name != "" {
					call.Name = frag.Function.Name
				}
				call.Arguments += frag.Function.Arguments
			}
		}
	}
	if err := scan.Err(); err != nil {
		return used, err
	}

	// At the end, because that is when a call is whole: a fragment
	// for the next index is not a promise that this one is finished.
	for _, i := range order {
		c := pending[i]
		fn(Calling(c.ID, c.Name, arguments(c.Arguments)))
	}
	return used, nil
}
