package tui

import (
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Command is a slash command the application offers. The terminal
// completes it and shows the help; running it is the application's.
type Command struct {
	Name string // without the slash
	Help string
}

// inputHeight caps how tall the box grows before it scrolls itself.
const inputHeight = 6

// input is the box at the bottom: multiline, with a history you walk
// when it is empty, and completion for slash commands.
type input struct {
	ta textarea.Model

	history []string
	at      int    // index into history; len(history) means "not browsing"
	draft   string // what was typed before browsing started

	cmds []Command
	sugg []Command
	sel  int
}

func newInput(st styles, placeholder string) *input {
	ta := textarea.New()
	ta.Placeholder = placeholder
	ta.Prompt = "› "
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	// The box grows with what is in it, up to the cap. Bubbles v2 does
	// this itself, and counts the lines the way the box draws them —
	// so a long line that wraps grows the box too, which counting
	// newlines never did.
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = inputHeight
	ta.SetHeight(1)
	// The cursor is the terminal's own — see Model.View. A virtual one
	// is a styled block in the frame, which is what bubbles draws when
	// nobody can place a real one; here somebody can.
	ta.SetVirtualCursor(false)
	// Every one of the textarea's own styles, because the ones it
	// ships are a light set and a dark set and it is the Palette that
	// is meant to be the whole of the terminal's colour. Set here,
	// nothing in the box picks a colour of its own.
	sty := ta.Styles()
	for _, state := range []*textarea.StyleState{&sty.Focused, &sty.Blurred} {
		state.Base = st.text
		state.CursorLine = st.text
		state.CursorLineNumber = st.dim
		state.EndOfBuffer = st.dim
		state.LineNumber = st.dim
		state.Placeholder = st.dim
		state.Text = st.text
		state.Prompt = st.dim
		// Selected text is the only thing in the box that needs a
		// background, and reversing whatever is already there is a
		// background on any terminal without naming a colour.
		state.Selection = lipgloss.NewStyle().Reverse(true)
	}
	sty.Focused.Prompt = st.user
	// No colour on the cursor: it is a real one now, and the colour it
	// already has is the one the person chose for their terminal.
	sty.Cursor.Color = nil
	ta.SetStyles(sty)
	// enter sends; a newline is the deliberate one.
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter", "shift+enter", "ctrl+j"),
		key.WithHelp("alt+enter", "newline"),
	)
	ta.Focus()
	return &input{ta: ta, at: 0}
}

// enable gives the box back, or takes it away. A blurred box takes
// no keys and shows no cursor, which is exactly what a terminal
// waiting on an answer wants.
func (in *input) enable(on bool) {
	if on {
		in.ta.Focus()
		return
	}
	in.ta.Blur()
	in.sugg = nil
}

func (in *input) value() string { return in.ta.Value() }
func (in *input) empty() bool   { return strings.TrimSpace(in.ta.Value()) == "" }

func (in *input) setValue(s string) {
	in.ta.SetValue(s)
	in.ta.CursorEnd()
}

func (in *input) reset() {
	in.ta.Reset()
	in.at = len(in.history)
	in.draft = ""
	in.sugg = nil
}

// load replaces the history with one from somewhere else — a session
// file, at startup. The cursor sits past the end, so the first up
// arrow reaches the last thing the person said.
func (in *input) load(history []string) {
	in.history = append([]string(nil), history...)
	in.at = len(in.history)
	in.draft = ""
}

// remember pushes a sent line onto the history, skipping a repeat of
// the last one.
func (in *input) remember(s string) {
	if n := len(in.history); n == 0 || in.history[n-1] != s {
		in.history = append(in.history, s)
	}
	in.at = len(in.history)
	in.draft = ""
}

// height is how many lines the box is taking, which is what the rest
// of the screen has to be laid out around.
func (in *input) height() int { return in.ta.Height() }

