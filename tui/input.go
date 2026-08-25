package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
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
	ta.MaxHeight = inputHeight
	ta.SetHeight(1)
	ta.FocusedStyle.CursorLine = st.text
	ta.FocusedStyle.Prompt = st.user
	ta.BlurredStyle.Prompt = st.dim
	ta.FocusedStyle.Placeholder = st.dim
	ta.BlurredStyle.Placeholder = st.dim
	// enter sends; a newline is the deliberate one.
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter", "shift+enter", "ctrl+j"),
		key.WithHelp("alt+enter", "newline"),
	)
	ta.Focus()
	return &input{ta: ta, at: 0}
}

func (in *input) value() string { return in.ta.Value() }
func (in *input) empty() bool   { return strings.TrimSpace(in.ta.Value()) == "" }

func (in *input) setValue(s string) {
	in.ta.SetValue(s)
	in.ta.CursorEnd()
	in.resize()
}

func (in *input) reset() {
	in.ta.Reset()
	in.at = len(in.history)
	in.draft = ""
	in.sugg = nil
	in.resize()
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

// resize grows the box with the text, up to inputHeight.
func (in *input) resize() {
	h := strings.Count(in.ta.Value(), "\n") + 1
	in.ta.SetHeight(clamp(h, 1, inputHeight))
}

func (in *input) height() int { return clamp(strings.Count(in.ta.Value(), "\n")+1, 1, inputHeight) }

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
	in.resize()
	in.suggest()
	return cmd
}

// renderSuggestions draws the completion list above the box.
func (m *Model) renderSuggestions(w int) []string {
	in := m.in
	if !in.completing() {
		return nil
	}
	width := 0
	for _, c := range in.sugg {
		width = max(width, len(c.Name))
	}
	var out []string
	for i, c := range in.sugg {
		name := "/" + c.Name + strings.Repeat(" ", width-len(c.Name))
		st := m.st.suggest
		mark := "  "
		if i == in.sel {
			st, mark = m.st.suggestSel, "▸ "
		}
		line := mark + st.Render(name) + "  " + m.st.dim.Render(c.Help)
		out = append(out, ansi.Truncate(line, w, "…"))
		if len(out) >= 8 {
			break
		}
	}
	return out
}
