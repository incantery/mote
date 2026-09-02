// Package agent is the seam between a harness and its terminal.
//
// Everything the TUI needs from an agent is one method: say a thing,
// and get back a stream of events until one of them says done. That is
// deliberately less than an agent loop knows. A local loop over a model
// with tools satisfies it; so does a client for an agent that lives on
// the other end of an HTTP connection. Nothing here assumes the agent
// is in this process, and nothing here mentions a provider.
package agent

import (
	"context"
	"fmt"
	"time"
)

// Kind says what an Event is. The zero Kind is not valid; a producer
// that does not set one is a bug, and consumers ignore the event.
type Kind string

const (
	// KindDelta is a piece of the reply, in order. Text holds it.
	KindDelta Kind = "delta"
	// KindStatus is a line for the person to read while they wait —
	// what the agent is doing right now. Each one replaces the last;
	// the first delta clears it.
	KindStatus Kind = "status"
	// KindToolCall is a tool about to run: ID, Name, Args.
	KindToolCall Kind = "tool_call"
	// KindToolOutput is a piece of what a tool is printing while it
	// runs: ID, Text. A tool that takes a minute says so as it goes.
	// Any number of these arrive between a call and its result.
	KindToolOutput Kind = "tool_output"
	// KindToolResult is that tool having run: ID, Result, Duration,
	// and Cost if the agent knows one. It ends the call, whether
	// anything was streamed or not.
	KindToolResult Kind = "tool_result"
	// KindAsk is a tool the harness will not run without a person's
	// word: ID, Name, Args, and Text saying why it is asking. It does
	// not end the stream — the exchange is waiting on the answer, and
	// nothing else arrives until Answerer.Answer is called with the
	// same ID. A KindDone with an ask still open cancels it.
	KindAsk Kind = "ask"
	// KindNotice is something that happened outside this exchange —
	// a task finished, a file changed. It may arrive mid-reply. An
	// ID is optional and means the notice is about a thing rather
	// than a moment: the next one with the same ID replaces it.
	KindNotice Kind = "notice"
	// KindError is a failure the person should see. It does not end
	// the stream; KindDone does.
	KindError Kind = "error"
	// KindDone ends the exchange. Exactly one, last, always. It may
	// carry Cost, InputTokens and OutputTokens for the turn.
	KindDone Kind = "done"
)

