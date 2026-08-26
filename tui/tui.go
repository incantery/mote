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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	// SideRefresh is how often Side is called. Default 2s.
	SideRefresh time.Duration

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
	Palette *Palette
	// Renderer decides what colour actually reaches the screen. Nil
	// means lipgloss's default, which looks at the real terminal; a
	// test hands one that writes nowhere and paints nothing.
	Renderer *lipgloss.Renderer

	// Placeholder is the ghost text in an empty input.
	Placeholder string
	// Greeting is markdown shown once, above the first exchange.
	Greeting string

	// AltScreen and Mouse apply to Run only. Both default on; set
	// NoAltScreen or NoMouse to turn them off.
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
	if o.Renderer == nil {
		o.Renderer = lipgloss.DefaultRenderer()
	}
}

// Run puts the terminal on the screen and returns when it closes.
//
// New comes first, deliberately: it settles what colour the terminal
// is before anything owns stdin, so that nothing has to ask afterwards.
// One question is still asked, and it is not ours — bubbletea v1 asks
// the terminal for its background in its own package init, before main
// runs, which nothing in this process can get in front of. A terminal
// that answers answers in microseconds; one that never answers costs
// termenv's five-second timeout, once, before the first frame. It is
// gone in bubbletea v2.
func Run(a agent.Agent, opts Options) error {
	m := New(a, opts)
	var popts []tea.ProgramOption
	if !opts.NoAltScreen {
		popts = append(popts, tea.WithAltScreen())
	}
	if !opts.NoMouse {
		popts = append(popts, tea.WithMouseCellMotion())
	}
	_, err := tea.NewProgram(m, popts...).Run()
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

// Show puts a block of markdown in the transcript, rendered the way a
// reply is. This is how a /report prints what it fetched.
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

// SetSide replaces the rail now, without waiting for the next poll.
func SetSide(items []SideItem) tea.Cmd {
	return func() tea.Msg { return sideMsg{items} }
}

// Refresh calls Options.Side now.
func Refresh() tea.Cmd { return func() tea.Msg { return refreshMsg{} } }
