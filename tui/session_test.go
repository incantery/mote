package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/incantery/mote/agent"
	"github.com/incantery/mote/session"
)

func openSession(t *testing.T, dir, id string) *session.Session {
	t.Helper()
	s, err := session.Open(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// The whole point of the package: a conversation written to disk and
// reopened is the transcript that was on screen — not something like
// it. Both are checked against the golden file the live rendering
// keeps, so the two cannot drift apart without a test failing.
func TestSessionRebuildsTheTranscript(t *testing.T) {
	dir := t.TempDir()
	opts := func(s *session.Session) Options {
		return Options{Name: "mote", Model: "fake-1", Conversation: "demo-1", Session: s}
	}

	first := openSession(t, dir, "demo-1")
	live := withScene(t, 120, 40, opts(first))
	golden(t, "transcript-120.txt", live.transcript())
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	again := openSession(t, dir, "demo-1")
	if n := len(again.Turns()); n != 1 {
		t.Fatalf("%d turns on disk, want the one exchange", n)
	}
	rebuilt := plain(t, 120, 40, opts(again))
	if got, want := rebuilt.transcript(), live.transcript(); got != want {
		t.Errorf("the rebuilt transcript differs from the live one.\n--- rebuilt ---\n%s\n--- live ---\n%s", got, want)
	}
	golden(t, "transcript-120.txt", rebuilt.transcript())

	// Cards come back closed, and nothing is left in flight.
	for _, e := range rebuilt.entries {
		if e.kind == entryTool && (e.expanded || e.running) {
			t.Errorf("card %q came back expanded=%v running=%v", e.name, e.expanded, e.running)
		}
	}
	if rebuilt.inflight || rebuilt.partial != "" {
		t.Error("a rebuilt transcript is not mid-turn")
	}
	// And what was streamed came back with it.
	var shell *entry
	for _, e := range rebuilt.entries {
		if e.name == "shell" {
			shell = e
		}
	}
	if shell == nil || !strings.Contains(shell.output, "mote/session") {
		t.Fatalf("the streamed output did not survive: %+v", shell)
	}
}

// The input history outlives the process, and the up arrow finds it.
// The sentence on a card is part of the transcript, so it survives
// the file: a reopened conversation says what the call did, not what
// it was called with.
func TestSessionKeepsTheCardsSentence(t *testing.T) {
	dir := t.TempDir()
	s := openSession(t, dir, "c")
	m := plain(t, 100, 30, Options{Name: "mote", Session: s})
	typeIn(m, "start one")
	step(m, kmsg("enter"))
	step(m, events(
		agent.Call("c1", "fleet", `{"verb":"start","repo":"vera"}`).
			WithSummary("started a ship task in vera → 05a40191"),
		agent.Result("c1", "05a40191", 473*time.Millisecond, 0),
		agent.Done(),
	)...)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	again := openSession(t, dir, "c")
	back := plain(t, 100, 30, Options{Name: "mote", Session: again})
	line := firstLine(back.renderTool(back.entries[1], 100, false))
	if !strings.Contains(line, "started a ship task in vera") {
		t.Errorf("the reopened card lost its sentence: %q", line)
	}
}

func TestSessionRestoresTheHistory(t *testing.T) {
	dir := t.TempDir()
	first := openSession(t, dir, "c")
	m := plain(t, 100, 30, Options{Name: "mote", Session: first,
		Commands: []Command{{Name: "tasks", Help: "the fleet"}}})
	typeIn(m, "the first thing")
	step(m, kmsg("enter"))
	step(m, events(agent.Delta("ok"), agent.Done())...)
	typeIn(m, "/tasks")
	step(m, kmsg("enter"))
	first.Close()

	again := openSession(t, dir, "c")
	m2 := plain(t, 100, 30, Options{Name: "mote", Session: again})
	step(m2, kmsg("up"))
	if got := m2.in.value(); got != "/tasks" {
		t.Fatalf("the first up arrow found %q, want the last line sent", got)
	}
	step(m2, kmsg("up"))
	if got := m2.in.value(); got != "the first thing" {
		t.Fatalf("the second up arrow found %q", got)
	}
	// A command is history, not a turn.
	if n := len(again.Turns()); n != 1 {
		t.Fatalf("%d turns, want one — a slash command is not an exchange", n)
	}
}

// A turn is written once, however the exchange ends: done and the end
// of the stream both arrive, and only one of them may write.
func TestTurnIsWrittenOnce(t *testing.T) {
	dir := t.TempDir()
	s := openSession(t, dir, "c")
	m := plain(t, 100, 30, Options{Name: "mote", Session: s})
	typeIn(m, "hello")
	step(m, kmsg("enter"))
	step(m, events(agent.Delta("hi"), agent.Done())...)
	step(m, turnMsg{nil}) // what the stream goroutine sends after the channel closes
	if n := len(s.Turns()); n != 1 {
		t.Fatalf("%d turns for one exchange", n)
	}
	turn := s.Turns()[0]
	if turn.Said != "hello" || turn.Answered() != "hi" {
		t.Fatalf("turn %+v", turn)
	}
	if turn.Ended.Before(turn.At) {
		t.Fatalf("turn ended before it started: %v … %v", turn.At, turn.Ended)
	}
}

// An exchange that failed is the one most worth having on disk.
func TestFailedTurnIsRecorded(t *testing.T) {
	dir := t.TempDir()
	s := openSession(t, dir, "c")
	m := plain(t, 100, 30, Options{Name: "mote", Session: s})
	typeIn(m, "say something")
	step(m, kmsg("enter"))
	step(m, turnMsg{err: errString("cannot reach verad")})
	turns := s.Turns()
	if len(turns) != 1 {
		t.Fatalf("%d turns", len(turns))
	}
	last := turns[0].Events[len(turns[0].Events)-1]
	if last.Kind != agent.KindError || last.Text != "cannot reach verad" {
		t.Fatalf("the failure is not in the record: %+v", turns[0].Events)
	}
}

// Nil Session is the old behaviour, exactly.
func TestNoSessionKeepsWorking(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	typeIn(m, "hello")
	step(m, kmsg("enter"))
	step(m, events(agent.Delta("hi"), agent.Spent(0.01, 100, 10))...)
	if len(m.entries) != 2 {
		t.Fatalf("entries %v", kinds(m))
	}
	if m.total != 0.01 {
		t.Fatalf("totals still add up without a file: %v", m.total)
	}
}

// SetSession is the other half of SetConversation: a /new writes into
// the new file, and what is on screen stays where it is.
func TestSetSessionSwapsTheFile(t *testing.T) {
	dir := t.TempDir()
	one, two := openSession(t, dir, "one"), openSession(t, dir, "two")
	m := plain(t, 100, 30, Options{Name: "mote", Session: one, Conversation: "one"})
	typeIn(m, "first")
	step(m, kmsg("enter"))
	step(m, events(agent.Delta("a"), agent.Done())...)

	before := len(m.entries)
	step(m, SetConversation("two")(), SetSession(two)())
	if len(m.entries) != before {
		t.Fatal("swapping the session should not touch the transcript")
	}
	typeIn(m, "second")
	step(m, kmsg("enter"))
	step(m, events(agent.Delta("b"), agent.Done())...)

	if n := len(one.Turns()); n != 1 {
		t.Fatalf("the old file has %d turns", n)
	}
	if n := len(two.Turns()); n != 1 {
		t.Fatalf("the new file has %d turns", n)
	}
	if two.Turns()[0].Said != "second" {
		t.Fatalf("the new file got %q", two.Turns()[0].Said)
	}
}

// --- tool output --------------------------------------------------------

// A tool that talks while it runs fills the open card, and the result
// ends it without throwing away what was said.
func TestToolOutputStreamsIntoTheCard(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	step(m, events(agent.Call("c1", "shell", `{"cmd":"go test ./..."}`))...)
	card := m.entries[0]
	step(m, events(
		agent.Output("c1", "ok\tmote/agent\t"),
		agent.Output("c1", "0.006s\n"),
		agent.Output("c1", "ok\tmote/tui\t8.325s\n"),
	)...)
	if card.output != "ok\tmote/agent\t0.006s\nok\tmote/tui\t8.325s\n" {
		t.Fatalf("output %q", card.output)
	}
	// Collapsed, it says how much has arrived.
	line := firstLine(m.renderTool(card, 100, false))
	for _, want := range []string{"shell", "2 lines", "B"} {
		if !strings.Contains(line, want) {
			t.Errorf("the running card %q is missing %q", line, want)
		}
	}
	// Expanded, it shows what has arrived.
	step(m, kmsg("ctrl+o"))
	out := m.renderTool(card, 100, false)
	if !strings.Contains(out, "output · 2 lines") || !strings.Contains(out, "mote/tui") {
		t.Fatalf("expanded, running:\n%s", out)
	}

	// The result ends the card and keeps the output as the body.
	step(m, events(agent.Result("c1", "", 9840*time.Millisecond, 0.0007))...)
	if card.running {
		t.Fatal("the result did not end the card")
	}
	line = firstLine(m.renderTool(card, 100, false))
	if !strings.Contains(line, "9.84s") || !strings.Contains(line, "$0.0007") {
		t.Errorf("the finished card %q lost its duration or cost", line)
	}
	if strings.Contains(line, "lines") {
		t.Errorf("the finished card %q is still counting output", line)
	}
	if card.body() != card.output {
		t.Errorf("an empty result threw the output away: %q", card.body())
	}

	// Output for a call nobody made is dropped, not crashed on.
	step(m, events(agent.Output("nope", "x"))...)
	if len(m.entries) != 1 {
		t.Fatalf("entries %v", kinds(m))
	}
}

// A running card is followed, not paged: what a long command has just
// printed is the part worth looking at.
func TestRunningCardFollowsItsOutput(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	step(m, events(agent.Call("c1", "shell", "{}"))...)
	card := m.entries[0]
	for i := range 40 {
		step(m, events(agent.Output("c1", "line "+string(rune('a'+i%26))+"\n"))...)
	}
	step(m, kmsg("ctrl+o"))
	out := m.renderTool(card, 100, false)
	if !strings.Contains(out, "lines 29–40 of 40") {
		t.Fatalf("a running card should show the tail:\n%s", out)
	}
	// Once it ends, it is a window from the top like any other.
	step(m, events(agent.Result("c1", "", time.Second, 0))...)
	out = m.renderTool(card, 100, false)
	if !strings.Contains(out, "lines 1–12 of 40") {
		t.Fatalf("a finished card pages from the top:\n%s", out)
	}
}

// A tool that both streams and returns shows both, in order.
func TestOutputAndResultAreBothKept(t *testing.T) {
	e := &entry{kind: entryTool, output: "one\ntwo\n", result: "exit 0"}
	if got := e.body(); got != "one\ntwo\nexit 0" {
		t.Fatalf("body %q", got)
	}
	if got := (&entry{result: "only"}).body(); got != "only" {
		t.Fatalf("body %q", got)
	}
	if got := (&entry{output: "only\n"}).body(); got != "only\n" {
		t.Fatalf("body %q", got)
	}
}

// A turn that ended with a call still open leaves a card that is not
// running any more — and comes back that way, rather than spinning
// forever in a transcript where nothing is happening.
func TestInterruptedCallDoesNotSpinForever(t *testing.T) {
	dir := t.TempDir()
	s := openSession(t, dir, "c")
	m := plain(t, 100, 30, Options{Name: "mote", Session: s})
	typeIn(m, "run something slow")
	step(m, kmsg("enter"))
	step(m, events(agent.Call("c1", "shell", `{"cmd":"sleep 900"}`))...)
	step(m, kmsg("esc")) // stop the turn
	step(m, events(agent.Done())...)

	card := m.entries[1]
	if card.running || !card.stopped() {
		t.Fatalf("the card is running=%v stopped=%v", card.running, card.stopped())
	}
	if m.moving() {
		t.Fatal("nothing is happening, but the terminal still wants frames")
	}
	line := firstLine(m.renderTool(card, 100, false))
	if !strings.Contains(line, "stopped") || !strings.Contains(line, "✗") {
		t.Errorf("the card says %q", line)
	}

	s.Close()
	again := openSession(t, dir, "c")
	m2 := plain(t, 100, 30, Options{Name: "mote", Session: again})
	if got, want := m2.transcript(), m.transcript(); got != want {
		t.Errorf("reopened:\n%s\nwas:\n%s", got, want)
	}
	if m2.moving() {
		t.Fatal("a reopened transcript is not moving")
	}
}

// --- totals -------------------------------------------------------------

// The status line carries the turn while it runs and the conversation
// once it is over.
func TestTotals(t *testing.T) {
	dir := t.TempDir()
	s := openSession(t, dir, "c")
	m := plain(t, 120, 30, Options{Name: "mote", Session: s})
	typeIn(m, "run the tests")
	step(m, kmsg("enter"))
	if !m.inflight {
		t.Fatal("enter should have started a turn")
	}
	if got := m.totals(); got != "" {
		t.Fatalf("nothing has been spent yet, but the line says %q", got)
	}

	step(m, events(
		agent.Call("c1", "shell", "{}"),
		agent.Result("c1", "", time.Second, 0.0007),
	)...)
	if got := m.totals(); got != "$0.0007" {
		t.Fatalf("mid-turn the line says %q, want the turn so far", got)
	}
	if !strings.Contains(m.statusLine(), "$0.0007") {
		t.Fatalf("the status line lost the total: %q", m.statusLine())
	}

	// done carries the model's own spend and the turn's tokens.
	step(m, events(agent.Spent(0.0138, 18422, 611))...)
	if got := m.totals(); got != "$0.0145 · 19.0k tok" {
		t.Fatalf("after the turn the line says %q", got)
	}

	// A second turn's total adds to the first.
	typeIn(m, "again")
	step(m, kmsg("enter"))
	step(m, events(agent.Spent(0.01, 1000, 100))...)
	if got := m.totals(); got != "$0.0245 · 20.1k tok" {
		t.Fatalf("two turns in, the line says %q", got)
	}

	// And it is the same number after reopening the file.
	s.Close()
	again := openSession(t, dir, "c")
	m2 := plain(t, 120, 30, Options{Name: "mote", Session: again})
	if got := m2.totals(); got != "$0.0245 · 20.1k tok" {
		t.Fatalf("reopened, the line says %q", got)
	}
}

// Replaying a file must not charge for it a second time.
func TestRestoreDoesNotDoubleCount(t *testing.T) {
	dir := t.TempDir()
	s := openSession(t, dir, "c")
	m := plain(t, 100, 30, Options{Name: "mote", Session: s})
	typeIn(m, "hello")
	step(m, kmsg("enter"))
	step(m, events(
		agent.Call("c1", "shell", "{}"),
		agent.Result("c1", "x", time.Second, 0.002),
		agent.Spent(0.003, 10, 5),
	)...)
	s.Close()

	again := openSession(t, dir, "c")
	m2 := plain(t, 100, 30, Options{Name: "mote", Session: again})
	if m2.total != m.total {
		t.Fatalf("restored total %v, live %v", m2.total, m.total)
	}
	if m2.turnCost != 0 {
		t.Fatalf("a replayed file left a turn in flight: %v", m2.turnCost)
	}
}

func TestFormatting(t *testing.T) {
	for in, want := range map[int]string{0: "0", 611: "611", 18422: "18.4k", 1_234_567: "1.2M"} {
		if got := formatTokens(in); got != want {
			t.Errorf("formatTokens(%d) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"":                         "1 line · 0 B",
		"one\n":                    "1 line · 4 B",
		"one\ntwo":                 "2 lines · 7 B",
		"one\ntwo\n":               "2 lines · 8 B",
		strings.Repeat("x\n", 700): "700 lines · 1.4 kB",
	} {
		if got := formatVolume(in); got != want {
			t.Errorf("formatVolume(%q) = %q, want %q", in, got, want)
		}
	}
}

// A notice that named a thing still names it after a reopen, so the
// task it is about goes on having one line and not two.
func TestSessionKeepsWhatANoticeWasAbout(t *testing.T) {
	dir := t.TempDir()
	first := openSession(t, dir, "n")
	m := plain(t, 100, 30, Options{Name: "mote", Conversation: "n", Session: first})
	typeIn(m, "start something")
	step(m, kmsg("enter"))
	step(m, events(
		agent.About("184a1100", "184a1100 is working"),
		agent.Delta("started it"),
		agent.Done(),
	)...)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	again := openSession(t, dir, "n")
	back := plain(t, 100, 30, Options{Name: "mote", Conversation: "n", Session: again})
	step(back, events(agent.About("184a1100", "184a1100 is done"))...)
	got := back.transcript()
	if strings.Contains(got, "is working") {
		t.Errorf("the reopened notice lost its name and said it twice:\n%s", got)
	}
	if !strings.Contains(got, "is done") {
		t.Errorf("the notice never caught up:\n%s", got)
	}
}

// A conversation that ended on a question comes back with the
// question, closed — and nobody is told about it. Answering a call id
// out of an old conversation is how a fresh call with the same id
// gets answered before it is asked.
func TestReopeningACancelledAskTellsNobody(t *testing.T) {
	dir := t.TempDir()
	s, err := session.Open(dir, "asked")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(session.Turn{
		At: time.Now(), Said: "write something",
		Events: []agent.Event{agent.Asking("call_7", "write", `{"path":"/tmp/x"}`, "ask")},
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	again, err := session.Open(dir, "asked")
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	a := &answers{}
	pal := DefaultPalette()
	pal.Markdown = "ascii"
	m := New(a, Options{Name: "mote", Palette: &pal, Session: again})
	step(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	if m.asking() {
		t.Fatal("a replayed question must not stop the terminal")
	}
	if len(a.all()) != 0 {
		t.Fatalf("nobody should have been answered: %v", a.all())
	}
	var card *entry
	for _, e := range m.entries {
		if e.kind == entryAsk {
			card = e
		}
	}
	if card == nil || !card.cancelled {
		t.Fatalf("the question should be back, closed: %+v", card)
	}
}
