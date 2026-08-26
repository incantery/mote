// Package provider is the wire, behind one interface.
//
// A loop over a model wants four things: send these messages and
// these tools, hand me what comes back as it arrives, tell me what it
// cost, and stop when I say stop. That is Provider.Stream, and it is
// the whole interface. Everything a particular API does differently —
// how it fragments a tool call, what it calls a cached token, whether
// it has an opinion about thinking — is settled inside a provider,
// before any of it reaches the loop.
//
// Nothing here knows about a terminal, a session or a policy. A
// Request is messages and tools; an Event is one thing that arrived;
// a Usage is what the exchange spent. A harness that swaps OpenAI for
// Anthropic changes one constructor and nothing else.
//
//	p := provider.NewAnthropic("", os.Getenv("ANTHROPIC_API_KEY"))
//	use, err := p.Stream(ctx, provider.Request{
//		System:   prompt,
//		Messages: []provider.Message{provider.User("what changed?")},
//		Tools:    reg.Definitions(),
//	}, func(ev provider.Event) {
//		switch ev.Kind {
//		case provider.KindDelta:
//			print(ev.Text)
//		case provider.KindToolCall:
//			run(ev.Call)
//		}
//	})
package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/incantery/mote/tool"
)

// Provider is one model endpoint, streamed.
//
// Stream sends the request and calls fn for every event until the
// model's turn is over, then returns what it cost. fn is called on
// Stream's own goroutine, in order, and never after Stream returns —
// so an implementation of it needs no lock. It has no error to
// return: a consumer that wants to stop cancels ctx.
//
// Cancelling ctx must end the stream. An error means the exchange did
// not happen, or stopped happening — a socket that closed, a key that
// was refused. A model that declined to answer is not an error: that
// is a KindError event and a Usage with the stop reason in it, because
// it happened, and it was paid for.
type Provider interface {
	Stream(ctx context.Context, req Request, fn func(Event)) (Usage, error)
}

// --- what is asked -------------------------------------------------------

// Request is one turn's worth of everything the model is told.
//
// The last three fields are hints, not commands: a provider that has
// no opinion about thinking, no effort dial, or no way to be told
// what to cache ignores them rather than failing. What each provider
// does with them is on its own documentation.
type Request struct {
	// Model is which model to call. Empty means the provider's own
	// default, which is what a harness with no opinion should send.
	Model string
	// System is the system prompt, whole.
	System string
	// Messages are the conversation, oldest first.
	Messages []Message
	// Tools are what the model may call, in the shape the registry
	// already hands out. Empty means no tools this turn.
	Tools []tool.Definition
	// MaxTokens caps the reply. Zero means the provider's default.
	MaxTokens int

	// Thinking asks for reasoning, or asks for none. The zero value
	// says nothing at all, which leaves the model's own default —
	// the right answer for a model that thinks adaptively.
	Thinking Thinking
	// Effort is how hard to work at it, where that is a dial.
	Effort Effort
	// CacheSystem asks the provider to cache the stable prefix — the
	// tools and the system prompt — where it can be asked. It costs a
	// little on the turn that writes the cache and saves most of the
	// prompt on every turn after it.
	CacheSystem bool
}

// Thinking is whether the model reasons before it answers.
type Thinking string

const (
	// ThinkingOff turns reasoning off where it can be turned off.
	ThinkingOff Thinking = "off"
	// ThinkingAdaptive lets the model decide how much to think, per
	// turn. It is what a recent Claude does when nobody says.
	ThinkingAdaptive Thinking = "adaptive"
)

// Effort is how much work to put into the answer. The words are the
// ones the APIs use.
type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

// The three roles a message can have. There is no system role: the
// system prompt is a field, because half the APIs do not have one and
// the other half want it in a different place.
const (
	// RoleUser is the person, or a batch of tool results on their way
	// back — which is the same turn as far as an API is concerned.
	RoleUser = "user"
	// RoleAssistant is the model, including any tools it asked for.
	RoleAssistant = "assistant"
	// RoleTool is one tool result, tied to the call it answers.
	RoleTool = "tool"
)

// Message is one turn of the conversation.
//
// Which fields carry meaning depends on Role: a user message is Text,
// an assistant message is Text and any Calls it made, a tool message
// is CallID, Text and whether it went wrong.
type Message struct {
	Role string
	// Text is what was said, or what a tool returned.
	Text string
	// Calls are the tools an assistant message asked for.
	Calls []Call
	// CallID is which call a tool message answers.
	CallID string
	// Error says a tool message is a failure rather than a result.
	// Some APIs mark it; the ones that do not are told in the text.
	Error bool
}

