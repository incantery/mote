package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Palette is the whole of the terminal's colour. It is deliberately
// small, and deliberately ANSI: colours 1–6 and 8 are whatever the
// person's terminal theme says they are, so the result reads on a
// light background and a dark one without asking which we are on.
// Nothing here needs truecolor; a 16-colour terminal loses nothing.
// The registers a transcript is written in — the person, the reply,
// the machinery, the world outside, what a command printed, and what
// went wrong — each want a colour of their own, or the screen is one
// grey slab. Warm is the person; cool is the agent and its tools; dim
// is everything that is not either of them talking.
type Palette struct {
	Text   color.Color // the reply, the input — usually the terminal's own
	Dim    color.Color // rules, hints, costs
	Accent color.Color // the focused card, a key on the ask line

	User      color.Color // what the person said — the warm one
	Assistant color.Color
	Status    color.Color // the line you read while waiting
	Error     color.Color

	// Tool is the cool one: the name on a card, and the machinery
	// around it. Not Accent — the focused card has to differ from
	// every other card, and it cannot if they are the same colour.
	Tool color.Color
	// Event is a notice from outside the exchange. Dim, and dimmer
	// than the reply on purpose: it is not the model talking.
	Event color.Color
	// Result is the gutter down a block a command printed — a /report,
	// a /policy. What the terminal was told to show, rather than what
	// the agent said.
	Result color.Color
	// Rule is the thin line between one exchange and the next.
	Rule color.Color
	// Needs is a rail item that is waiting on the person: a question
	// nobody answered, a report nobody read.
	Needs color.Color

	// Side-pane states.
	Working color.Color
	Idle    color.Color
	Blocked color.Color
	Done    color.Color
	Failed  color.Color

	// Markdown is the glamour style name: dark, light, ascii, notty, or
	// any other glamour standard style. "auto" — the default — means
	// let the terminal decide: the safe guess is drawn first and the
	// answer, when the terminal gives one, replaces it. A name here is
	// the person's own word and nothing overrides it. See resolveStyle.
	Markdown string
}

// DefaultPalette is the one the demo uses.
func DefaultPalette() Palette {
	return Palette{
		Text:      lipgloss.NoColor{},
		Dim:       lipgloss.Color("8"),
		Accent:    lipgloss.Color("6"),
		User:      lipgloss.Color("5"),
		Assistant: lipgloss.Color("4"),
		Status:    lipgloss.Color("3"),
		Error:     lipgloss.Color("1"),
		Tool:      lipgloss.Color("4"),
		Event:     lipgloss.Color("8"),
		Result:    lipgloss.Color("6"),
		Rule:      lipgloss.Color("8"),
		Needs:     lipgloss.Color("3"),
		Working:   lipgloss.Color("4"),
		Idle:      lipgloss.Color("8"),
		Blocked:   lipgloss.Color("3"),
		Done:      lipgloss.Color("2"),
		Failed:    lipgloss.Color("1"),
		Markdown:  "auto",
	}
}

// styles is the palette as lipgloss styles. Everything drawn goes
// through one of these. They hold colour at full fidelity; bubbletea
// downsamples on the way to the terminal, so a test that wants the
// layout on its own strips the escapes rather than switching a
// renderer off.
type styles struct {
	text   lipgloss.Style
	dim    lipgloss.Style
	accent lipgloss.Style

	user      lipgloss.Style
	assistant lipgloss.Style
	status    lipgloss.Style
	errline   lipgloss.Style

	tool     lipgloss.Style
	toolArgs lipgloss.Style
	focused  lipgloss.Style
	rule     lipgloss.Style

	event  lipgloss.Style
	result lipgloss.Style
	stamp  lipgloss.Style
	needs  lipgloss.Style

	statusbar lipgloss.Style
	hint      lipgloss.Style
	key       lipgloss.Style

	sideTitle lipgloss.Style
	sideRule  lipgloss.Style
	state     map[State]lipgloss.Style

	suggest    lipgloss.Style
	suggestSel lipgloss.Style
}

func newStyles(p Palette) styles {
	s := styles{
		text:       lipgloss.NewStyle().Foreground(p.Text),
		dim:        lipgloss.NewStyle().Foreground(p.Dim),
		accent:     lipgloss.NewStyle().Foreground(p.Accent),
		user:       lipgloss.NewStyle().Foreground(p.User).Bold(true),
		assistant:  lipgloss.NewStyle().Foreground(p.Assistant).Bold(true),
		status:     lipgloss.NewStyle().Foreground(p.Status).Italic(true),
		errline:    lipgloss.NewStyle().Foreground(p.Error),
		tool:       lipgloss.NewStyle().Foreground(p.Tool).Bold(true),
		toolArgs:   lipgloss.NewStyle().Foreground(p.Dim),
		focused:    lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		rule:       lipgloss.NewStyle().Foreground(p.Rule),
		event:      lipgloss.NewStyle().Foreground(p.Event),
		result:     lipgloss.NewStyle().Foreground(p.Result),
		stamp:      lipgloss.NewStyle().Foreground(p.Dim),
		needs:      lipgloss.NewStyle().Foreground(p.Needs).Bold(true),
		statusbar:  lipgloss.NewStyle().Foreground(p.Dim),
		hint:       lipgloss.NewStyle().Foreground(p.Dim),
		key:        lipgloss.NewStyle().Foreground(p.Accent),
		sideTitle:  lipgloss.NewStyle().Foreground(p.Dim).Bold(true),
		sideRule:   lipgloss.NewStyle().Foreground(p.Dim),
		suggest:    lipgloss.NewStyle().Foreground(p.Text),
		suggestSel: lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
	}
	s.state = map[State]lipgloss.Style{
		Working: lipgloss.NewStyle().Foreground(p.Working),
		Idle:    lipgloss.NewStyle().Foreground(p.Idle),
		Blocked: lipgloss.NewStyle().Foreground(p.Blocked).Bold(true),
		Done:    lipgloss.NewStyle().Foreground(p.Done),
		Failed:  lipgloss.NewStyle().Foreground(p.Failed).Bold(true),
	}
	return s
}

func (s styles) forState(st State) lipgloss.Style {
	if v, ok := s.state[st]; ok {
		return v
	}
	return s.dim
}
