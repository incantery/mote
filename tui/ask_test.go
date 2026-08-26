package tui

import (
	"context"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/incantery/mote/agent"
)

// answers records what the terminal told the agent, which is the half
// of an ask that nothing on screen can show.
type answers struct {
	agent.Fake

	mu   sync.Mutex
	said []string
}

func (a *answers) Answer(ctx context.Context, id, choice string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.said = append(a.said, id+"="+choice)
	return nil
}

func (a *answers) all() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.said...)
}

// mute is an agent that cannot be answered — the case the terminal
// must not stop on. It is written out rather than embedding the Fake,
// because the Fake can be answered.
type mute struct{}

func (mute) Send(context.Context, string, string) (<-chan agent.Event, error) {
	ch := make(chan agent.Event)
	close(ch)
	return ch, nil
}

// asked builds a terminal with a question open on it.
func asked(t *testing.T, a agent.Agent) *Model {
	t.Helper()
	pal := DefaultPalette()
	pal.Markdown = "ascii"
	m := New(a, Options{Name: "mote", Palette: &pal})
	step(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	step(m, events(
		agent.Delta("I need to write a note.\n"),
		agent.Asking("call_2", "write",
			`{"path":"/tmp/scratch/notes.md","content":"hi\n"}`,
			"outside ~/vera and not a project — the profile says ask"),
	)...)
	return m
}

// run drives a command the way the program would, folding whatever it
// returns back through Update.
func runCmd(m *Model, cmd tea.Cmd) {
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			return
		}
		_, cmd = m.Update(msg)
	}
}

func press(m *Model, key string) {
	_, cmd := m.Update(kmsg(key))
	runCmd(m, cmd)
}

// The card is the question, the arguments, and the three ways out.
func TestAskCardShowsTheQuestion(t *testing.T) {
	m := asked(t, &answers{})
	card := ansi.Strip(m.renderAsk(m.entries[1], 100))
	for _, want := range []string{
		"write", "path=/tmp/scratch/notes.md",
		"the profile says ask",
		"[y] yes", "[n] no", "[a] always",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("card is missing %q:\n%s", want, card)
		}
	}
}

// While a question is open the box is not taking dictation, and the
// cursor is gone with it.
func TestAskDisablesTheInput(t *testing.T) {
	m := asked(t, &answers{})
	if !m.asking() {
		t.Fatal("the terminal should be waiting")
	}
	typeIn(m, "hello")
	if m.in.value() != "" {
		t.Fatalf("the box took %q", m.in.value())
	}
	step(m, kmsg("enter"))
	if len(m.entries) != 2 {
		t.Fatalf("enter sent something: %v", kinds(m))
	}
	if m.cursor(0) != nil {
		t.Fatal("a blurred box shows no cursor")
	}
	if !strings.Contains(ansi.Strip(m.statusLine()), "waiting for you") {
		t.Fatalf("status line: %q", ansi.Strip(m.statusLine()))
	}
}

// Each key is its answer, and the agent is told.
func TestAskKeys(t *testing.T) {
	for _, c := range []struct{ key, want string }{
		{"y", agent.Yes},
		{"n", agent.No},
		{"a", agent.Always},
		{"esc", agent.No},
	} {
		t.Run(c.key, func(t *testing.T) {
			a := &answers{}
			m := asked(t, a)
			press(m, c.key)
			if m.asking() {
				t.Fatal("still asking")
			}
			if got := m.entries[1].answer; got != c.want {
				t.Fatalf("card says %q, want %q", got, c.want)
			}
			if got := a.all(); len(got) != 1 || got[0] != "call_2="+c.want {
				t.Fatalf("the agent was told %v", got)
			}
			if !m.in.ta.Focused() {
				t.Fatal("the box should be back")
			}
			card := ansi.Strip(m.renderAsk(m.entries[1], 100))
			if !strings.Contains(card, "you said "+c.want) {
				t.Fatalf("card: %s", card)
			}
			if strings.Contains(card, "[y] yes") {
				t.Fatal("an answered card keeps no keys")
			}
			// And a second key changes nothing.
			press(m, "y")
			if got := a.all(); len(got) != 1 {
				t.Fatalf("answered twice: %v", got)
			}
		})
	}
}

// The mouse lands on the same three things the keys do.
func TestAskMouse(t *testing.T) {
	a := &answers{}
	m := asked(t, a)
	m.refresh()
	if m.askRow < 0 || len(m.askSpans) != 3 {
		t.Fatalf("row %d spans %v", m.askRow, m.askSpans)
	}
	// The line the mouse is over is the one the transcript drew.
	line := strings.Split(ansi.Strip(m.transcript()), "\n")[m.askRow]
	for _, s := range m.askSpans {
		if !strings.Contains(line, s.choice) {
			t.Fatalf("line %q has no %q", line, s.choice)
		}
		if got := lipglossWidth(line[:strings.Index(line, "["+string(s.choice[0])+"]")]); got != s.from {
			t.Errorf("%s starts at %d, the span says %d (%q)", s.choice, got, s.from, line)
		}
	}

	y := m.askRow - m.vp.YOffset()
	always := m.askSpans[2]
	_, cmd := m.Update(tea.MouseReleaseMsg{X: always.from + 1, Y: y, Button: tea.MouseLeft})
	runCmd(m, cmd)
	if got := a.all(); len(got) != 1 || got[0] != "call_2="+agent.Always {
		t.Fatalf("%v", got)
	}
}

