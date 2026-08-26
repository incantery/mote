package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/incantery/mote/agent"
)

func kinds(m *Model) []entryKind {
	out := make([]entryKind, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e.kind)
	}
	return out
}

// A reply is one entry, however many deltas it arrived in.
func TestDeltasBecomeOneReply(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	step(m, events(agent.Status("thinking"))...)
	if m.statusText != "thinking" {
		t.Fatalf("status %q", m.statusText)
	}
	step(m, events(agent.Delta("hello "), agent.Delta("there"))...)
	if m.statusText != "" {
		t.Fatal("the first delta should clear the status line")
	}
	if m.partial != "hello there" {
		t.Fatalf("partial %q", m.partial)
	}
	if len(m.entries) != 0 {
		t.Fatal("nothing is finished until done")
	}
	step(m, events(agent.Done())...)
	if len(m.entries) != 1 || m.entries[0].kind != entryReply || m.entries[0].text != "hello there" {
		t.Fatalf("after done: %v", m.entries)
	}
	if m.partial != "" || m.inflight {
		t.Fatal("done should have ended the turn")
	}
}

func TestToolRound(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	step(m, events(
		agent.Delta("looking...\n"),
		agent.Call("c1", "read_file", `{"path":"README.md"}`),
	)...)
	// Text that came before the call is finished text.
	if got := kinds(m); len(got) != 2 || got[0] != entryReply || got[1] != entryTool {
		t.Fatalf("entries %v", got)
	}
	card := m.entries[1]
	if !card.running {
		t.Fatal("a card is running until its result arrives")
	}
	if !strings.Contains(m.transcript(), "read_file") {
		t.Fatal("the card is not in the transcript")
	}

	step(m, events(agent.Result("c1", "one\ntwo\nthree", 1500*time.Millisecond, 0.0021))...)
	if card.running || card.result == "" {
		t.Fatal("the result did not land on the card")
	}
	line := firstLine(m.renderTool(card, 100, false))
	for _, want := range []string{"read_file", "path=README.md", "1.50s", "$0.0021"} {
		if !strings.Contains(line, want) {
			t.Errorf("collapsed card %q is missing %q", line, want)
		}
	}
	if n := strings.Count(m.renderTool(card, 100, false), "\n"); n != 0 {
		t.Errorf("a collapsed card is one line, got %d more", n)
	}
}

// A result for a call nobody made is dropped, not crashed on.
func TestOrphanResultIsIgnored(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	step(m, events(agent.Result("nope", "x", time.Second, 0))...)
	if len(m.entries) != 0 {
		t.Fatalf("entries %v", kinds(m))
	}
}

// A notice mid-exchange lands in the transcript without cutting the
// reply that is still arriving in two.
func TestNoticeArrivesMidReply(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	step(m, events(
		agent.Delta("part one "),
		agent.Notice("task 184a1100 finished"),
		agent.Delta("part two"),
		agent.Done(),
	)...)
	got := kinds(m)
	if len(got) != 2 || got[0] != entryNotice || got[1] != entryReply {
		t.Fatalf("entries %v", got)
	}
	if m.entries[1].text != "part one part two" {
		t.Fatalf("the reply was split: %q", m.entries[1].text)
	}
}

// An error is visible, and does not end the exchange by itself.
func TestErrorIsVisibleAndNotFinal(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	m.inflight = true
	step(m, events(agent.Delta("half a thought"), agent.Fail("upstream: 429"))...)
	got := kinds(m)
	if len(got) != 2 || got[0] != entryReply || got[1] != entryError {
		t.Fatalf("entries %v", got)
	}
	if !m.inflight {
		t.Fatal("an error is not the end of a turn; done is")
	}
	if !strings.Contains(m.transcript(), "upstream: 429") {
		t.Fatal("the error is not in the transcript")
	}
	step(m, events(agent.Done())...)
	if m.inflight {
		t.Fatal("done should end the turn")
	}
}

