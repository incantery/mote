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

// The thinking a turn did, kept and handed back.
//
// This is the one thing the Messages API is unforgiving about. With
// extended thinking on — which is what a Claude 5 does when nobody
// says otherwise — an assistant turn that thought and then called a
// tool has to come back with its thinking blocks, and their
// signatures, in front of the tool call. Drop them and the API
// refuses the conversation, not the turn.
const (
	claudeThinkOpen = `{"type":"content_block_start","index":0,` +
		`"content_block":{"type":"thinking","thinking":"","signature":""}}`
	claudeThought = `{"type":"content_block_delta","index":0,` +
		`"delta":{"type":"thinking_delta","thinking":"GAPS.md is small enough to read whole"}}`
	claudeSigned = `{"type":"content_block_delta","index":0,` +
		`"delta":{"type":"signature_delta","signature":"ErUBCkYIBRgCIkA"}}`
	claudeThinkShut = `{"type":"content_block_stop","index":0}`
	claudeRedacted  = `{"type":"content_block_start","index":1,` +
		`"content_block":{"type":"redacted_thinking","data":"EroBCkYIBRgCKk"}}`
	claudeRedactedShut = `{"type":"content_block_stop","index":1}`
)

// thoughtThenTool is a first turn that thinks, has a thought it will
// not show, and then calls a tool.
func thoughtThenTool() string {
	return events(
		"message_start", claudeStart,
		"content_block_start", claudeThinkOpen,
		"content_block_delta", claudeThought,
		"content_block_delta", claudeSigned,
		"content_block_stop", claudeThinkShut,
		"content_block_start", claudeRedacted,
		"content_block_stop", claudeRedactedShut,
		"content_block_start", claudeToolOpen,
		"content_block_delta", claudeToolOne,
		"content_block_delta", claudeToolTwo,
		"content_block_delta", claudeToolThree,
		"content_block_stop", claudeToolShut,
		"message_delta", claudeStopTool,
		"message_stop", claudeStop,
	)
}

// blocks is the content of the nth request's mth message.
func blocks(t *testing.T, w *wire, n, m int) []map[string]any {
	t.Helper()
	msgs, _ := w.sent(t, n)["messages"].([]any)
	if m >= len(msgs) {
		t.Fatalf("request %d has %d messages", n, len(msgs))
	}
	msg, _ := msgs[m].(map[string]any)
	content, ok := msg["content"].([]any)
	if !ok {
		t.Fatalf("message %d is not blocks: %v", m, msg)
	}
	out := make([]map[string]any, 0, len(content))
	for _, b := range content {
		block, _ := b.(map[string]any)
		out = append(out, block)
	}
	return out
}

func TestAnthropicRoundTripsThinking(t *testing.T) {
	t.Setenv(EnvAnthropicKey, "sk-ant-test")
	w := serve(t, thoughtThenTool(), claudeTurnTwo())
	p := NewAnthropic(w.URL, "sk-ant-test")

	req := Request{Messages: []Message{User("how many gaps?")}}
	var first collect
	if _, err := p.Stream(t.Context(), req, first.on); err != nil {
		t.Fatal(err)
	}
	if first.think.String() != "GAPS.md is small enough to read whole" {
		t.Fatalf("thought %q", first.think.String())
	}
	// The harness is handed one opaque thing, at the end, and does
	// not read it.
	if len(first.raw) == 0 {
		t.Fatal("nothing came back to hand over")
	}
	if !strings.Contains(string(first.raw), "ErUBCkYIBRgCIkA") {
		t.Fatalf("the signature is not in what was kept: %s", first.raw)
	}

	// Round two: the assistant turn, with what the provider asked to
	// be kept, and the tool result.
	assistant := Assistant(first.text.String(), first.calls[0])
	assistant.Raw = first.raw
	req.Messages = append(req.Messages, assistant, Answer(first.calls[0].ID, "nine rows"))
	var second collect
	if _, err := p.Stream(t.Context(), req, second.on); err != nil {
		t.Fatal(err)
	}
	if second.text.String() != "Nine rows." {
		t.Fatalf("said %q", second.text.String())
	}

	// And this is what went on the wire: the thinking first, with its
	// signature, then the redacted one, then the tool call it led to.
	got := blocks(t, w, 1, 1)
	if len(got) != 3 {
		t.Fatalf("%d blocks in the assistant turn: %v", len(got), got)
	}
	if got[0]["type"] != "thinking" || got[0]["signature"] != "ErUBCkYIBRgCIkA" ||
		got[0]["thinking"] != "GAPS.md is small enough to read whole" {
		t.Fatalf("the first block is %v", got[0])
	}
	if got[1]["type"] != "redacted_thinking" || got[1]["data"] != "EroBCkYIBRgCKk" {
		t.Fatalf("the second block is %v", got[1])
	}
	if got[2]["type"] != "tool_use" || got[2]["id"] != "toolu_1" {
		t.Fatalf("the third block is %v", got[2])
	}
	// The turn said nothing, so there is no text block — and an empty
	// one would be a 400 of its own.
	for _, b := range got {
		if b["type"] == "text" {
			t.Fatalf("an empty text block went out: %v", b)
		}
	}
}

