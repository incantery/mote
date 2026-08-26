package provider

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/incantery/mote/tool"
)

// events is a Messages API event stream: every event names itself on
// an `event:` line, which is how the SDK's decoder knows what it is
// looking at.
func events(pairs ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		b.WriteString("event: " + pairs[i] + "\ndata: " + pairs[i+1] + "\n\n")
	}
	return b.String()
}

// The same scripted exchange as the OpenAI one, in this wire's words:
// a text block, a tool_use block whose input arrives as three
// input_json_delta fragments, then a second turn that only speaks.
const (
	claudeStart = `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant",` +
		`"model":"claude-opus-5-20260101","content":[],"stop_reason":null,` +
		`"usage":{"input_tokens":12,"cache_read_input_tokens":2,"cache_creation_input_tokens":0,"output_tokens":1}}}`
	claudeTextOpen  = `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`
	claudeSaid      = `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Let me look. "}}`
	claudeTextShut  = `{"type":"content_block_stop","index":0}`
	claudeToolOpen  = `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read","input":{}}}`
	claudeToolOne   = `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`
	claudeToolTwo   = `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"GAPS"}}`
	claudeToolThree = `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":".md\"}"}}`
	claudeToolShut  = `{"type":"content_block_stop","index":1}`
	claudeStopTool  = `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},` +
		`"usage":{"input_tokens":12,"cache_read_input_tokens":2,"cache_creation_input_tokens":0,"output_tokens":7}}`
	claudeStop = `{"type":"message_stop"}`

	claudeStartTwo = `{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant",` +
		`"model":"claude-opus-5-20260101","content":[],"stop_reason":null,` +
		`"usage":{"input_tokens":30,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":1}}}`
	claudeAnswered = `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Nine rows."}}`
	claudeStopTurn = `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},` +
		`"usage":{"input_tokens":30,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":5}}`
)

func claudeTurnOne() string {
	return events(
		"message_start", claudeStart,
		"content_block_start", claudeTextOpen,
		"content_block_delta", claudeSaid,
		"content_block_stop", claudeTextShut,
		"content_block_start", claudeToolOpen,
		"content_block_delta", claudeToolOne,
		"content_block_delta", claudeToolTwo,
		"content_block_delta", claudeToolThree,
		"content_block_stop", claudeToolShut,
		"message_delta", claudeStopTool,
		"message_stop", claudeStop,
	)
}

func claudeTurnTwo() string {
	return events(
		"message_start", claudeStartTwo,
		"content_block_start", claudeTextOpen,
		"content_block_delta", claudeAnswered,
		"content_block_stop", claudeTextShut,
		"message_delta", claudeStopTurn,
		"message_stop", claudeStop,
	)
}

func anthropicScene(t *testing.T) (Provider, *wire) {
	t.Helper()
	t.Setenv(EnvAnthropicKey, "sk-ant-test")
	w := serve(t, claudeTurnOne(), claudeTurnTwo())
	return NewAnthropic(w.URL, "sk-ant-test"), w
}

func anthropicHangs(t *testing.T) Provider {
	t.Helper()
	t.Setenv(EnvAnthropicKey, "sk-ant-test")
	s := hang(t, events("message_start", claudeStart,
		"content_block_start", claudeTextOpen,
		"content_block_delta", claudeSaid))
	return NewAnthropic(s.URL, "sk-ant-test")
}

// A tool_use block is whole at its content_block_stop and not before:
// the input arrives as fragments of a JSON string.
func TestAnthropicReassemblesToolUse(t *testing.T) {
	p, _ := anthropicScene(t)
	var got collect
	used, err := p.Stream(t.Context(), Request{Messages: []Message{User("how many gaps?")}}, got.on)
	if err != nil {
		t.Fatal(err)
	}
	if got.text.String() != "Let me look. " {
		t.Fatalf("said %q", got.text.String())
	}
	if len(got.calls) != 1 {
		t.Fatalf("%d calls, want 1", len(got.calls))
	}
	if c := got.calls[0]; c.ID != "toolu_1" || c.Name != "read" || c.Arguments != `{"path":"GAPS.md"}` {
		t.Fatalf("call is %+v", c)
	}
	if used.Input != 12 || used.CacheRead != 2 || used.Output != 7 {
		t.Fatalf("usage is %+v", used)
	}
	if used.Model != "claude-opus-5-20260101" || used.StopReason != "tool_use" {
		t.Fatalf("usage is %+v", used)
	}
}

