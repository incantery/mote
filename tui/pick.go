package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// A picker is the other thing that stops the terminal, and it stops it
// for the opposite reason to an ask. An ask is the agent's question
// and it lives in the transcript, because what was asked and what was
// answered is part of the conversation. A picker is the application's,
// and it is not: choosing a model is not something the agent said, so
// the card sits above the box, takes the keyboard while it is up, and
// leaves nothing behind when it closes. What it leaves behind, if the
// application wants one, is a line the application writes itself.

// Pick puts a list on the screen and takes the keyboard until the
// person has chosen from it. An application sends one as a message —
// a Handle that returns Choose(Pick{…}), or any tea.Cmd of its own
// that yields one.
//
// The rows are Items; the person moves with ↑/↓ or j/k, jumps with
// 1–9, and chooses with Enter or with any Action's Key. Esc always
// cancels, and cancelling calls OnPick too — an application that put a
// question up is told how it ended, whichever way it ended.
//
// A Pick with no Items is not a card and does not open one.
type Pick struct {
	// Title heads the card, in the accent colour. "Select model".
	Title string
	// Text is a line or two under the title saying what choosing
	// does. Optional; it wraps, and it is the first thing to give way
	// when the window is short.
	Text string
	// Items are the rows, in the order they are read.
	Items []PickItem
	// Current is the row selected when the card opens.
	Current int
	// Dial is an optional second axis — an effort, a verbosity —
	// moved with ←/→. Nil for a list with one axis.
	Dial *PickDial
	// Actions are what Enter and the letter keys do. Enter runs the
	// first; every other one is run by its Key. Esc is not among them:
	// cancelling is not an action, it is the absence of one. Empty
	// means Enter chooses and the choice carries no action name.
	//
	// A Key the picker already owns — an arrow, j, k, a digit, enter,
	// esc — never reaches the action: the keys that move the selection
	// are the same on every picker, which is what makes them keys a
	// person keeps rather than reads.
	Actions []PickAction
	// OnPick is told what was chosen. It is called off the UI
	// goroutine and the command it returns is run there too, so it may
	// take as long as it likes. Nil is allowed: a picker nobody is
	// listening to still draws, and still closes.
	OnPick func(choice PickChoice) tea.Cmd
}

// PickItem is one row: what it is called, what is worth knowing about
// it, and whether it is the one in force now.
type PickItem struct {
	Label  string
	Detail string
	// Current draws a tick. It is what is true now, which is not the
	// same as what is selected — the selection moves, this does not.
	Current bool
}

// PickDial is the second axis: a word from a short list, moved with
// ←/→ without leaving the row you are on.
type PickDial struct {
	Label   string   // "effort"
	Options []string // "none", "low", "medium", "high"
	Current int
}

// PickAction is one way out of the card. Key is what runs it — "enter"
// for the first one by convention, a letter for the rest.
type PickAction struct {
	Key   string
	Label string
}

// PickChoice is what the person did. Item and Dial are indexes into
// what the Pick was built with, so the application looks the answer up
// in its own list rather than parsing a label back.
type PickChoice struct {
	Item      int
	Dial      int
	Action    string
	Cancelled bool
}

// Choose puts a picker on the screen.
func Choose(p Pick) tea.Cmd { return func() tea.Msg { return p } }

// picker is a Pick with the person's place in it.
type picker struct {
	p    Pick
	item int // the row the › is on
	dial int
	top  int // the first row drawn, when there is not room for all of them
}

func (m *Model) picking() bool { return m.pick != nil }

// openPick puts a card up, replacing whatever was there. Another
// picker is cancelled — its application is told, because an
// application that asked a question is owed an answer either way —
// and an open question is answered no, the way anything else that
// ends a question answers it.
func (m *Model) openPick(p Pick) tea.Cmd {
	if len(p.Items) == 0 {
		return nil
	}
	var cmd tea.Cmd
	if m.pick != nil {
		cmd = m.closePick(m.pick.choice(true, ""))
	}
	m.cancelAsk(true)
	pk := &picker{p: p, item: clamp(p.Current, 0, len(p.Items)-1)}
	if p.Dial != nil && len(p.Dial.Options) > 0 {
		pk.dial = clamp(p.Dial.Current, 0, len(p.Dial.Options)-1)
	}
	m.pick = pk
	m.in.enable(false)
	m.layout()
	m.refresh()
	return cmd
}