// A turn with nothing to keep hands nothing over, and a request with
// thinking turned off must not carry somebody's thinking blocks.
func TestAnthropicKeepsNothingWhenThereIsNothing(t *testing.T) {
	t.Setenv(EnvAnthropicKey, "sk-ant-test")
	w := serve(t, claudeTurnOne(), claudeTurnTwo())
	p := NewAnthropic(w.URL, "sk-ant-test")

	var first collect
	if _, err := p.Stream(t.Context(), Request{Messages: []Message{User("how many gaps?")}}, first.on); err != nil {
		t.Fatal(err)
	}
	if first.raw != nil {
		t.Fatalf("a turn that did not think kept %s", first.raw)
	}

	// Thinking off, and an assistant turn that has blocks from when
	// it was on: they stay behind.
	assistant := Assistant("Let me look. ", Call{ID: "toolu_1", Name: "read", Arguments: `{"path":"GAPS.md"}`})
	assistant.Raw = json.RawMessage(`[{"type":"thinking","thinking":"hm","signature":"sig"}]`)
	var second collect
	if _, err := p.Stream(t.Context(), Request{
		Thinking: ThinkingOff,
		Messages: []Message{User("how many gaps?"), assistant, Answer("toolu_1", "nine rows")},
	}, second.on); err != nil {
		t.Fatal(err)
	}
	for _, b := range blocks(t, w, 1, 1) {
		if b["type"] == "thinking" {
			t.Fatalf("a request with thinking off carried one: %v", b)
		}
	}
	// Raw that is not this provider's is not a reason to fail a turn.
	assistant.Raw = json.RawMessage(`"somebody else's"`)
	if _, err := p.Stream(t.Context(), Request{
		Messages: []Message{User("again?"), assistant, Answer("toolu_1", "nine rows")},
	}, (&collect{}).on); err != nil {
		t.Fatalf("unreadable Raw ended the turn: %v", err)
	}
}

// Thinking and its display are two dials. display lives on the
// adaptive config, so asking for one asks for the other.
func TestAnthropicThinkingConfig(t *testing.T) {
	t.Setenv(EnvAnthropicKey, "sk-ant-test")
	for _, c := range []struct {
		name string
		req  Request
		want any
	}{
		{"nothing said", Request{}, nil},
		{"adaptive", Request{Thinking: ThinkingAdaptive},
			map[string]any{"type": "adaptive"}},
		{"off", Request{Thinking: ThinkingOff},
			map[string]any{"type": "disabled"}},
		{"omitted", Request{ThinkingDisplay: DisplayOmitted},
			map[string]any{"type": "adaptive", "display": "omitted"}},
		{"summarized, said out loud", Request{Thinking: ThinkingAdaptive, ThinkingDisplay: DisplaySummarized},
			map[string]any{"type": "adaptive", "display": "summarized"}},
		// Off wins: a caller who turned thinking off does not get an
		// adaptive config because they also said how to show it.
		{"off beats display", Request{Thinking: ThinkingOff, ThinkingDisplay: DisplayOmitted},
			map[string]any{"type": "disabled"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := serve(t, claudeTurnTwo())
			if _, err := NewAnthropic(w.URL, "sk-ant-test").Stream(t.Context(), c.req, func(Event) {}); err != nil {
				t.Fatal(err)
			}
			got := w.sent(t, 0)["thinking"]
			if c.want == nil {
				if got != nil {
					t.Fatalf("thinking is %v, and nothing was said about it", got)
				}
				return
			}
			want, _ := c.want.(map[string]any)
			have, _ := got.(map[string]any)
			if len(have) != len(want) {
				t.Fatalf("thinking is %v, want %v", got, c.want)
			}
			for k, v := range want {
				if have[k] != v {
					t.Fatalf("thinking is %v, want %v", got, c.want)
				}
			}
			// budget_tokens is never sent: it is the wrong way to ask
			// a model that thinks adaptively.
			if _, ok := have["budget_tokens"]; ok {
				t.Fatalf("budget_tokens went out: %v", got)
			}
		})
	}
}
