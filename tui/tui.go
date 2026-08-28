// Package tui is the terminal a person watches an agent in.
//
// It talks to exactly one thing — an agent.Agent — and knows nothing
// about how that agent works or where it runs. Everything the
// application wants to put on the screen that is not the conversation
// (the rail of items, the slash commands, the name in the status
// line) it supplies through Options.
package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/incantery/mote/agent"
	"github.com/incantery/mote/session"
)

// Options is everything the application tells the terminal. All of it
// has a working default except the parts only the application knows.
type Options struct {
	// Name and Model go in the status line: who is answering, on what.
	Name  string
	Model string
	// Conversation is the id passed to every Send. The application can
	// change it later with SetConversation.
	Conversation string
	// OnConversation is told the id whenever it changes — which is to
	// say whenever a SetConversation lands, because the terminal never
	// picks one itself. It is how an application that called Run
	// learns what /new decided; one that embedded the Model can read
	// Model.Conversation instead. It is called on the UI goroutine, so
	// keep the id and get out. Nothing calls it for Conversation
	// above: the application chose that one.
	OnConversation func(string)

	// Side supplies the rail on the right. It is called on a timer and
	// after every exchange, on the UI goroutine, so it must be quick —
	// read a cached value, do not make a request.
	Side func() []SideItem
	// SideTitle heads the rail. Default "side".
	SideTitle string
	// SideWidth is the rail's width in columns. Default 32.
	SideWidth int
	// SideMinWidth is the window width below which the rail hides
	// however it was toggled. Default 100.
	SideMinWidth int
	// SideRefresh is how often Side and StatusRight are called.
	// Default 2s.
	SideRefresh time.Duration
	// SideClosed starts with the rail hidden. It is the application's
	// opening position, not a lock: ctrl+t still shows it. An
	// application whose rail is a detail rather than the point of the
	// screen wants this.
	SideClosed bool

	// StatusRight is one line's worth of the application's own text,
	// on the right of the status line: what the person is looking at,
	// how many runs are in flight — whatever is true all the time
	// rather than worth a notice. It is called on the same timer as
	// Side and under the same rule: on the UI goroutine, so read a
	// cached value and get out. The terminal's key hints go before it
	// when there is room and are dropped when there is not.
	StatusRight func() string

	// Commands are offered as completion when the person types "/".
	Commands []Command
	// Handle runs one. name is without the slash, args is the rest of
	// the line. It returns a tea.Cmd so the work can happen off the UI
	// goroutine; have that Cmd return Note, Fail, Show, SetConversation
	// or any tea.Msg of the application's own.
	Handle func(name, args string) tea.Cmd

	// Session is the conversation on disk. Nil keeps everything in
	// memory, which is what a terminal over a stateless agent wants.
	// With one, the transcript and the input history are rebuilt from
	// it at New, and every exchange is appended as it ends. The
	// terminal never opens or closes it: the application chose the
	// file, and the application owns it.
	Session *session.Session

	// Notices carries events from outside any exchange — a task
	// finished, a file changed. Only KindNotice, KindError and
	// KindStatus mean anything here. The terminal reads it until it
	// is closed.
	Notices <-chan agent.Event

	// Palette is the colour. Nil means DefaultPalette.
	//
	// There is no renderer to resolve it against: in lipgloss v2 a
	// colour is a value and a style is a value, and what the terminal
	// can actually paint is decided once, by bubbletea, on the way
	// out. Whether the terminal is dark or light is decided the same
	// way — see Model.Init.
	Palette *Palette

	// Timestamps puts the time an exchange began on the rule above
	// it. Off by default: a conversation you are having does not need
	// clocking, and one you are reading back usually does.
	Timestamps bool

	// Placeholder is the ghost text in an empty input.
	Placeholder string
	// Greeting is markdown shown once, above the first exchange.
	Greeting string

	// AltScreen and Mouse are fields of the frame in bubbletea v2, so
	// they are decided by View and apply however the Model is run.
	// Both default on; set NoAltScreen or NoMouse to turn them off.
	NoAltScreen bool
	NoMouse     bool
}

func (o *Options) fill() {
	if o.Name == "" {
		o.Name = "agent"
	}
	if o.SideTitle == "" {
		o.SideTitle = "side"
	}
	if o.SideWidth == 0 {
		o.SideWidth = 32
	}
	if o.SideMinWidth == 0 {
		o.SideMinWidth = 100
	}
	if o.SideRefresh == 0 {
		o.SideRefresh = 2 * time.Second
	}
	if o.Placeholder == "" {
		o.Placeholder = "say something, or / for commands"
	}
	if o.Palette == nil {
		p := DefaultPalette()
		o.Palette = &p
	}
}

// Run puts the terminal on the screen and returns when it closes.
//
// There is nothing to set up here any more. The alt screen and the
// mouse are fields of the frame the Model draws, and what colour the
// terminal is is asked for after the first frame rather than before
// it — so Run is the program, and nothing blocks in front of it.
func Run(a agent.Agent, opts Options) error {
	_, err := tea.NewProgram(New(a, opts)).Run()
	return err
}

// --- what an application's Handle can send back -------------------------

type (
	noteMsg    struct{ text string }
	failMsg    struct{ text string }
	blockMsg   struct{ md string }
	convMsg    struct{ id string }
	sessionMsg struct{ s *session.Session }
	sideMsg    struct{ items []SideItem }
	refreshMsg struct{}
	modelMsg   struct{ name string }
)

// Note puts a dim line in the transcript.
func Note(format string, a ...any) tea.Cmd {
	s := fmt.Sprintf(format, a...)
	return func() tea.Msg { return noteMsg{s} }
}

// Fail puts a visible error in the transcript.
func Fail(format string, a ...any) tea.Cmd {
	s := fmt.Sprintf(format, a...)
	return func() tea.Msg { return failMsg{s} }
}

// Show puts a block of markdown in the transcript, down a gutter of
// its own: what a command printed, which is neither the agent talking
// nor a notice about the world. This is how a /report prints what it
// fetched.
func Show(markdown string) tea.Cmd {
	return func() tea.Msg { return blockMsg{markdown} }
}

// SetConversation changes the id future exchanges are sent under.
func SetConversation(id string) tea.Cmd {
	return func() tea.Msg { return convMsg{id} }
}

// SetSession changes the file future exchanges are appended to. It is
// the other half of SetConversation — a /new that changes the id has
// to change the record too, or the new conversation is written into
// the old one's file. It does not touch what is already on screen:
// the person is still looking at what happened, and a new file does
// not unhappen it.
func SetSession(s *session.Session) tea.Cmd {
	return func() tea.Msg { return sessionMsg{s} }
}

// SetModel changes the model named on the status line. Options.Model
// is where it starts; this is how it moves — an application whose
// person just chose another one in a picker says so with this.
func SetModel(name string) tea.Cmd {
	return func() tea.Msg { return modelMsg{name} }
}

// SetSide replaces the rail now, without waiting for the next poll.
func SetSide(items []SideItem) tea.Cmd {
	return func() tea.Msg { return sideMsg{items} }
}

// Refresh calls Options.Side now.
func Refresh() tea.Cmd { return func() tea.Msg { return refreshMsg{} } }