// Event is one thing that happened. Which fields carry meaning depends
// on Kind; the rest are zero. It is a flat struct on purpose — it
// crosses process boundaries as often as it crosses function calls,
// and every field of it is JSON-shaped already.
type Event struct {
	Kind Kind `json:"kind"`

	// Text is the delta, the status line, the notice, the error, the
	// reason an ask is being asked, or a piece of a tool's output,
	// depending on Kind.
	Text string `json:"text,omitempty"`

	// ID ties a KindToolOutput, KindToolResult or KindAsk to its
	// KindToolCall — an ask carries the call's own id, because the
	// answer is about that call and nothing else — and gives a
	// KindNotice an identity: a later notice with the same
	// ID replaces the line the earlier one left, rather than leaving
	// two. A notice without one is a moment and keeps its place.
	ID string `json:"id,omitempty"`
	// Name is the tool's name (KindToolCall, KindAsk).
	Name string `json:"name,omitempty"`
	// Args is the tool's arguments as JSON (KindToolCall, KindAsk). It is a
	// string, not a map: the terminal shows it, it does not read it.
	Args string `json:"args,omitempty"`
	// Summary is the one line a tool call reads as, in whatever words
	// the harness would use out loud: "started a ship task in vera →
	// 05a40191". The terminal puts it on the collapsed card in place
	// of the arguments, which is the difference between a card a
	// person reads and one they decode. Optional on KindToolCall,
	// KindToolResult and KindAsk; a result's replaces its call's, so
	// a tool that only knows what it did once it has done it can
	// still say so. Empty means the terminal summarizes the arguments
	// itself.
	Summary string `json:"summary,omitempty"`
	// Result is what the tool returned (KindToolResult). Often long.
	// On a KindAsk it is the answer — "yes", "no" or "always" — which
	// is only ever set on an ask that has already been answered: a
	// recorded exchange, replayed, comes back with the answer in it
	// rather than as a question nobody can answer any more.
	Result string `json:"result,omitempty"`
	// Duration is how long the tool took (KindToolResult).
	Duration time.Duration `json:"duration,omitempty"`
	// Cost is USD spent, if the agent tracks it. Zero means unknown.
	// On KindToolResult it is that call's; on KindDone it is the
	// whole turn's model cost — what the exchange spent on the model
	// itself, not counting the tools, which reported their own.
	Cost float64 `json:"cost,omitempty"`
	// InputTokens and OutputTokens are the turn's, on KindDone, if
	// the agent knows them. Zero means it does not. Nothing here
	// says where the numbers came from: an agent that knows its
	// provider fills them in, and one that does not leaves them.
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`

	// Tone is what kind of notice a KindNotice is, when the kind is
	// worth a colour: a thing that is waiting on the person, or a
	// thing that failed. The terminal tints the notice's gutter by
	// it and nothing else — the words stay the harness's. Empty is
	// the ordinary case: something happened, and it is neither.
	Tone Tone `json:"tone,omitempty"`
	// Open is the command line that opens what a KindNotice is about
	// — "/report 1ea6a4b5" for a task that has written one. With it
	// the notice can be reached with tab and opened with enter, the
	// way a tool card is opened, instead of being retyped. It is the
	// whole line, slash and arguments, exactly as the person would
	// type it; the terminal runs it through the application's
	// Handle like anything typed.
	Open string `json:"open,omitempty"`
}

// Tone is the colour a notice is allowed to carry, and there are only
// two, because the transcript has only two things to say beyond
// "this happened": that something is asking, and that something has
// gone wrong. Red is failure only; an ask is not a crash, and a
// gutter that paints it like one teaches a glance to read alarm
// where there is none.
type Tone string

const (
	// ToneNeeds is a notice about something waiting on the person.
	ToneNeeds Tone = "needs"
	// ToneFailed is a notice about something that went wrong.
	ToneFailed Tone = "failed"
)

// The three answers to an ask. They are strings rather than a type
// because they cross a wire — a terminal over HTTP posts one — and a
// harness that receives a fourth should say so rather than guess.
const (
	// Yes runs this call, and asks again next time.
	Yes = "yes"
	// No does not run it. The model is told the person declined.
	No = "no"
	// Always runs it and stops asking about calls like it for the
	// rest of the session. What "like it" means is the harness's to
	// decide — see tool.Gate, which decides it as the directory for a
	// file and the program for a command.
	Always = "always"
)

// Answerer is an agent that can be answered.
//
// An agent that never asks does not implement it, and a terminal that
// finds an agent does not is a terminal that will never see an ask.
// Answer is called from the terminal, off the agent's own goroutine,
// with the ID of the KindAsk event. Answering an id that is not open
// — because the turn ended, or because it was answered already — is
// not an error: the person pressed a key at the same moment as
// something else happened, and that is not a failure.
type Answerer interface {
	Answer(ctx context.Context, id, choice string) error
}

// Agent is the whole of what a terminal needs.
//
// Send delivers text to a conversation and returns the events it
// causes. The channel is closed after the KindDone event; a Send that
// could not start at all returns an error instead of a channel.
// Cancelling ctx must end the stream — closing the channel, with or
// without a KindError first.
//
// Implementations must be safe for one Send at a time per Agent. The
// terminal does not start a second exchange while one is in flight.
type Agent interface {
	Send(ctx context.Context, conversation, text string) (<-chan Event, error)
}

// The constructors below exist so that a client mapping some other
// wire onto Event — Vera's /say frames, say — reads as a translation
// rather than as struct literals.

// Delta is a piece of reply text.
func Delta(text string) Event { return Event{Kind: KindDelta, Text: text} }

// Status is a line to show while waiting.
func Status(text string) Event { return Event{Kind: KindStatus, Text: text} }

// Notice is something that happened outside the exchange.
func Notice(text string) Event { return Event{Kind: KindNotice, Text: text} }

// About is a notice about something that has a name — a task, a file,
// a run. The next one about the same thing replaces the line this one
// left, so a task that changed four times says where it got to rather
// than how it got there.
func About(id, text string) Event {
	return Event{Kind: KindNotice, ID: id, Text: text}
}

// Fail is an error the person should see.
func Fail(text string) Event { return Event{Kind: KindError, Text: text} }

// Failf is Fail with formatting.
func Failf(format string, a ...any) Event { return Fail(fmt.Sprintf(format, a...)) }

// Err is Fail from an error. A nil error gives the zero Event, which
// consumers drop.
func Err(err error) Event {
	if err == nil {
		return Event{}
	}
	return Fail(err.Error())
}

// Call is a tool about to run.
func Call(id, name, args string) Event {
	return Event{Kind: KindToolCall, ID: id, Name: name, Args: args}
}

// WithSummary is the same event with the sentence a person should
// read on it. It is a method rather than a second set of
// constructors: every event that can carry a summary is already made
// by one, and Call(...).WithSummary("read the README") is the whole
// of the addition.
func (e Event) WithSummary(summary string) Event {
	e.Summary = summary
	return e
}

// WithTone is the same notice with a colour on its gutter — see Tone.
func (e Event) WithTone(t Tone) Event {
	e.Tone = t
	return e
}

// WithOpen is the same notice with the command that opens what it is
// about, so that the terminal can offer it under enter.
func (e Event) WithOpen(cmd string) Event {
	e.Open = cmd
	return e
}

// Output is a piece of what a tool is printing as it runs.
func Output(id, text string) Event {
	return Event{Kind: KindToolOutput, ID: id, Text: text}
}

// Result is a tool having run. Pass cost 0 when it is not known.
func Result(id, result string, d time.Duration, cost float64) Event {
	return Event{Kind: KindToolResult, ID: id, Result: result, Duration: d, Cost: cost}
}

// Asking is a tool call the harness will not run without a word from
// the person. why is what it puts on the card: the policy's reason,
// in the profile's own words when it had any.
func Asking(id, name, args, why string) Event {
	return Event{Kind: KindAsk, ID: id, Name: name, Args: args, Text: why}
}

// Answered is an ask that has already been answered. It is what a
// recorded exchange holds: the question, and what the person said.
func Answered(id, name, args, why, choice string) Event {
	ev := Asking(id, name, args, why)
	ev.Result = choice
	return ev
}

// Done ends an exchange.
func Done() Event { return Event{Kind: KindDone} }

// Spent ends an exchange and says what the model itself cost: USD,
// and the turn's tokens. Zeros mean the agent does not know, which is
// the honest answer for an agent on the other end of a wire that does
// not tell it.
func Spent(cost float64, in, out int) Event {
	return Event{Kind: KindDone, Cost: cost, InputTokens: in, OutputTokens: out}
}