// browse walks the history. It is only reachable when the box is
// empty or already showing a history entry, so it never eats an
// arrow key someone meant for the text they are writing.
func (in *input) browse(delta int) bool {
	if len(in.history) == 0 {
		return false
	}
	if in.at == len(in.history) {
		if delta > 0 {
			return false
		}
		in.draft = in.ta.Value()
	}
	next := clamp(in.at+delta, 0, len(in.history))
	if next == in.at {
		return false
	}
	in.at = next
	if in.at == len(in.history) {
		in.setValue(in.draft)
	} else {
		in.setValue(in.history[in.at])
	}
	return true
}

// browsing says whether the arrows belong to the history rather than
// to the text: an empty box, or one holding exactly a history entry.
func (in *input) browsing() bool {
	if in.empty() {
		return true
	}
	return in.at < len(in.history) && in.ta.Value() == in.history[in.at]
}

// --- completion ---------------------------------------------------------

// suggest recomputes the completion list from what is typed. It only
// fires on a single line that starts with a slash and has no space
// yet: once there are arguments, the command is chosen.
func (in *input) suggest() {
	in.sugg, in.sel = nil, 0
	v := in.ta.Value()
	if !strings.HasPrefix(v, "/") || strings.ContainsAny(v, " \n") {
		return
	}
	prefix := strings.ToLower(v[1:])
	for _, c := range in.cmds {
		if strings.HasPrefix(strings.ToLower(c.Name), prefix) {
			in.sugg = append(in.sugg, c)
		}
	}
	sort.SliceStable(in.sugg, func(i, j int) bool { return in.sugg[i].Name < in.sugg[j].Name })
}

func (in *input) completing() bool { return len(in.sugg) > 0 }

// exact says the box already holds the selected command in full, so
// enter should run it rather than complete it again.
func (in *input) exact() bool {
	return in.completing() && in.ta.Value() == "/"+in.sugg[in.sel].Name
}

func (in *input) move(delta int) {
	if !in.completing() {
		return
	}
	n := len(in.sugg)
	in.sel = ((in.sel+delta)%n + n) % n
}

// accept puts the selected command in the box. A command that takes
// arguments gets a trailing space, which is what you want next.
func (in *input) accept() bool {
	if !in.completing() {
		return false
	}
	c := in.sugg[in.sel]
	in.setValue("/" + c.Name + " ")
	in.sugg = nil
	return true
}

func (in *input) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	in.ta, cmd = in.ta.Update(msg)
	in.suggest()
	return cmd
}

// renderSuggestions draws the completion popup above the box: the
// commands that match, the chosen one as a solid block, and one line
// of keys — in a box of its own, drawn in the dim border so that the
// accent border under it still says where typing goes. The lines
// returned are the whole box, border and all, which is what the
// layout counts.
func (m *Model) renderSuggestions(w int) []string {
	in := m.in
	if !in.completing() {
		return nil
	}
	width := 0
	for _, c := range in.sugg {
		width = max(width, len(c.Name))
	}
	inner := max(w-4, 8) // the border and its padding
	var rows []string
	for i, c := range in.sugg {
		name := "/" + c.Name
		st := m.st.suggest
		if i == in.sel {
			st = m.st.suggestSel
		}
		pad := strings.Repeat(" ", width-len(c.Name))
		line := st.Render(name) + pad + "  " + m.st.dim.Render(c.Help)
		rows = append(rows, ansi.Truncate(line, inner, "…"))
		if len(rows) >= 8 {
			break
		}
	}
	rows = append(rows, ansi.Truncate(m.st.hint.Render(suggestHint), inner, "…"))
	box := m.st.box.Width(max(w-2, 1)).Render(strings.Join(rows, "\n"))
	return strings.Split(box, "\n")
}

// suggestHint is the popup's own line of keys. The status line's
// hints give way to the application's text when the window is
// narrow; these do not, because they are the keys for the thing the
// person is in the middle of.
const suggestHint = "↑↓ choose · ⏎ accept · esc dismiss · ⇧⏎ newline"