// A click that is not on a choice is a click on the transcript.
func TestAskMouseElsewhere(t *testing.T) {
	a := &answers{}
	m := asked(t, a)
	m.refresh()
	y := m.askRow - m.vp.YOffset()
	step(m, tea.MouseReleaseMsg{X: 0, Y: y, Button: tea.MouseLeft})
	step(m, tea.MouseReleaseMsg{X: m.askSpans[0].from, Y: y + 1, Button: tea.MouseLeft})
	if len(a.all()) != 0 {
		t.Fatalf("%v", a.all())
	}
	if !m.asking() {
		t.Fatal("still asking")
	}
}

// A done while a question is open cancels it: the card says so, the
// box comes back, and the agent is told no so nothing stays parked.
func TestDoneCancelsAnOpenAsk(t *testing.T) {
	a := &answers{}
	m := asked(t, a)
	step(m, events(agent.Done())...)
	if m.asking() {
		t.Fatal("done should have closed the question")
	}
	if !m.entries[1].cancelled {
		t.Fatal("the card should say it was cancelled")
	}
	if !m.in.ta.Focused() {
		t.Fatal("the box should be back")
	}
	card := ansi.Strip(m.renderAsk(m.entries[1], 100))
	if !strings.Contains(card, "cancelled") {
		t.Fatalf("card: %s", card)
	}
	// The agent is told off the UI goroutine, so wait for it.
	waitFor(t, func() bool { return len(a.all()) == 1 })
	if got := a.all(); got[0] != "call_2="+agent.No {
		t.Fatalf("%v", got)
	}
}

// An agent that takes no answers must not stop the terminal.
func TestAskWithNobodyToAnswer(t *testing.T) {
	m := asked(t, mute{})
	if m.asking() {
		t.Fatal("a question nobody can answer does not stop the box")
	}
	if !m.in.ta.Focused() {
		t.Fatal("the box should still work")
	}
	if !m.entries[1].cancelled {
		t.Fatal("and the card should say the question went nowhere")
	}
}

// Reading is allowed while deciding.
func TestScrollingWhileAsking(t *testing.T) {
	m := asked(t, &answers{})
	for range 40 {
		step(m, events(agent.Notice("something happened"))...)
	}
	m.refresh()
	before := m.vp.YOffset()
	step(m, kmsg("pgup"))
	if m.vp.YOffset() >= before {
		t.Fatalf("pgup did not scroll: %d then %d", before, m.vp.YOffset())
	}
	if !m.asking() {
		t.Fatal("scrolling is not an answer")
	}
}

// The question and the answer go into the file together, so a
// reopened conversation shows what was asked and what was said
// rather than asking it again.
func TestAskRoundTripsThroughATurn(t *testing.T) {
	a := &answers{}
	m := asked(t, a)
	press(m, "y")
	m.turnStart = 0 // the turn began at the top of this transcript
	evs := m.record().Events
	var ask *agent.Event
	for i := range evs {
		if evs[i].Kind == agent.KindAsk {
			ask = &evs[i]
		}
	}
	if ask == nil || ask.Result != agent.Yes || ask.Name != "write" {
		t.Fatalf("recorded %+v", ask)
	}

	// Replayed, it comes back answered and stops nothing.
	m2 := plain(t, 100, 30, Options{Name: "mote"})
	step(m2, events(*ask)...)
	if m2.asking() {
		t.Fatal("a replayed ask must not stop the terminal")
	}
	if got := m2.entries[0].answer; got != agent.Yes {
		t.Fatalf("answer %q", got)
	}
}

// A turn that ended on a question records it as a question, and it
// comes back cancelled rather than open.
func TestACancelledAskRoundTrips(t *testing.T) {
	m := asked(t, &answers{})
	m.turnStart = 0
	m.cancelAsk(true)
	evs := m.record().Events
	var ask agent.Event
	for _, ev := range evs {
		if ev.Kind == agent.KindAsk {
			ask = ev
		}
	}
	if ask.Result != "" {
		t.Fatalf("a cancelled ask has no answer: %+v", ask)
	}
	m2 := plain(t, 100, 30, Options{Name: "mote"})
	step(m2, events(ask)...)
	m2.cancelAsk(false)
	if m2.asking() || !m2.entries[0].cancelled {
		t.Fatalf("%+v", m2.entries[0])
	}
}

// The status line's hints become the three answers while one is open.
func TestAskHints(t *testing.T) {
	m := asked(t, &answers{})
	if got := m.hints(); !strings.Contains(got, "y yes") || !strings.Contains(got, "click") {
		t.Fatalf("hints %q", got)
	}
}

// Nothing spins under an open question: the card is the thing to
// look at, and a spinner says the opposite of "waiting for you".
func TestNoSpinnerUnderAnAsk(t *testing.T) {
	m := asked(t, &answers{})
	m.inflight = true
	if got := ansi.Strip(m.transcript()); strings.Contains(got, "thinking") {
		t.Fatalf("transcript ends with a spinner:\n%s", got)
	}
	press(m, "y")
	if got := ansi.Strip(m.transcript()); !strings.Contains(got, "thinking") {
		t.Fatalf("and it comes back once the question is answered:\n%s", got)
	}
}
