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
	// KindToolResult is that tool having run: ID, Result, Duration,
	// and Cost if the agent knows one.
	KindToolResult Kind = "tool_result"
	// KindNotice is something that happened outside this exchange —
	// a task finished, a file changed. It may arrive mid-reply.
	KindNotice Kind = "notice"
	// KindError is a failure the person should see. It does not end
	// the stream; KindDone does.
	KindError Kind = "error"
	// KindDone ends the exchange. Exactly one, last, always.
	KindDone Kind = "done"
)

// Event is one thing that happened. Which fields carry meaning depends
// on Kind; the rest are zero. It is a flat struct on purpose — it
// crosses process boundaries as often as it crosses function calls,
// and every field of it is JSON-shaped already.
type Event struct {
	Kind Kind `json:"kind"`

	// Text is the delta, the status line, the notice, or the error,
	// depending on Kind.
	Text string `json:"text,omitempty"`

	// ID ties a KindToolResult to its KindToolCall.
	ID string `json:"id,omitempty"`
	// Name is the tool's name (KindToolCall).
	Name string `json:"name,omitempty"`
	// Args is the tool's arguments as JSON (KindToolCall). It is a
	// string, not a map: the terminal shows it, it does not read it.
	Args string `json:"args,omitempty"`
	// Result is what the tool returned (KindToolResult). Often long.
	Result string `json:"result,omitempty"`
	// Duration is how long the tool took (KindToolResult).
	Duration time.Duration `json:"duration,omitempty"`
	// Cost is USD spent, if the agent tracks it. Zero means unknown.
	Cost float64 `json:"cost,omitempty"`
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

// Result is a tool having run. Pass cost 0 when it is not known.
func Result(id, result string, d time.Duration, cost float64) Event {
	return Event{Kind: KindToolResult, ID: id, Result: result, Duration: d, Cost: cost}
}

// Done ends an exchange.
func Done() Event { return Event{Kind: KindDone} }