// closePick takes the card down and tells the application. The box
// comes back unless a question is still waiting on it: a picker that
// opened over an ask does not answer it on its way out.
func (m *Model) closePick(c PickChoice) tea.Cmd {
	pk := m.pick
	if pk == nil {
		return nil
	}
	m.pick = nil
	m.in.enable(!m.asking())
	m.layout()
	m.refresh()
	on := pk.p.OnPick
	if on == nil {
		return nil
	}
	// Off the UI goroutine, and the command it hands back with it:
	// nothing promises an application's OnPick is quick.
	return func() tea.Msg {
		if cmd := on(c); cmd != nil {
			return cmd()
		}
		return nil
	}
}

func (pk *picker) choice(cancelled bool, action string) PickChoice {
	return PickChoice{Item: pk.item, Dial: pk.dial, Action: action, Cancelled: cancelled}
}

// move walks the rows and wraps, the way the completion list does:
// down from the last row is the first one.
func (pk *picker) move(d int) {
	n := len(pk.p.Items)
	pk.item = ((pk.item+d)%n + n) % n
}

// turn moves the dial, and does not wrap: a dial has ends, and "high"
// rolling round to "none" is how somebody ends up on the wrong one.
func (pk *picker) turn(d int) {
	if pk.p.Dial == nil || len(pk.p.Dial.Options) == 0 {
		return
	}
	pk.dial = clamp(pk.dial+d, 0, len(pk.p.Dial.Options)-1)
}

// firstAction is what Enter runs.
func (pk *picker) firstAction() string {
	if len(pk.p.Actions) == 0 {
		return ""
	}
	return pk.p.Actions[0].Key
}

// pickKey handles a key while a card is up. It reports whether it took
// it: what it does not take is scrolling, because reading what the
// choice is about is part of making it.
func (m *Model) pickKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	pk := m.pick
	key := strings.ToLower(msg.String())
	switch key {
	case "ctrl+c", "ctrl+d":
		return tea.Quit, true
	case "pgup", "pgdown", "ctrl+l":
		return nil, false // reading behind the card is allowed while choosing
	case "esc":
		return m.closePick(pk.choice(true, "")), true
	case "up", "k":
		pk.move(-1)
		return nil, true
	case "down", "j":
		pk.move(1)
		return nil, true
	case "left":
		pk.turn(-1)
		return nil, true
	case "right":
		pk.turn(1)
		return nil, true
	case "enter":
		return m.closePick(pk.choice(false, pk.firstAction())), true
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		if i := int(key[0] - '1'); i < len(pk.p.Items) {
			pk.item = i
			return nil, true
		}
	}
	for _, a := range pk.p.Actions {
		if a.Key != "" && strings.EqualFold(a.Key, key) {
			return m.closePick(pk.choice(false, a.Key)), true
		}
	}
	// Everything else is swallowed: the box is not taking dictation.
	return nil, true
}

// clickPick moves the selection to the row under the pointer. The card
// is drawn at a known place — under the transcript, above the rule —
// so the row is arithmetic rather than something to record.
func (m *Model) clickPick(msg tea.MouseMsg) (tea.Cmd, bool) {
	if m.pick == nil {
		return nil, false
	}
	if _, ok := msg.(tea.MouseReleaseMsg); !ok {
		return nil, false
	}
	mouse := msg.Mouse()
	if mouse.Button != tea.MouseLeft {
		return nil, false
	}
	_, rows := m.pickLines(m.width)
	i := mouse.Y - m.vp.Height() - 1 // the blank line the card opens with
	if i < 0 || i >= len(rows) || rows[i] < 0 {
		return nil, false
	}
	m.pick.item = rows[i]
	return nil, true
}

// --- drawing ------------------------------------------------------------

// pickRoom is the tallest the card may be: what is left of the window
// once the box, the rule, the status line and a few lines of the
// conversation have had theirs. A card is a question about something,
// and covering the something to ask about it is no good.
func (m *Model) pickRoom() int {
	const keep = 3 // the transcript never goes below this
	base := 1 /*the blank line above the card*/ + 1 /*rule*/ + m.in.height() + 1 /*status*/ + keep
	return max(m.height-base, 3)
}

// pickHeight is how many lines the card costs the rest of the screen,
// the blank line above it included.
func (m *Model) pickHeight() int {
	if m.pick == nil {
		return 0
	}
	lines, _ := m.pickLines(m.width)
	return 1 + len(lines)
}

// pickCard is the card as one string, which is what a golden file
// holds and what a person sees above the box.
func (m *Model) pickCard(w int) string {
	lines, _ := m.pickLines(w)
	return strings.Join(lines, "\n")
}

