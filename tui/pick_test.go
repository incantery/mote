package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/incantery/mote/agent"
)

// picked is where a test's OnPick puts what it was told.
type picked struct {
	got PickChoice
	n   int
}

// model is the picker the reference draws: three rows, a tick on the
// one in force, a dial, and two ways out.
func modelPick(p *picked) Pick {
	return Pick{
		Title: "Select model",
		Text:  "Switch models. Your pick becomes the default for new conversations.",
		Items: []PickItem{
			{Label: "gpt-5.6-luna", Detail: "via openai · effort none only"},
			{Label: "gpt-5", Detail: "via openai", Current: true},
			{Label: "claude-opus-5", Detail: "via anthropic"},
		},
		Current: 1,
		Dial:    &PickDial{Label: "effort", Options: []string{"none", "low", "medium", "high"}, Current: 2},
		Actions: []PickAction{
			{Key: "enter", Label: "set as default"},
			{Key: "s", Label: "this conversation only"},
		},
		OnPick: func(c PickChoice) tea.Cmd {
			p.got, p.n = c, p.n+1
			return nil
		},
	}
}

// picking builds a terminal with that card up.
func pickAt(t *testing.T, w, h int) (*Model, *picked) {
	t.Helper()
	p := &picked{}
	m := plain(t, w, h, Options{Name: "mote", Model: "gpt-5"})
	step(m, modelPick(p))
	if !m.picking() {
		t.Fatal("the card did not open")
	}
	return m, p
}

// deliver folds a message through Update the way the program does,
// running whatever comes back.
func deliver(m *Model, msg tea.Msg) {
	_, cmd := m.Update(msg)
	runCmd(m, cmd)
}

// cardRow is the screen row a row of the list landed on, which is what
// a click has to hit.
func cardRow(m *Model, item int) int {
	_, at := m.pickLines(m.width)
	for i, v := range at {
		if v == item {
			return m.vp.Height() + 1 + i
		}
	}
	return -1
}

// card is the card with the colour taken off, which is what a person
// reads.
func card(m *Model) string { return ansi.Strip(m.pickCard(m.width)) }

// rows is the selected row's label, which is what most of these tests
// are really asking about.
func selected(m *Model) string { return m.pick.p.Items[m.pick.item].Label }

// The card is the title, the sentence, the rows, the dial and the
// keys — at the two widths people have.
func TestPickCardGolden(t *testing.T) {
	for _, w := range []int{80, 120} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			m, _ := pickAt(t, w, 30)
			golden(t, fmt.Sprintf("pick-%d.txt", w), m.pickCard(w))
		})
	}
}

// Everything the reference card says is on it.
func TestPickCardSaysIt(t *testing.T) {
	m, _ := pickAt(t, 80, 30)
	got := card(m)
	for _, want := range []string{
		"Select model",
		"Switch models.",
		"1. gpt-5.6-luna", "via openai · effort none only",
		"› 2. gpt-5 ✓",
		"3. claude-opus-5",
		"● effort: medium", "←/→ to adjust",
		"Enter to set as default · s this conversation only · Esc to cancel",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the card is missing %q:\n%s", want, got)
		}
	}
}

// The card is above the box, not in the transcript: closing it leaves
// nothing behind.
func TestPickDrawsAboveTheInput(t *testing.T) {
	m, _ := pickAt(t, 80, 24)
	screen := ansi.Strip(view(m))
	lines := strings.Split(screen, "\n")
	title, box := -1, -1
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "Select model"):
			title = i
		case strings.HasPrefix(l, "╭───") && box < 0:
			box = i // the top of the input's box
		}
	}
	if title < 0 || box < 0 || title > box {
		t.Fatalf("the card is not between the transcript and the box (title %d, box %d):\n%s", title, box, screen)
	}
	if n := len(lines); n != 24 {
		t.Errorf("the frame is %d lines, not 24 — the card did not take them from the transcript", n)
	}
	before := len(m.entries)
	press(m, "esc")
	if len(m.entries) != before {
		t.Errorf("the picker left %d entries behind", len(m.entries)-before)
	}
}

