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
type Palette struct {
	Text   color.Color // the reply, the input — usually the terminal's own
	Dim    color.Color // rules, notices, hints, costs
	Accent color.Color // tool names, the focused card

	User      color.Color
	Assistant color.Color
	Status    color.Color // the line you read while waiting
	Error     color.Color

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
		Accent:    lipgloss.Color("4"),
		User:      lipgloss.Color("6"),
		Assistant: lipgloss.Color("5"),
		Status:    lipgloss.Color("3"),
		Error:     lipgloss.Color("1"),
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
		tool:       lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		toolArgs:   lipgloss.NewStyle().Foreground(p.Dim),
		focused:    lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		rule:       lipgloss.NewStyle().Foreground(p.Dim),
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