// The stable prefix — the tools, then the system prompt — is what
// gets a cache breakpoint, so it is read back rather than resent on
// every turn after the first.
func TestAnthropicCachesThePrefix(t *testing.T) {
	p, w := anthropicScene(t)
	req := Request{
		System:      "you are a supervisor",
		Messages:    []Message{User("how many gaps?")},
		Tools:       tool.NewRegistry(stub{name: "read"}, stub{name: "list"}).Definitions(),
		CacheSystem: true,
	}
	if _, err := p.Stream(t.Context(), req, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	body := w.sent(t, 0)

	system, _ := body["system"].([]any)
	if len(system) != 1 {
		t.Fatalf("the system prompt is %v", body["system"])
	}
	last, _ := system[len(system)-1].(map[string]any)
	if last["text"] != "you are a supervisor" {
		t.Fatalf("the system prompt is %v", last)
	}
	if cc, _ := last["cache_control"].(map[string]any); cc["type"] != "ephemeral" {
		t.Fatalf("the last system block is not cached: %v", last)
	}

	tools, _ := body["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("%d tools", len(tools))
	}
	first, _ := tools[0].(map[string]any)
	if _, cached := first["cache_control"]; cached {
		t.Fatalf("a breakpoint on every tool, not just the last: %v", first)
	}
	end, _ := tools[1].(map[string]any)
	if cc, _ := end["cache_control"].(map[string]any); cc["type"] != "ephemeral" {
		t.Fatalf("the last tool is not cached: %v", end)
	}
	// And the schema survived the trip, additionalProperties included.
	schema, _ := end["input_schema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["path"]; !ok || schema["additionalProperties"] != false {
		t.Fatalf("the schema is %v", schema)
	}
	if req, _ := schema["required"].([]any); len(req) != 1 || req[0] != "path" {
		t.Fatalf("required is %v", schema["required"])
	}
}

// Nothing is cached unless it was asked for: a prompt that changes
// every turn would pay to write a cache nobody reads.
func TestAnthropicCachesNothingUnasked(t *testing.T) {
	p, w := anthropicScene(t)
	req := Request{
		System:   "you are a supervisor",
		Messages: []Message{User("how many gaps?")},
		Tools:    tool.NewRegistry(stub{name: "read"}).Definitions(),
	}
	if _, err := p.Stream(t.Context(), req, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if body := w.raw(0); strings.Contains(body, "cache_control") {
		t.Fatalf("cached without being asked: %s", body)
	}
}

// Calls that ran in parallel come back as tool_result blocks in one
// user message, which is what the API wants and is not what a list of
// messages looks like.
func TestAnthropicParallelResultsAreOneMessage(t *testing.T) {
	p, w := anthropicScene(t)
	req := Request{Messages: []Message{
		User("how many gaps?"),
		Assistant("Let me look. ",
			Call{ID: "toolu_1", Name: "read", Arguments: `{"path":"GAPS.md"}`},
			Call{ID: "toolu_2", Name: "list", Arguments: `{"dir":"."}`}),
		Answer("toolu_1", "nine rows"),
		Failure("toolu_2", "error: nothing was done: you were asked, and said no"),
	}}
	if _, err := p.Stream(t.Context(), req, func(Event) {}); err != nil {
		t.Fatal(err)
	}

	msgs, _ := w.sent(t, 0)["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("%d messages, want user / assistant / user: %v", len(msgs), msgs)
	}
	assistant, _ := msgs[1].(map[string]any)
	blocks, _ := assistant["content"].([]any)
	if assistant["role"] != "assistant" || len(blocks) != 3 {
		t.Fatalf("the assistant turn is %v", assistant)
	}
	use, _ := blocks[1].(map[string]any)
	if use["type"] != "tool_use" || use["id"] != "toolu_1" {
		t.Fatalf("the first call is %v", use)
	}
	if input, _ := use["input"].(map[string]any); input["path"] != "GAPS.md" {
		t.Fatalf("the call's input is %v", use["input"])
	}

	results, _ := msgs[2].(map[string]any)
	got, _ := results["content"].([]any)
	if results["role"] != "user" || len(got) != 2 {
		t.Fatalf("the results are %v", results)
	}
	one, _ := got[0].(map[string]any)
	two, _ := got[1].(map[string]any)
	if one["type"] != "tool_result" || one["tool_use_id"] != "toolu_1" || one["is_error"] != false {
		t.Fatalf("the first result is %v", one)
	}
	if two["is_error"] != true {
		t.Fatalf("a refused call did not come back as an error: %v", two)
	}
}

// Adaptive is the default and says nothing at all; off says so; a
// budget is never sent, because a model that thinks adaptively is the
// wrong model to hand one to.
func TestAnthropicThinkingAndEffort(t *testing.T) {
	for _, c := range []struct {
		name     string
		req      Request
		thinking any
		effort   any
	}{
		{"nothing said", Request{}, nil, nil},
		{"adaptive", Request{Thinking: ThinkingAdaptive}, "adaptive", nil},
		{"off", Request{Thinking: ThinkingOff}, "disabled", nil},
		{"effort", Request{Effort: EffortXHigh}, nil, "xhigh"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, w := anthropicScene(t)
			if _, err := p.Stream(t.Context(), c.req, func(Event) {}); err != nil {
				t.Fatal(err)
			}
			body := w.sent(t, 0)
			thinking, _ := body["thinking"].(map[string]any)
			if c.thinking == nil {
				if _, said := body["thinking"]; said {
					t.Fatalf("thinking is %v, and should not have been sent", body["thinking"])
				}
			} else if thinking["type"] != c.thinking {
				t.Fatalf("thinking is %v, want %v", body["thinking"], c.thinking)
			}
			if _, budgeted := thinking["budget_tokens"]; budgeted {
				t.Fatalf("sent a budget: %v", thinking)
			}
			out, _ := body["output_config"].(map[string]any)
			if c.effort == nil {
				if _, said := out["effort"]; said {
					t.Fatalf("effort is %v, and should not have been sent", out)
				}
			} else if out["effort"] != c.effort {
				t.Fatalf("effort is %v, want %v", out, c.effort)
			}
		})
	}
}

// The two the API insists on, so a caller with no opinion has one.
func TestAnthropicDefaults(t *testing.T) {
	p, w := anthropicScene(t)
	if _, err := p.Stream(t.Context(), Request{}, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	body := w.sent(t, 0)
	if DefaultAnthropicModel != "claude-opus-5" {
		t.Fatalf("the default model is %q", DefaultAnthropicModel)
	}
	if body["model"] != DefaultAnthropicModel {
		t.Fatalf("model is %v", body["model"])
	}
	if body["max_tokens"] != float64(DefaultAnthropicMaxTokens) {
		t.Fatalf("max_tokens is %v", body["max_tokens"])
	}
}

// A refusal is not a failure: it happened, it was paid for, and the
// person is told which category tripped.
func TestAnthropicRefusal(t *testing.T) {
	t.Setenv(EnvAnthropicKey, "sk-ant-test")
	w := serve(t, events(
		"message_start", claudeStart,
		"message_delta", `{"type":"message_delta","delta":{"stop_reason":"refusal",`+
			`"stop_details":{"type":"refusal","category":"cyber","explanation":"that is an exploit"}},`+
			`"usage":{"input_tokens":12,"output_tokens":3}}`,
		"message_stop", claudeStop,
	))
	var got collect
	used, err := NewAnthropic(w.URL, "sk-ant-test").Stream(t.Context(), Request{}, got.on)
	if err != nil {
		t.Fatalf("a refusal is not an error from Stream: %v", err)
	}
	if used.StopReason != "refusal" {
		t.Fatalf("stop reason is %q", used.StopReason)
	}
	if len(got.fails) != 1 || !strings.Contains(got.fails[0], "cyber") ||
		!strings.Contains(got.fails[0], "that is an exploit") {
		t.Fatalf("the refusal reads %q", got.fails)
	}
}

// Thinking, when the model shows any, is delivered apart from the
// reply.
func TestAnthropicThinkingDeltas(t *testing.T) {
	t.Setenv(EnvAnthropicKey, "sk-ant-test")
	w := serve(t, events(
		"message_start", claudeStart,
		"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"the file is small"}}`,
		"content_block_stop", claudeTextShut,
		"content_block_start", claudeTextOpen,
		"content_block_delta", claudeAnswered,
		"content_block_stop", claudeTextShut,
		"message_delta", claudeStopTurn,
		"message_stop", claudeStop,
	))
	var got collect
	if _, err := NewAnthropic(w.URL, "sk-ant-test").Stream(t.Context(), Request{}, got.on); err != nil {
		t.Fatal(err)
	}
	if got.think.String() != "the file is small" || got.text.String() != "Nine rows." {
		t.Fatalf("thought %q, said %q", got.think.String(), got.text.String())
	}
}

// Arguments that did not parse are not a call. Running one would be
// running something nobody can read.
func TestAnthropicUnreadableToolInput(t *testing.T) {
	t.Setenv(EnvAnthropicKey, "sk-ant-test")
	w := serve(t, events(
		"message_start", claudeStart,
		"content_block_start", claudeToolOpen,
		"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
		"content_block_stop", claudeToolShut,
		"message_delta", claudeStopTool,
		"message_stop", claudeStop,
	))
	var got collect
	if _, err := NewAnthropic(w.URL, "sk-ant-test").Stream(t.Context(), Request{}, got.on); err != nil {
		t.Fatal(err)
	}
	if len(got.calls) != 0 {
		t.Fatalf("delivered a call it could not read: %+v", got.calls)
	}
	if len(got.fails) != 1 || !strings.Contains(got.fails[0], "read") {
		t.Fatalf("said %q", got.fails)
	}
}

// A tool with no arguments at all still sends an object.
func TestAnthropicEmptyToolInput(t *testing.T) {
	t.Setenv(EnvAnthropicKey, "sk-ant-test")
	w := serve(t, events(
		"message_start", claudeStart,
		"content_block_start", claudeToolOpen,
		"content_block_stop", claudeToolShut,
		"message_delta", claudeStopTool,
		"message_stop", claudeStop,
	))
	var got collect
	if _, err := NewAnthropic(w.URL, "sk-ant-test").Stream(t.Context(), Request{}, got.on); err != nil {
		t.Fatal(err)
	}
	if len(got.calls) != 1 || got.calls[0].Arguments != "{}" {
		t.Fatalf("calls are %+v", got.calls)
	}
}

// An endpoint that says no comes back as an error, with what it said.
func TestAnthropicRefusedKey(t *testing.T) {
	t.Setenv(EnvAnthropicKey, "sk-ant-test")
	w := serve(t)
	w.stop(http.StatusUnauthorized, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	_, err := NewAnthropic(w.URL, "sk-ant-wrong").Stream(t.Context(), Request{}, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), "invalid x-api-key") {
		t.Fatalf("error is %v", err)
	}
}

// The schema a tool wrote is the schema the model is told about, and
// a tool that wrote none still gets an object.
func TestInputSchema(t *testing.T) {
	s := inputSchema(json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}},"required":["a"],"additionalProperties":false}`))
	props, _ := s.Properties.(map[string]any)
	if _, ok := props["a"]; !ok {
		t.Fatalf("properties are %v", s.Properties)
	}
	if len(s.Required) != 1 || s.Required[0] != "a" {
		t.Fatalf("required is %v", s.Required)
	}
	if s.ExtraFields["additionalProperties"] != false {
		t.Fatalf("extras are %v", s.ExtraFields)
	}
	empty := inputSchema(nil)
	if props, _ := empty.Properties.(map[string]any); props == nil || len(props) != 0 {
		t.Fatalf("an absent schema became %v", empty.Properties)
	}
}