// Up and down, j and k, and both of them wrap.
func TestPickMoves(t *testing.T) {
	for _, c := range []struct {
		keys []string
		want string
	}{
		{[]string{"down"}, "claude-opus-5"},
		{[]string{"up"}, "gpt-5.6-luna"},
		{[]string{"j"}, "claude-opus-5"},
		{[]string{"k"}, "gpt-5.6-luna"},
		{[]string{"down", "down"}, "gpt-5.6-luna"}, // wraps past the end
		{[]string{"up", "up"}, "claude-opus-5"},    // and past the start
		{[]string{"j", "j", "j"}, "gpt-5"},         // all the way round
	} {
		t.Run(strings.Join(c.keys, "+"), func(t *testing.T) {
			m, _ := pickAt(t, 80, 30)
			for _, k := range c.keys {
				press(m, k)
			}
			if got := selected(m); got != c.want {
				t.Errorf("after %v the selection is %q, want %q", c.keys, got, c.want)
			}
		})
	}
}

// A digit jumps; one past the end of the list does nothing.
func TestPickDigits(t *testing.T) {
	m, _ := pickAt(t, 80, 30)
	press(m, "1")
	if got := selected(m); got != "gpt-5.6-luna" {
		t.Errorf("1 selected %q", got)
	}
	press(m, "3")
	if got := selected(m); got != "claude-opus-5" {
		t.Errorf("3 selected %q", got)
	}
	press(m, "9")
	if got := selected(m); got != "claude-opus-5" {
		t.Errorf("9 moved the selection to %q; there is no ninth row", got)
	}
}

// The dial moves with the arrows and stops at both ends: high does not
// roll round to none.
func TestPickDial(t *testing.T) {
	m, _ := pickAt(t, 80, 30)
	press(m, "left")
	if !strings.Contains(card(m), "effort: low") {
		t.Errorf("left did not turn the dial:\n%s", card(m))
	}
	for range 5 {
		press(m, "right")
	}
	if !strings.Contains(card(m), "effort: high") {
		t.Errorf("right did not reach the end:\n%s", card(m))
	}
	for range 9 {
		press(m, "left")
	}
	if !strings.Contains(card(m), "effort: none") {
		t.Errorf("left did not reach the start:\n%s", card(m))
	}
	if strings.Contains(card(m), "effort: high") {
		t.Error("the dial wrapped")
	}
	// A picker with no dial has no dial line, and the arrows are quiet.
	m.pick.p.Dial = nil
	if strings.Contains(card(m), "←/→") {
		t.Errorf("a picker with no dial still offers one:\n%s", card(m))
	}
	press(m, "right")
}

// Enter runs the first action, and hands back where both axes stood.
func TestPickEnterRunsTheFirstAction(t *testing.T) {
	m, p := pickAt(t, 80, 30)
	press(m, "down")
	press(m, "right")
	press(m, "enter")
	if m.picking() {
		t.Fatal("enter left the card up")
	}
	want := PickChoice{Item: 2, Dial: 3, Action: "enter"}
	if p.got != want || p.n != 1 {
		t.Errorf("OnPick got %+v (%d times), want %+v once", p.got, p.n, want)
	}
}

// Any other action's key runs it.
func TestPickActionKey(t *testing.T) {
	m, p := pickAt(t, 80, 30)
	press(m, "s")
	if m.picking() {
		t.Fatal("the action left the card up")
	}
	if p.got.Action != "s" || p.got.Item != 1 || p.got.Cancelled {
		t.Errorf("OnPick got %+v", p.got)
	}
}

// A key nobody claimed is swallowed: the card stays, the box stays empty.
func TestPickSwallowsTheRest(t *testing.T) {
	m, p := pickAt(t, 80, 30)
	typeIn(m, "hello")
	if !m.picking() {
		t.Fatal("typing closed the card")
	}
	if m.in.value() != "" {
		t.Errorf("the box took %q", m.in.value())
	}
	if m.cursor(0, 0) != nil {
		t.Error("a blurred box shows no cursor")
	}
	if p.n != 0 {
		t.Errorf("OnPick was called %d times", p.n)
	}
	if !strings.Contains(ansi.Strip(m.statusLine()), "choosing") {
		t.Errorf("status line: %q", ansi.Strip(m.statusLine()))
	}
}

