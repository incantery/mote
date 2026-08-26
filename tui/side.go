package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// State is what a side-pane item is doing. The five are the states a
// person acts on differently: leave it, look at it, answer it, land
// it, or fix it.
type State string

const (
	Working State = "working"
	Idle    State = "idle"
	Blocked State = "blocked"
	Done    State = "done"
	Failed  State = "failed"
)

// SideItem is one row of the rail. The application owns the list and
// what the words mean; the terminal only draws it.
type SideItem struct {
	ID       string
	Title    string
	Subtitle string
	State    State
	// Current marks the one the person is on, if any.
	Current bool
}

func glyph(s State) string {
	switch s {
	case Working:
		return "●"
	case Blocked:
		return "!"
	case Done:
		return "✓"
	case Failed:
		return "✗"
	default:
		return "·"
	}
}

// renderSide draws the rail: one item as two lines, a marker on the
// current one, states carrying the colour. Nothing here scrolls — a
// rail that needs scrolling is a list, and a list wants its own pane —
// but it says what it could not show. Four tasks and four of nine have
// to look different, or a person reads a short rail as a quiet fleet.
func (m *Model) renderSide(w, h int) string {
	inner := w - 2
	if inner < 8 {
		inner = 8
	}
	title := m.opts.SideTitle
	if title == "" {
		title = "side"
	}
	lines := []string{m.st.sideTitle.Render(ansi.Truncate(title, inner, "…"))}
	lines = append(lines, m.st.sideRule.Render(strings.Repeat("─", inner)))

	if len(m.side) == 0 {
		lines = append(lines, m.st.dim.Render("nothing here"))
	}

	// Two lines an item, and the last line held back for the count as
	// soon as there is more than there is room for.
	room := max(h-len(lines), 0)
	shown := len(m.side)
	if shown*2 > room {
		shown = max((room-1)/2, 0)
	}
	for _, it := range m.side[:shown] {
		mark := "  "
		if it.Current {
			mark = "▸ "
		}
		st := m.st.forState(it.State)
		head := mark + st.Render(glyph(it.State)) + " " + m.st.text.Render(ansi.Truncate(it.Title, inner-4, "…"))
		lines = append(lines, head)
		sub := it.Subtitle
		if sub == "" {
			sub = string(it.State)
		} else {
			sub = string(it.State) + " · " + sub
		}
		if it.ID != "" {
			sub = it.ID + " · " + sub
		}
		lines = append(lines, m.st.dim.Render("    "+ansi.Truncate(oneLine(sub), inner-4, "…")))
	}
	if n := len(m.side) - shown; n > 0 {
		lines = append(lines, m.st.dim.Render(fmt.Sprintf("  +%d more", n)))
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	// A left border makes the rail a rail rather than a second column
	// of text that happens to be over there.
	for i, l := range lines {
		lines[i] = m.st.sideRule.Render("│ ") + l
	}
	return lipgloss.NewStyle().Width(w).MaxWidth(w).Render(strings.Join(lines, "\n"))
}