// pickLines draws the card and says, for each line, which row of the
// list it is — -1 for the lines that are not rows. The second half is
// what the mouse reads.
func (m *Model) pickLines(w int) ([]string, []int) {
	pk := m.pick
	if pk == nil {
		return nil, nil
	}
	p := pk.p
	w = max(w, 20)

	// Above the rows: the title, the sentence, and a blank line.
	var head []string
	if p.Title != "" {
		head = append(head, ansi.Truncate(m.st.accent.Render(oneLine(p.Title)), w, "…"))
	}
	if p.Text != "" {
		for _, l := range strings.Split(ansi.Wrap(p.Text, w, " -/"), "\n") {
			head = append(head, m.st.dim.Render(l))
		}
	}
	if len(head) > 0 {
		head = append(head, "")
	}

	// Below them: the dial, if there is one, and the keys.
	var foot []string
	if p.Dial != nil && len(p.Dial.Options) > 0 {
		foot = append(foot, ansi.Truncate(m.pickDial(pk), w, "…"))
	}
	foot = append(foot, ansi.Truncate(m.st.dim.Render(pickHints(p)), w, "…"))

	// The rows are the point of the card, so the sentence above them
	// is what gives way when the window is too short for both.
	room := m.pickRoom()
	if n := room - len(foot) - 1; len(head) > max(n, 0) {
		head = head[:max(n, 0)]
	}
	rows := max(room-len(head)-len(foot), 1)

	lines := append([]string(nil), head...)
	at := make([]int, len(head))
	for i := range at {
		at[i] = -1
	}
	for _, row := range m.pickRows(pk, rows, w) {
		lines = append(lines, row.text)
		at = append(at, row.item)
	}
	for _, l := range foot {
		lines = append(lines, l)
		at = append(at, -1)
	}
	if len(lines) > room {
		lines, at = lines[:room], at[:room]
	}
	return lines, at
}

// pickRow is one drawn row and the item it stands for.
type pickRow struct {
	text string
	item int
}

// pickRows draws at most n rows, scrolled so the selection is on one
// of them. Nothing else moves: the window into the list follows the
// selection and stays where it is put.
func (m *Model) pickRows(pk *picker, n, w int) []pickRow {
	items := pk.p.Items
	vis := min(n, len(items))
	top := clamp(pk.top, 0, max(len(items)-vis, 0))
	if pk.item < top {
		top = pk.item
	}
	if pk.item >= top+vis {
		top = pk.item - vis + 1
	}
	pk.top = top

	// The details line up, so the numbers and the ticks are counted
	// into the width of the widest label rather than pushing it about.
	numw := len(strconv.Itoa(len(items)))
	labelw := 0
	for i, it := range items {
		labelw = max(labelw, lipgloss.Width(pickLabel(numw, i, it)))
	}

	out := make([]pickRow, 0, vis)
	for i := top; i < top+vis; i++ {
		it := items[i]
		mark, lst := "  ", m.st.text
		if i == pk.item {
			mark, lst = "› ", m.st.focused
		}
		plain := pickLabel(numw, i, it)
		line := m.st.focused.Render(mark) + lst.Render(fmt.Sprintf("%*d. ", numw, i+1)+it.Label)
		if it.Current {
			line += " " + m.st.needs.Render("✓")
		}
		if it.Detail != "" {
			pad := max(labelw-lipgloss.Width(plain)+2, 1)
			line += strings.Repeat(" ", pad) + m.st.dim.Render(oneLine(it.Detail))
		}
		out = append(out, pickRow{ansi.Truncate(line, w, "…"), i})
	}
	return out
}

// pickLabel is a row's left-hand side with no colour on it: what the
// details have to clear.
func pickLabel(numw, i int, it PickItem) string {
	s := fmt.Sprintf("%*d. ", numw, i+1) + it.Label
	if it.Current {
		s += " ✓"
	}
	return s
}

// pickDial is the second axis on its own line, with the keys that move
// it beside it — the only place they are said, because a dial nobody
// knows is there is a dial nobody turns.
func (m *Model) pickDial(pk *picker) string {
	d := pk.p.Dial
	return m.st.accent.Render("●") + " " +
		m.st.text.Render(d.Label+": "+d.Options[pk.dial]) +
		m.st.dim.Render("   ←/→ to adjust")
}

// pickHints is the last line: what Enter does, what every other key
// does, and that Esc is the way out.
func pickHints(p Pick) string {
	var parts []string
	for i, a := range p.Actions {
		label := a.Label
		if label == "" {
			label = a.Key
		}
		if i == 0 {
			parts = append(parts, "Enter to "+label)
			continue
		}
		parts = append(parts, a.Key+" "+label)
	}
	if len(parts) == 0 {
		parts = append(parts, "Enter to choose")
	}
	return strings.Join(append(parts, "Esc to cancel"), " · ")
}