// Esc cancels, and cancelling is still an answer.
func TestPickEscCancels(t *testing.T) {
	m, p := pickAt(t, 80, 30)
	press(m, "esc")
	if m.picking() {
		t.Fatal("esc left the card up")
	}
	if !p.got.Cancelled || p.got.Action != "" || p.n != 1 {
		t.Errorf("OnPick got %+v (%d times)", p.got, p.n)
	}
	if !m.in.ta.Focused() {
		t.Error("the box did not come back")
	}
}

// Ctrl+c still quits.
func TestPickQuits(t *testing.T) {
	m, _ := pickAt(t, 80, 30)
	cmd, took := m.pickKey(kmsg("ctrl+c"))
	if !took || cmd == nil {
		t.Fatal("ctrl+c did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c returned %T", cmd())
	}
}

// A click on a row selects it; one somewhere else is left for the
// transcript to scroll with.
func TestPickClick(t *testing.T) {
	m, _ := pickAt(t, 80, 24)
	view(m) // the rows are where the frame put them
	step(m, tea.MouseReleaseMsg{X: 4, Y: cardRow(m, 2), Button: tea.MouseLeft})
	if got := selected(m); got != "claude-opus-5" {
		t.Errorf("the click selected %q", got)
	}
	if _, took := m.clickPick(tea.MouseReleaseMsg{X: 4, Y: 0, Button: tea.MouseLeft}); took {
		t.Error("a click in the transcript was taken by the card")
	}
}

// A card with more rows than room scrolls, and the selection is always
// on one of the rows that are drawn.
func TestPickScrolls(t *testing.T) {
	p := &picked{}
	pk := Pick{Title: "Pick one", Items: nil, OnPick: func(c PickChoice) tea.Cmd { p.got = c; return nil }}
	for i := range 40 {
		pk.Items = append(pk.Items, PickItem{Label: fmt.Sprintf("item-%02d", i+1)})
	}
	m := plain(t, 80, 20, Options{Name: "mote"})
	step(m, pk)
	if n := len(strings.Split(card(m), "\n")); n > m.pickRoom() {
		t.Fatalf("the card is %d lines, and there is room for %d", n, m.pickRoom())
	}
	for _, at := range []int{0, 20, 39, 12} {
		for m.pick.item != at {
			press(m, "down")
		}
		if !strings.Contains(card(m), "› "+fmt.Sprintf("%2d. item-%02d", at+1, at+1)) {
			t.Errorf("item %d is selected and not drawn:\n%s", at+1, card(m))
		}
	}
	// And the frame is still the height of the window.
	if n := len(strings.Split(ansi.Strip(view(m)), "\n")); n != 20 {
		t.Errorf("the frame is %d lines, not 20", n)
	}
}

// A second Pick replaces the first, and the first is told it lost.
func TestPickReplacesAPick(t *testing.T) {
	m, first := pickAt(t, 80, 30)
	second := &picked{}
	deliver(m, modelPick(second))
	if first.n != 1 || !first.got.Cancelled {
		t.Errorf("the first picker was told %+v (%d times)", first.got, first.n)
	}
	if !m.picking() {
		t.Fatal("the second card is not up")
	}
	press(m, "enter")
	if second.n != 1 || second.got.Cancelled {
		t.Errorf("the second picker was told %+v (%d times)", second.got, second.n)
	}
}

// A Pick over an open question answers it no, the way anything else
// that ends a question does.
func TestPickReplacesAnAsk(t *testing.T) {
	a := &answers{}
	m := asked(t, a)
	if !m.asking() {
		t.Fatal("the question is not open")
	}
	_, cmd := m.Update(modelPick(&picked{}))
	runCmd(m, cmd)
	if m.asking() {
		t.Error("the question is still open")
	}
	if !m.picking() {
		t.Error("the card is not up")
	}
	waitFor(t, func() bool { return len(a.all()) == 1 })
	if got := a.all()[0]; got != "call_2="+agent.No {
		t.Errorf("the agent was told %q", got)
	}
	// And the box does not come back until the card goes.
	if m.in.ta.Focused() {
		t.Error("the box came back while the card is up")
	}
	press(m, "esc")
	if !m.in.ta.Focused() {
		t.Error("the box did not come back")
	}
}

