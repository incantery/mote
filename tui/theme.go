package tui

import "github.com/charmbracelet/lipgloss"

// Palette is the whole of the terminal's colour. It is deliberately
// small, and deliberately ANSI: colours 1–6 and 8 are whatever the
// person's terminal theme says they are, so the result reads on a
// light background and a dark one without asking which we are on.
// Nothing here needs truecolor; a 16-colour terminal loses nothing.
type Palette struct {
	Text   lipgloss.TerminalColor // the reply, the input — usually the terminal's own
	Dim    lipgloss.TerminalColor // rules, notices, hints, costs
	Accent lipgloss.TerminalColor // tool names, the focused card

	User      lipgloss.TerminalColor
	Assistant lipgloss.TerminalColor
	Status    lipgloss.TerminalColor // the line you read while waiting
	Error     lipgloss.TerminalColor

	// Side-pane states.
	Working lipgloss.TerminalColor
	Idle    lipgloss.TerminalColor
	Blocked lipgloss.TerminalColor
	Done    lipgloss.TerminalColor
	Failed  lipgloss.TerminalColor

	// Markdown is the glamour style name: auto, dark, light, ascii,
	// notty, or any other glamour standard style.
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

// styles is the palette resolved against a renderer. Everything drawn
// goes through one of these, so a test can hand the model a renderer
// with colour switched off and get plain text out.
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

func newStyles(r *lipgloss.Renderer, p Palette) styles {
	s := styles{
		text:       r.NewStyle().Foreground(p.Text),
		dim:        r.NewStyle().Foreground(p.Dim),
		accent:     r.NewStyle().Foreground(p.Accent),
		user:       r.NewStyle().Foreground(p.User).Bold(true),
		assistant:  r.NewStyle().Foreground(p.Assistant).Bold(true),
		status:     r.NewStyle().Foreground(p.Status).Italic(true),
		errline:    r.NewStyle().Foreground(p.Error),
		tool:       r.NewStyle().Foreground(p.Accent).Bold(true),
		toolArgs:   r.NewStyle().Foreground(p.Dim),
		focused:    r.NewStyle().Foreground(p.Accent).Bold(true),
		rule:       r.NewStyle().Foreground(p.Dim),
		statusbar:  r.NewStyle().Foreground(p.Dim),
		hint:       r.NewStyle().Foreground(p.Dim),
		key:        r.NewStyle().Foreground(p.Accent),
		sideTitle:  r.NewStyle().Foreground(p.Dim).Bold(true),
		sideRule:   r.NewStyle().Foreground(p.Dim),
		suggest:    r.NewStyle().Foreground(p.Text),
		suggestSel: r.NewStyle().Foreground(p.Accent).Bold(true),
	}
	s.state = map[State]lipgloss.Style{
		Working: r.NewStyle().Foreground(p.Working),
		Idle:    r.NewStyle().Foreground(p.Idle),
		Blocked: r.NewStyle().Foreground(p.Blocked).Bold(true),
		Done:    r.NewStyle().Foreground(p.Done),
		Failed:  r.NewStyle().Foreground(p.Failed).Bold(true),
	}
	return s
}

func (s styles) forState(st State) lipgloss.Style {
	if v, ok := s.state[st]; ok {
		return v
	}
	return s.dim
}