// A Send that never started still ends the turn.
func TestTurnErrorEndsTheTurn(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	m.inflight = true
	step(m, turnMsg{err: errString("cannot reach verad")})
	if m.inflight {
		t.Fatal("still in flight")
	}
	last := m.entries[len(m.entries)-1]
	if last.kind != entryError || last.text != "cannot reach verad" {
		t.Fatalf("got %v %q", last.kind, last.text)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// Cards expand, and the last one expands without being found first.
func TestCardExpansion(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	body := strings.Repeat("a line of result\n", 40)
	step(m, events(
		agent.Call("c1", "grep", `{"pattern":"func Send"}`),
		agent.Result("c1", body, time.Second, 0),
	)...)

	step(m, kmsg("ctrl+o"))
	card := m.entries[0]
	if !card.expanded {
		t.Fatal("ctrl+o with nothing focused should open the last card")
	}
	out := m.renderTool(card, 100, false)
	if !strings.Contains(out, `"pattern": "func Send"`) {
		t.Fatal("expanded card does not show the arguments")
	}
	if n := strings.Count(out, "a line of result"); n != resultLines {
		t.Fatalf("expanded card shows %d result lines, want the cap %d", n, resultLines)
	}
	if !strings.Contains(out, "lines 1–12 of 40") {
		t.Fatalf("no window header:\n%s", out)
	}

	// Focused, page down scrolls the result rather than the transcript.
	step(m, kmsg("tab"))
	if m.focus != 0 {
		t.Fatalf("focus %d", m.focus)
	}
	before := m.vp.YOffset
	step(m, kmsg("pgdown"))
	if card.offset != resultLines {
		t.Fatalf("result offset %d", card.offset)
	}
	if m.vp.YOffset != before {
		t.Fatal("the transcript scrolled instead of the card")
	}
	// And it stops at the end rather than running off it.
	for range 10 {
		step(m, kmsg("pgdown"))
	}
	if card.offset != 40-resultLines {
		t.Fatalf("offset ran to %d, want %d", card.offset, 40-resultLines)
	}

	step(m, kmsg("esc"))
	if m.focus != -1 {
		t.Fatal("esc should let go of the card")
	}
}

// tab walks the cards, newest first, and falls off the end.
func TestCardFocusWalk(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	step(m, events(
		agent.Call("c1", "one", "{}"), agent.Result("c1", "a", time.Second, 0),
		agent.Call("c2", "two", "{}"), agent.Result("c2", "b", time.Second, 0),
	)...)
	step(m, kmsg("tab"))
	if m.focus != 1 {
		t.Fatalf("first tab focused %d, want the newest card", m.focus)
	}
	step(m, kmsg("shift+tab"))
	if m.focus != 0 {
		t.Fatalf("shift+tab focused %d", m.focus)
	}
	step(m, kmsg("shift+tab"))
	if m.focus != -1 {
		t.Fatalf("walking off the front left focus at %d", m.focus)
	}
}

// The finished transcript is rendered once. Re-rendering it after a
// delta would be the same bytes but a different allocation; the point
// is that the cached entry is reused, so identity is the test.
func TestFinishedEntriesAreCached(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	step(m, events(agent.Delta("a finished reply"), agent.Done())...)
	first := m.transcript()
	cached := m.entries[0].cache
	if cached == "" {
		t.Fatal("nothing was cached")
	}
	step(m, events(agent.Delta("now streaming"))...)
	if m.entries[0].cache != cached {
		t.Fatal("a delta re-rendered a finished entry")
	}
	if !strings.HasPrefix(m.transcript(), first) {
		t.Fatal("the finished transcript changed under a delta")
	}
	if m.stableN != 1 {
		t.Fatalf("the stable prefix is %d entries, want 1", m.stableN)
	}
}

// The view follows the tail while streaming, and stops when the person
// scrolls away from it.
func TestFollowsTailUntilScrolled(t *testing.T) {
	m := plain(t, 80, 12, Options{Name: "mote"})
	for range 40 {
		step(m, events(agent.Delta("a line of streamed reply\n\n"))...)
	}
	if !m.follow || !m.vp.AtBottom() {
		t.Fatal("streaming should hold the tail")
	}
	step(m, kmsg("pgup"))
	if m.follow {
		t.Fatal("scrolling up should stop the following")
	}
	at := m.vp.YOffset
	step(m, events(agent.Delta("more\n\n"))...)
	if m.vp.YOffset != at {
		t.Fatal("a delta yanked the view back down")
	}
	step(m, tea.KeyMsg{Type: tea.KeyCtrlL})
	if !m.follow || !m.vp.AtBottom() {
		t.Fatal("ctrl+l should return to the tail")
	}
}

// A second message while one is in flight is refused, not queued.
func TestNoSecondTurnWhileInFlight(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	m.inflight = true
	typeIn(m, "hello")
	step(m, kmsg("enter"))
	last := m.entries[len(m.entries)-1]
	if last.kind != entryError {
		t.Fatalf("got %v %q", last.kind, last.text)
	}
}

// What the application sends back from a command lands in the right
// shape.
func TestApplicationMessages(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote", Conversation: "one"})
	step(m,
		Note("a note")(),
		Fail("a failure")(),
		Show("# a block")(),
		SetConversation("two")(),
		sideMsg{[]SideItem{{ID: "x", Title: "a task", State: Done}}},
	)
	got := kinds(m)
	want := []entryKind{entryNotice, entryError, entryBlock}
	if len(got) != len(want) {
		t.Fatalf("entries %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entries %v, want %v", got, want)
		}
	}
	if m.conversation != "two" {
		t.Fatalf("conversation %q", m.conversation)
	}
	if len(m.side) != 1 || m.side[0].ID != "x" {
		t.Fatalf("side %v", m.side)
	}
	if !strings.Contains(m.statusLine(), "two") {
		t.Fatal("the status line did not follow the conversation")
	}
}

// Options.Side is polled, and what it returns is what the rail shows.
func TestSidePolling(t *testing.T) {
	n := 0
	m := plain(t, 120, 30, Options{Name: "mote", Side: func() []SideItem {
		n++
		return []SideItem{{ID: "x", Title: "poll " + strings.Repeat("!", n), State: Working}}
	}})
	step(m, sideTick{})
	if !strings.Contains(m.View(), "poll !!") {
		t.Fatalf("the rail did not repoll; calls=%d", n)
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// The conversation id is not write-only: an application that called
// Run hears about it, and one that embedded the Model can read it.
func TestConversationIsReadable(t *testing.T) {
	var heard []string
	m := plain(t, 100, 30, Options{
		Name: "mote", Conversation: "demo-1",
		OnConversation: func(id string) { heard = append(heard, id) },
	})
	if m.Conversation() != "demo-1" {
		t.Fatalf("Conversation() = %q", m.Conversation())
	}
	if len(heard) != 0 {
		t.Fatalf("the application chose demo-1; it does not need telling: %q", heard)
	}
	step(m, SetConversation("demo-2")())
	if m.Conversation() != "demo-2" {
		t.Fatalf("Conversation() = %q", m.Conversation())
	}
	// The same id twice is not a change.
	step(m, SetConversation("demo-2")())
	if len(heard) != 1 || heard[0] != "demo-2" {
		t.Fatalf("heard %q, want one demo-2", heard)
	}
}