// Call is a tool the model asked for.
type Call struct {
	// ID is the API's, and the tool result must quote it back.
	ID string
	// Name is the tool's name, as the registry knows it.
	Name string
	// Arguments is JSON as the model wrote it. It is a string because
	// nothing between here and the tool needs to read it.
	Arguments string
}

// User is something the person said.
func User(text string) Message { return Message{Role: RoleUser, Text: text} }

// Assistant is a model turn: what it said, and what it asked to call.
func Assistant(text string, calls ...Call) Message {
	return Message{Role: RoleAssistant, Text: text, Calls: calls}
}

// Answer is a tool result, for the call with that id.
func Answer(id, text string) Message {
	return Message{Role: RoleTool, CallID: id, Text: text}
}

// Failure is a tool result that went wrong. The text still goes to
// the model — a tool that failed is information, not an outage.
func Failure(id, text string) Message {
	return Message{Role: RoleTool, CallID: id, Text: text, Error: true}
}

// --- what arrives --------------------------------------------------------

// Kind says what an Event is. The zero Kind is not valid.
type Kind string

const (
	// KindDelta is a piece of the reply, in order. Text holds it.
	KindDelta Kind = "delta"
	// KindThinking is a piece of the model's reasoning, from a
	// provider that shows any. A provider that does not never sends
	// one, and a loop that does not care ignores it.
	KindThinking Kind = "thinking"
	// KindToolCall is a tool the model asked for: Call holds it,
	// whole. However many fragments the wire cut the arguments into,
	// one of these arrives once, complete.
	KindToolCall Kind = "tool_call"
	// KindError is something the person should be told that did not
	// end the exchange — a refusal, most of the time. Text says what.
	// An exchange that ended is Stream's error instead.
	KindError Kind = "error"
)

// Event is one thing that arrived.
type Event struct {
	Kind Kind
	// Text is the delta, the thought, or the error.
	Text string
	// Call is the tool call, on KindToolCall.
	Call Call
}

// Delta is a piece of the reply.
func Delta(text string) Event { return Event{Kind: KindDelta, Text: text} }

// Thought is a piece of the model's reasoning.
func Thought(text string) Event { return Event{Kind: KindThinking, Text: text} }

// Calling is a complete tool call.
func Calling(id, name, args string) Event {
	return Event{Kind: KindToolCall, Call: Call{ID: id, Name: name, Arguments: args}}
}

// Fail is something that went wrong without ending the exchange.
func Fail(text string) Event { return Event{Kind: KindError, Text: text} }

// Failf is Fail with formatting.
func Failf(format string, a ...any) Event { return Fail(fmt.Sprintf(format, a...)) }

// --- what it cost --------------------------------------------------------

// Usage is what one Stream spent.
//
// The four counts do not overlap: a token read from the cache is in
// CacheRead and nowhere else, and Input is what was charged as fresh
// input. Providers disagree about this on the wire — OpenAI's
// prompt_tokens includes the cached ones, Anthropic's input_tokens
// does not — and each provider corrects its own numbers on the way
// out, so that Input+CacheRead+CacheWrite is the prompt whoever
// answered.
type Usage struct {
	// Input is prompt tokens that were not served from a cache.
	Input int
	// Output is what the model generated, reasoning included.
	Output int
	// CacheRead is prompt tokens served from a cache, and CacheWrite
	// is prompt tokens written into one. Zero from a provider that
	// has no cache, or was not asked to use it.
	CacheRead  int
	CacheWrite int
	// Model is the model that actually answered, which is not always
	// the one that was asked for: an alias resolves to a version.
	Model string
	// StopReason is why the turn ended, in the provider's own word —
	// "stop", "tool_calls", "end_turn", "tool_use", "refusal". It is
	// not normalised, because a vocabulary invented here would be a
	// third thing to learn and would lose what the API meant.
	StopReason string
}

// --- small shared helpers ------------------------------------------------

// arguments is a call's JSON, made safe to send: a model that asked
// for a tool with no arguments sends nothing at all, and an empty
// string is not JSON.
func arguments(s string) string {
	if s == "" {
		return "{}"
	}
	return s
}

// object reads a call's arguments as a map, for an API that wants the
// input as a value rather than as a string. Unreadable JSON becomes
// an empty object: the tool will say so far better than a marshaller
// halfway to the wire.
func object(args string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(arguments(args)), &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}
