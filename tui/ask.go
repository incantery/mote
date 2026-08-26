package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/incantery/mote/agent"
)

// An ask is the one thing in this terminal that stops it. Everything
// else the agent says is something to look at; this is something to
// answer, and until it is answered there is nothing else to type. So
// the box is disabled, three keys mean three things, and the card
// says what "always" would cost.

// choices are the three answers, in the order a person reads them and
// with the keys they are bound to.
var choices = []struct {
	key    string
	choice string
	help   string
}{
	{"y", agent.Yes, "run it, and ask again next time"},
	{"n", agent.No, "do not run it"},
	{"a", agent.Always, "run it, and stop asking about ones like it"},
}

// askSpan is where one choice sits on the choices line, so a click
// lands on the same thing the key does.
type askSpan struct {
	from, to int // columns, [from, to)
	choice   string
}

// openAsk is the ask waiting for an answer, if there is one.
func (m *Model) openAsk() *entry {
	if m.ask < 0 || m.ask >= len(m.entries) {
		return nil
	}
	e := m.entries[m.ask]
	if e.kind != entryAsk || e.answer != "" {
		return nil
	}
	return e
}

// asking says whether the terminal is waiting on a person. While it
// is, the input is blurred and almost every key does nothing.
func (m *Model) asking() bool { return m.openAsk() != nil }

// raise puts a question in the transcript and stops.
func (m *Model) raise(ev agent.Event) {
	// Text before the question is finished text; close it off.
	m.commit()
	// Two at once is not a shape this terminal has, but if it ever
	// happens the older one is not left looking answerable.
	m.cancelAsk(true)
	e := &entry{kind: entryAsk, id: ev.ID, name: ev.Name, args: ev.Args, text: ev.Text}
	if ev.Result != "" {
		// A recorded ask, replayed: it was answered once and does not
		// get asked again.
		e.answer = ev.Result
		m.add(e)
		return
	}
	m.ask = len(m.entries)
	m.add(e)
	m.statusText = ""
	if _, ok := m.agent.(agent.Answerer); !ok {
		// Nobody to tell. Say so rather than blocking the terminal on
		// an answer that can never be delivered.
		e.answer, e.cancelled = agent.No, true
		e.text = ev.Text
		m.ask = -1
		return
	}
	m.in.enable(false)
	m.follow = true
}

// answer is the person's word. It closes the card, gives the box
// back, and tells the agent off the UI goroutine.
func (m *Model) answer(choice string) tea.Cmd {
	e := m.openAsk()
	if e == nil {
		return nil
	}
	e.answer = choice
	m.touch(m.ask)
	m.ask = -1
	m.in.enable(true)
	m.refresh()

	a, ok := m.agent.(agent.Answerer)
	if !ok {
		return nil
	}
	id := e.id
	return func() tea.Msg {
		if err := a.Answer(context.Background(), id, choice); err != nil {
			return failMsg{"answer: " + err.Error()}
		}
		return nil
	}
}

// cancelAsk closes an open ask because the exchange ended.
//
// tell says whether the agent hears about it. A live turn that ended
// with a question open has to be told no — a harness still parked on
// it has to be let go, and nothing is coming. A transcript being
// replayed from a file has nobody parked on anything, and telling the
// agent about a call id out of last week's conversation is how a
// fresh call with the same id gets answered before it is asked.
func (m *Model) cancelAsk(tell bool) {
	e := m.openAsk()
	if e == nil {
		return
	}
	e.answer, e.cancelled = agent.No, true
	m.touch(m.ask)
	m.ask = -1
	m.in.enable(true)
	if !tell {
		return
	}
	if a, ok := m.agent.(agent.Answerer); ok {
		// Off the UI goroutine: nothing promises Answer is quick.
		id := e.id
		go func() { _ = a.Answer(context.Background(), id, agent.No) }()
	}
}

// touch invalidates an entry that changed, and drops the cached
// transcript if the change was above the fold.
func (m *Model) touch(i int) {
	if i < 0 || i >= len(m.entries) {
		return
	}
	m.entries[i].invalidate()
	if i < m.stableN {
		m.resetStable()
	}
}