// A reply still arriving keeps arriving behind the card.
func TestPickLetsTheReplyThrough(t *testing.T) {
	m, _ := pickAt(t, 80, 30)
	step(m, events(agent.Delta("still writing"))...)
	if !strings.Contains(ansi.Strip(m.transcript()), "still writing") {
		t.Errorf("the reply stopped behind the card:\n%s", ansi.Strip(m.transcript()))
	}
	if !strings.Contains(ansi.Strip(view(m)), "still writing") {
		t.Error("the reply is not on the screen")
	}
	if !m.picking() {
		t.Error("the reply closed the card")
	}
}

// A picker with nothing to choose from is not a card.
func TestPickNeedsRows(t *testing.T) {
	m := plain(t, 80, 24, Options{Name: "mote"})
	step(m, Pick{Title: "Nothing"})
	if m.picking() {
		t.Error("an empty picker opened a card")
	}
	if !m.in.ta.Focused() {
		t.Error("an empty picker took the box away")
	}
}

// A picker with no actions still chooses on enter.
func TestPickWithoutActions(t *testing.T) {
	p := &picked{}
	m := plain(t, 80, 24, Options{Name: "mote"})
	step(m, Pick{
		Items:  []PickItem{{Label: "one"}, {Label: "two"}},
		OnPick: func(c PickChoice) tea.Cmd { p.got, p.n = c, p.n+1; return nil },
	})
	if !strings.Contains(card(m), "Enter to choose · Esc to cancel") {
		t.Errorf("the keys line reads:\n%s", card(m))
	}
	press(m, "down")
	press(m, "enter")
	if p.n != 1 || p.got.Item != 1 || p.got.Action != "" {
		t.Errorf("OnPick got %+v (%d times)", p.got, p.n)
	}
}

// A picker nobody is listening to still opens and still closes.
func TestPickWithoutOnPick(t *testing.T) {
	m := plain(t, 80, 24, Options{Name: "mote"})
	step(m, Pick{Items: []PickItem{{Label: "one"}}})
	if !m.picking() {
		t.Fatal("the card did not open")
	}
	press(m, "enter")
	if m.picking() {
		t.Error("enter left the card up")
	}
}

// OnPick's command is run, and what it returns lands in the terminal.
func TestPickRunsTheCommand(t *testing.T) {
	m := plain(t, 80, 24, Options{Name: "mote", Model: "gpt-5"})
	step(m, Pick{
		Items:   []PickItem{{Label: "claude-opus-5"}},
		Actions: []PickAction{{Key: "enter", Label: "use it"}},
		OnPick: func(c PickChoice) tea.Cmd {
			if c.Cancelled {
				return nil
			}
			return tea.Batch(SetModel("claude-opus-5"), Note("picked claude-opus-5"))
		},
	})
	press(m, "enter")
	if !strings.Contains(ansi.Strip(m.statusLine()), "claude-opus-5") {
		t.Errorf("the status line still says %q", ansi.Strip(m.statusLine()))
	}
	if !strings.Contains(ansi.Strip(m.transcript()), "picked claude-opus-5") {
		t.Errorf("the note is not in the transcript:\n%s", ansi.Strip(m.transcript()))
	}
}

// The model on the status line is settable.
func TestSetModel(t *testing.T) {
	m := plain(t, 80, 24, Options{Name: "mote", Model: "gpt-5", Conversation: "demo-1"})
	if !strings.Contains(ansi.Strip(m.statusLine()), "gpt-5") {
		t.Fatalf("status line: %q", ansi.Strip(m.statusLine()))
	}
	runCmd(m, SetModel("claude-opus-5"))
	line := ansi.Strip(m.statusLine())
	if !strings.Contains(line, "claude-opus-5") || strings.Contains(line, "gpt-5 ·") {
		t.Errorf("status line: %q", line)
	}
}

// The rail can start hidden, and ctrl+t still brings it back.
func TestSideClosed(t *testing.T) {
	side := func() []SideItem { return []SideItem{{ID: "a1", Title: "a task", State: Working}} }
	m := plain(t, 120, 24, Options{Name: "mote", Side: side, SideTitle: "fleet", SideClosed: true})
	if strings.Contains(ansi.Strip(view(m)), "fleet") {
		t.Errorf("the rail is showing:\n%s", ansi.Strip(view(m)))
	}
	press(m, "ctrl+t")
	if !strings.Contains(ansi.Strip(view(m)), "fleet") {
		t.Error("ctrl+t did not show the rail")
	}
}
