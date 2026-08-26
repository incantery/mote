package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/incantery/mote/tool"
)

// chunks is an OpenAI-compatible event stream: one `data:` line per
// chunk and the [DONE] sentinel the endpoint really does send.
func chunks(payloads ...string) string {
	var b strings.Builder
	for _, p := range payloads {
		b.WriteString("data: " + p + "\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

// The scripted exchange the conformance test drives, in this wire's
// own words: a sentence, then a tool call whose arguments arrive in
// three pieces, then — after the tool result goes back — a sentence.
const (
	openAISaid = `{"id":"c1","model":"gpt-5-2026-01","choices":[{"index":0,"delta":{"content":"Let me look. "}}]}`
	openAICall = `{"id":"c1","model":"gpt-5-2026-01","choices":[{"index":0,"delta":{"tool_calls":[` +
		`{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`
	openAICallMore = `{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[` +
		`{"index":0,"function":{"arguments":"\"GAPS"}}]}}]}`
	openAICallEnd = `{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[` +
		`{"index":0,"function":{"arguments":".md\"}"}}]}}]}`
	openAIStopTool = `{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`
	openAIUsedOne  = `{"id":"c1","choices":[],"usage":{"prompt_tokens":14,"completion_tokens":7,` +
		`"prompt_tokens_details":{"cached_tokens":2}}}`

	openAIAnswered = `{"id":"c2","model":"gpt-5-2026-01","choices":[{"index":0,"delta":{"content":"Nine rows."}}]}`
	openAIStop     = `{"id":"c2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	openAIUsedTwo  = `{"id":"c2","choices":[],"usage":{"prompt_tokens":30,"completion_tokens":5}}`
)

func openAIScene(t *testing.T) (Provider, *wire) {
	t.Helper()
	w := serve(t,
		chunks(openAISaid, openAICall, openAICallMore, openAICallEnd, openAIStopTool, openAIUsedOne),
		chunks(openAIAnswered, openAIStop, openAIUsedTwo),
	)
	p := NewOpenAI(w.URL, "sk-test")
	p.Model = "gpt-5"
	return p, w
}

func openAIHangs(t *testing.T) Provider {
	t.Helper()
	s := hang(t, "data: "+openAISaid+"\n\n")
	return NewOpenAI(s.URL, "sk-test")
}

// The arguments arrive as three fragments of one string; a call is
// only a call once all of them have.
func TestOpenAIReassemblesToolCalls(t *testing.T) {
	p, _ := openAIScene(t)
	var got collect
	if _, err := p.Stream(t.Context(), Request{Messages: []Message{User("how many gaps?")}}, got.on); err != nil {
		t.Fatal(err)
	}
	if len(got.calls) != 1 {
		t.Fatalf("%d calls, want 1", len(got.calls))
	}
	if c := got.calls[0]; c.ID != "call_1" || c.Name != "read" || c.Arguments != `{"path":"GAPS.md"}` {
		t.Fatalf("call is %+v", c)
	}
}

// The usage chunk arrives last and carries no choices; without
// stream_options.include_usage it never arrives at all.
func TestOpenAIAsksForUsageAndCorrectsIt(t *testing.T) {
	p, w := openAIScene(t)
	used, err := p.Stream(t.Context(), Request{Messages: []Message{User("how many gaps?")}}, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	// prompt_tokens is 14 and two of them were cached, so Input is
	// the twelve that were not.
	if used.Input != 12 || used.CacheRead != 2 || used.Output != 7 {
		t.Fatalf("usage is %+v", used)
	}
	if used.Model != "gpt-5-2026-01" {
		t.Fatalf("model is %q", used.Model)
	}
	if used.StopReason != "tool_calls" {
		t.Fatalf("stop reason is %q", used.StopReason)
	}
	body := w.sent(t, 0)
	opts, _ := body["stream_options"].(map[string]any)
	if body["stream"] != true || opts["include_usage"] != true {
		t.Fatalf("request did not ask for a streamed usage: %v", body)
	}
}

// The system prompt is a message on this wire, an assistant turn
// carries its tool_calls, and a tool result quotes the call id.
func TestOpenAISendsTheConversation(t *testing.T) {
	p, w := openAIScene(t)
	req := Request{
		System: "you are a supervisor",
		Messages: []Message{
			User("how many gaps?"),
			Assistant("Let me look. ", Call{ID: "call_1", Name: "read", Arguments: `{"path":"GAPS.md"}`}),
			Answer("call_1", "nine rows"),
		},
		Tools:     tool.NewRegistry(stub{name: "read"}).Definitions(),
		MaxTokens: 1000,
	}
	if _, err := p.Stream(t.Context(), req, func(Event) {}); err != nil {
		t.Fatal(err)
	}

	body := w.sent(t, 0)
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("%d messages, want 4: %v", len(msgs), msgs)
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "you are a supervisor" {
		t.Fatalf("the system prompt is not the first message: %v", first)
	}
	assistant, _ := msgs[2].(map[string]any)
	calls, _ := assistant["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("the assistant turn lost its call: %v", assistant)
	}
	result, _ := msgs[3].(map[string]any)
	if result["role"] != "tool" || result["tool_call_id"] != "call_1" {
		t.Fatalf("the tool result is not tied to its call: %v", result)
	}
	if body["max_completion_tokens"] != float64(1000) {
		t.Fatalf("max tokens is %v", body["max_completion_tokens"])
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("%d tools on the wire", len(tools))
	}
}

// Effort is the dial; thinking off is how you tell an endpoint that
// refuses function tools with reasoning on to turn it off.
func TestOpenAIEffortAndThinking(t *testing.T) {
	for _, c := range []struct {
		name string
		req  Request
		want any
	}{
		{"nothing said", Request{}, nil},
		{"effort", Request{Effort: EffortHigh}, "high"},
		{"thinking off", Request{Thinking: ThinkingOff}, "none"},
		{"effort wins", Request{Effort: EffortLow, Thinking: ThinkingOff}, "low"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, w := openAIScene(t)
			if _, err := p.Stream(t.Context(), c.req, func(Event) {}); err != nil {
				t.Fatal(err)
			}
			if got := w.sent(t, 0)["reasoning_effort"]; got != c.want {
				t.Fatalf("reasoning_effort is %v, want %v", got, c.want)
			}
		})
	}
}

// An endpoint that says no says why, and the sentence it said is
// worth more than the number.
func TestOpenAIRefusedKey(t *testing.T) {
	w := serve(t)
	w.stop(http.StatusUnauthorized, `{"error":{"message":"incorrect api key"}}`)
	_, err := NewOpenAI(w.URL, "sk-wrong").Stream(t.Context(), Request{}, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), "incorrect api key") {
		t.Fatalf("error is %v", err)
	}
}

// A model that streams its reasoning has it delivered apart from the
// reply, so a terminal can show it apart or not at all.
func TestOpenAIThinkingDeltas(t *testing.T) {
	w := serve(t, chunks(
		`{"model":"m","choices":[{"index":0,"delta":{"reasoning_content":"the file is small"}}]}`,
		`{"model":"m","choices":[{"index":0,"delta":{"content":"nine"},"finish_reason":"stop"}]}`,
	))
	var got collect
	if _, err := NewOpenAI(w.URL, "").Stream(t.Context(), Request{}, got.on); err != nil {
		t.Fatal(err)
	}
	if got.think.String() != "the file is small" || got.text.String() != "nine" {
		t.Fatalf("thought %q, said %q", got.think.String(), got.text.String())
	}
}

// A key of "" sends no Authorization header, which is what a model
// running on this machine wants.
func TestOpenAIKeylessEndpoint(t *testing.T) {
	w := serve(t, chunks(`{"model":"local","choices":[{"index":0,"delta":{"content":"hi"}}]}`))
	if _, err := NewOpenAI(w.URL, "").Stream(t.Context(), Request{}, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if h := w.header(0, "Authorization"); h != "" {
		t.Fatalf("sent an Authorization header of %q", h)
	}
}

// A tool that says nothing about its arguments still has to send
// something that is JSON.
func TestArgumentsAreAlwaysJSON(t *testing.T) {
	if got := arguments(""); got != "{}" {
		t.Fatalf("empty arguments became %q", got)
	}
	if got := object(`not json`); len(got) != 0 {
		t.Fatalf("unreadable arguments became %v", got)
	}
	if got := object(`{"path":"x"}`); got["path"] != "x" {
		t.Fatalf("arguments became %v", got)
	}
}

// stub is a tool that does nothing, so a request body can carry a
// real definition without anything touching a disk.
type stub struct{ name string }

func (s stub) Name() string        { return s.name }
func (s stub) Description() string { return s.name + " reads a file" }
func (s stub) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
}
func (s stub) Run(context.Context, json.RawMessage, tool.Handle) (tool.Result, error) {
	return tool.Result{Text: "ran"}, nil
}