// askKey handles a key while a question is open. It reports whether
// it took it: what it does not take is the small set that is still
// worth having — quitting, and scrolling to read what you are being
// asked about.
func (m *Model) askKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch key := strings.ToLower(msg.String()); key {
	case "ctrl+c", "ctrl+d":
		return tea.Quit, true
	case "esc":
		// Esc is no: the answer, not a way out of answering.
		return m.answer(agent.No), true
	case "up", "down", "pgup", "pgdown", "ctrl+l", "home", "end":
		return nil, false // reading is allowed while deciding
	}
	for _, c := range choices {
		if strings.ToLower(msg.String()) == c.key {
			return m.answer(c.choice), true
		}
	}
	// Everything else is swallowed: the box is not taking dictation.
	return nil, true
}

// clickAsk answers a question with the mouse. row and col are the
// screen's; the transcript's own line is the viewport's offset plus
// the row, which is why the row of the choices line is recorded when
// the transcript is built.
func (m *Model) clickAsk(msg tea.MouseMsg) (tea.Cmd, bool) {
	if _, ok := msg.(tea.MouseReleaseMsg); !ok {
		return nil, false
	}
	if m.askRow < 0 || !m.asking() {
		return nil, false
	}
	mouse := msg.Mouse()
	if mouse.Button != tea.MouseLeft {
		return nil, false
	}
	if mouse.Y < 0 || mouse.Y >= m.vp.Height() {
		return nil, false
	}
	if m.vp.YOffset()+mouse.Y != m.askRow {
		return nil, false
	}
	for _, s := range m.askSpans {
		if mouse.X >= s.from && mouse.X < s.to {
			return m.answer(s.choice), true
		}
	}
	return nil, false
}

// --- drawing ------------------------------------------------------------

// askIndent is the card's gutter plus the indent its body sits at,
// which is also where the first choice starts.
const askIndent = 4

// spans is where the three choices land on the choices line. It is a
// pure function of nothing, because the labels are fixed — which is
// what makes the mouse and the keys agree without either of them
// consulting the other.
func askSpans() []askSpan {
	out := make([]askSpan, 0, len(choices))
	col := askIndent
	for _, c := range choices {
		label := "[" + c.key + "] " + c.choice
		out = append(out, askSpan{from: col, to: col + len(label), choice: c.choice})
		col += len(label) + 3
	}
	return out
}

func (m *Model) renderAsk(e *entry, w int) string {
	inner := max(w-2, 20)
	gutter, gst := "  ", m.st.dim
	if e.answer == "" {
		gutter, gst = "┃ ", m.st.status
	}

	mark, mst := "?", m.st.status
	switch {
	case e.cancelled:
		mark, mst = "✗", m.st.errline
	case e.answer == agent.No:
		mark, mst = "✗", m.st.errline
	case e.answer != "":
		mark, mst = "✓", m.st.dim
	}

	head := mst.Render(mark) + " " + m.st.tool.Render(e.name)
	used := 1 + 1 + lipgloss.Width(e.name)
	right := ""
	switch {
	case e.cancelled:
		right = "cancelled — the turn ended first"
	case e.answer != "":
		right = "you said " + e.answer
	}
	summary := summarizeArgs(e.args, max(8, inner-used-lipgloss.Width(right)-3))
	if summary != "" {
		head += "  " + m.st.toolArgs.Render(summary)
		used += 2 + lipgloss.Width(summary)
	}
	if right != "" {
		pad := max(1, inner-used-lipgloss.Width(right))
		head += strings.Repeat(" ", pad) + m.st.dim.Render(right)
	}

	lines := []string{head}
	if e.text != "" {
		for _, l := range strings.Split(ansi.Wrap(e.text, max(inner-2, 8), " -/"), "\n") {
			lines = append(lines, m.st.text.Render("  "+l))
		}
	}
	if e.answer == "" {
		lines = append(lines, m.askChoices())
	}
	for i, l := range lines {
		lines[i] = gst.Render(gutter) + l
	}
	return strings.Join(lines, "\n")
}

// askChoices is the line the keys and the mouse both point at. It is
// built at the columns askSpans promises, so the two cannot drift.
func (m *Model) askChoices() string {
	var b strings.Builder
	b.WriteString("  ")
	for i, c := range choices {
		if i > 0 {
			b.WriteString("   ")
		}
		b.WriteString(m.st.key.Render("[" + c.key + "]"))
		b.WriteString(" ")
		b.WriteString(m.st.text.Render(c.choice))
	}
	return b.String()
}

// askHint is what the status line says while a question is open.
func askHint() string {
	var parts []string
	for _, c := range choices {
		parts = append(parts, c.key+" "+c.choice)
	}
	return strings.Join(parts, " · ") + " · or click"
}
