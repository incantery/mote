package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/incantery/mote/agent"
)

const reply = `## the seam

A harness is a loop over a model with tools. The pieces:

- ` + "`agent`" + ` — the loop, and this seam
- ` + "`tui`" + ` — the terminal

` + "```go" + `
type Agent interface {
	Send(ctx context.Context, conversation, text string) (<-chan Event, error)
}
` + "```" + `

| agent | where | transport |
| ----- | ----- | --------- |
| demo  | here  | a channel |
| vera  | verad | HTTP      |
`

// conversation is the scene the golden files hold: a question, a
// markdown answer, a tool round with a notice landing in the middle,
// and an error that does not end the world.
func conversation() []tea.Msg {
	var msgs []tea.Msg
	msgs = append(msgs, eventMsg{agent.Delta("")}) // no-op, keeps the shape obvious
	msgs = append(msgs, events(
		agent.Status("thinking"),
	)...)
	for _, chunk := range strings.SplitAfter(reply, "\n") {
		msgs = append(msgs, eventMsg{agent.Delta(chunk)})
	}
	msgs = append(msgs, events(
		agent.Call("call_1", "read_file", `{"path":"README.md","limit":200}`),
		agent.Status("reading README.md"),
		agent.Result("call_1", "# mote\n\nA small agent harness, in Go.\nline three\nline four\nline five", 1420*time.Millisecond, 0.0021),
		agent.Notice("task 184a1100 finished — /report 184a1100"),
		agent.Call("call_2", "shell", `{"cmd":"go test ./... -race"}`),
		agent.Output("call_2", "ok\tgithub.com/incantery/mote/agent\t0.006s\n"),
		agent.Output("call_2", "ok\tgithub.com/incantery/mote/session\t0.008s\n"),
		agent.Result("call_2", "", 9840*time.Millisecond, 0.0007),
		agent.Delta("Read it. The first milestone is the terminal.\n"),
		agent.Fail("upstream: 429 rate limited — retry in 12s"),
		agent.Spent(0.0138, 18422, 611),
	)...)
	return msgs
}

func withScene(t *testing.T, w, h int, opts Options) *Model {
	t.Helper()
	m := plain(t, w, h, opts)
	step(m, kmsg("w"), kmsg("h"), kmsg("y"), kmsg("?"), kmsg("enter"))
	step(m, conversation()...)
	return m
}

// A line that fits its window stays on one line — at the width it
// actually needs, not four columns wider. Glamour spends two columns
// on a document margin, keeps two in reserve, and pads every inline
// code span with a space either side; a greeting that was one sentence
// long broke in the middle of itself because of it.
func TestMarkdownUsesTheWholeWidth(t *testing.T) {
	const line = "say something, or `/` for commands — `/help` has the keys, " +
		"and the rail on the right is every task."
	for _, style := range []string{"ascii", "dark", "light", "notty"} {
		t.Run(style, func(t *testing.T) {
			md := newMarkdown(style)
			wide := md.render(line, 200, false)
			if strings.Contains(wide, "\n") {
				t.Fatalf("200 columns was not enough for one sentence:\n%s", wide)
			}
			w := lipglossWidth(strings.TrimRight(ansi.Strip(wide), " "))
			if got := md.render(line, w, false); strings.Contains(got, "\n") {
				t.Errorf("at %d columns — its own width — it wrapped:\n%s", w, ansi.Strip(got))
			}
		})
	}
}

// The transcript has to be right at the widths people actually use.
func TestTranscriptGolden(t *testing.T) {
	for _, w := range []int{80, 120, 200} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			m := withScene(t, w, 40, Options{Name: "mote", Model: "fake-1", Conversation: "demo-1"})
			golden(t, fmt.Sprintf("transcript-%d.txt", w), m.transcript())
		})
	}
}

// The registers, in one transcript: two exchanges with a rule between
// them, a card the agent summarized in its own words, a burst of
// notices grouped into one aside, what a command printed, an error,
// and the reply still arriving with a cursor at the end of it. If any
// two of these ever look the same, this golden says so.
func TestRegistersGolden(t *testing.T) {
	m := plain(t, 100, 44, Options{
		Name: "mote", Model: "fake-1", Conversation: "demo-1", Timestamps: true,
	})
	typeIn(m, "what happened while I was out?")
	step(m, kmsg("enter"))
	step(m, events(
		agent.Delta("Three things, and one of them wants you.\n"),
		agent.Call("c1", "fleet", `{"verb":"start","repo":"vera","brief":"ship the price table"}`).
			WithSummary("started a ship task in vera → 05a40191"),
		agent.Result("c1", "05a40191 started", 473*time.Millisecond, 0),
		agent.Notice("05a40191 is working — ship the price table"),
		agent.Notice("c41f9a02 finished — /report c41f9a02"),
		agent.Notice("7b20e5d9 needs you — a question on the tool registry"),
		agent.Delta("The scout is done and nobody has read it.\n"),
		agent.Spent(0.0041, 8210, 190),
	)...)
	step(m, Note("dumped → /tmp/mote-2026-08-28.tar.gz")())
	step(m, Show("## report — c41f9a02\n\nThe seam is one method. `mote demo` shows it.\n")())
	step(m, Fail("verad: 502 from /fleet — retrying")())
	typeIn(m, "read it to me")
	step(m, kmsg("enter"))
	step(m, events(agent.Delta("It says the seam is one method"))...)

	// The clock is the one thing in here nobody can pin, so it is
	// pinned: what the golden is for is the shape of the rule, not
	// what time it was.
	at := time.Date(2026, 8, 28, 14, 3, 0, 0, time.UTC)
	for _, e := range m.entries {
		if e.kind == entryUser {
			e.at = at
			e.invalidate()
			at = at.Add(90 * time.Second)
		}
	}
	m.resetStable()
	golden(t, "registers.txt", m.transcript())
}

// Mid-flight: a running card with its spinner, a status line, and the
// reply arriving under it — including a code fence that is still open.
func TestStreamingGolden(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote", Model: "fake-1", Conversation: "demo-1"})
	step(m, kmsg("h"), kmsg("i"), kmsg("enter"))
	m.inflight = true
	step(m, events(
		agent.Call("c1", "read_file", `{"path":"README.md","limit":200}`),
		agent.Result("c1", "# mote\n\nA small agent harness.", 420*time.Millisecond, 0.0003),
		agent.Call("c2", "grep", `{"pattern":"func Send","glob":"**/*.go"}`),
		agent.Delta("Here is what I found so far:\n\n```go\nfunc Send("),
		agent.Status("searching"),
	)...)
	golden(t, "streaming.txt", m.transcript())
}

// The cursor at the end of a reply that is still arriving never
// pushes a line past the window, however long the last word was.
func TestStreamCursorStaysInsideTheWindow(t *testing.T) {
	for _, w := range []int{40, 61, 80} {
		m := plain(t, w, 20, Options{Name: "mote"})
		step(m, kmsg("h"), kmsg("i"), kmsg("enter"))
		m.inflight = true
		step(m, events(agent.Delta(strings.Repeat("word ", 60)))...)
		out := ansi.Strip(m.transcript())
		if !strings.Contains(out, "▍") {
			t.Fatalf("%d: nothing says the reply is still arriving:\n%s", w, out)
		}
		for i, l := range strings.Split(out, "\n") {
			if got := lipglossWidth(l); got > w {
				t.Errorf("%d columns: line %d is %d wide: %q", w, i, got, l)
			}
		}
		// And it goes when the reply does.
		step(m, events(agent.Done())...)
		if strings.Contains(ansi.Strip(m.transcript()), "▍") {
			t.Errorf("%d: the cursor outlived the reply", w)
		}
	}
}

// An expanded card shows the arguments and a window on the result.
func TestExpandedCardGolden(t *testing.T) {
	m := withScene(t, 100, 40, Options{Name: "mote", Model: "fake-1", Conversation: "demo-1"})
	step(m, kmsg("tab"), kmsg("ctrl+o"))
	golden(t, "card-expanded.txt", m.transcript())
}

// The whole screen, at the two sizes the brief names. The input is
// blurred so that the box is drawn the way an unfocused one is; the
// cursor is a field of the frame now and was never in the content.
func TestScreenGolden(t *testing.T) {
	side := func() []SideItem {
		return []SideItem{
			{ID: "184a1100", Title: "build mote's first milestone", Subtitle: "mote", State: Working, Current: true},
			{ID: "c41f9a02", Title: "tool registry with policy", Subtitle: "mote", State: Idle},
			{ID: "7b20e5d9", Title: "session on disk", Subtitle: "mote", State: Blocked},
			{ID: "0f3c8811", Title: "anthropic provider", Subtitle: "mote", State: Done},
		}
	}
	for _, sz := range []struct{ w, h int }{{80, 24}, {200, 50}} {
		t.Run(fmt.Sprintf("%dx%d", sz.w, sz.h), func(t *testing.T) {
			m := withScene(t, sz.w, sz.h, Options{
				Name: "mote", Model: "fake-1", Conversation: "demo-1",
				Side: side, SideTitle: "fleet",
			})
			m.in.ta.Blur()
			golden(t, fmt.Sprintf("screen-%dx%d.txt", sz.w, sz.h), view(m))
		})
	}
}

// Every line of the screen must fit the window, or the terminal wraps
// it and the layout comes apart. This is the check that 80x24 works.
func TestScreenFits(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{80, 24}, {100, 30}, {200, 50}, {60, 20}} {
		m := withScene(t, sz.w, sz.h, Options{
			Name: "mote", Model: "fake-1", Conversation: "demo-1",
			Side: func() []SideItem {
				return []SideItem{{ID: "184a1100", Title: "a task with a fairly long title indeed", State: Working, Current: true}}
			},
		})
		m.in.ta.Blur()
		lines := strings.Split(view(m), "\n")
		if len(lines) != sz.h {
			t.Errorf("%dx%d: %d lines, want %d", sz.w, sz.h, len(lines), sz.h)
		}
		for i, l := range lines {
			if w := lipglossWidth(l); w > sz.w {
				t.Errorf("%dx%d: line %d is %d wide: %q", sz.w, sz.h, i, w, l)
			}
		}
	}
}

// Resizing must not leave stale geometry behind.
func TestResize(t *testing.T) {
	m := withScene(t, 200, 50, Options{Name: "mote"})
	for _, sz := range []struct{ w, h int }{{80, 24}, {200, 50}, {100, 30}, {80, 24}} {
		step(m, tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
		lines := strings.Split(view(m), "\n")
		if len(lines) != sz.h {
			t.Fatalf("after resize to %dx%d: %d lines", sz.w, sz.h, len(lines))
		}
	}
	// And the transcript comes back byte-identical at a width it has
	// already been rendered at, so the cache is not lying.
	a := m.transcript()
	step(m, tea.WindowSizeMsg{Width: 120, Height: 40}, tea.WindowSizeMsg{Width: 80, Height: 24})
	if b := m.transcript(); a != b {
		t.Error("transcript differs after a round trip through another width")
	}
}

// tasks is a fleet of n, for a rail that has to say what it dropped.
func tasks(n int) []SideItem {
	titles := []string{"build mote's first milestone", "tool registry with policy",
		"session on disk, resumable", "anthropic-native provider", "mcp, as a tool source",
		"the rail says what it dropped", "a notice with an identity", "cost, per provider",
		"publish the module"}
	states := []State{Working, Idle, Blocked, Done, Failed}
	out := make([]SideItem, 0, n)
	for i := range n {
		out = append(out, SideItem{
			ID:       fmt.Sprintf("%08x", 0x184a1100+i*0x9e37),
			Title:    titles[i%len(titles)],
			Subtitle: "mote",
			State:    states[i%len(states)],
			Current:  i == 0,
		})
	}
	return out
}

// A rail with more in it than there is room for says how much more.
// "Four tasks" and "four of nine" are different situations and a
// person acts on them differently.
func TestSideSaysWhatDidNotFit(t *testing.T) {
	items := tasks(9)
	m := plain(t, 120, 30, Options{Name: "mote", Side: func() []SideItem { return items }, SideTitle: "fleet"})

	// Room for the title, the rule and four items, and one line left
	// over for the count.
	rail := m.renderSide(32, 11)
	if !strings.Contains(rail, "+5 more") {
		t.Errorf("the rail dropped five tasks without saying so:\n%s", rail)
	}
	if strings.Contains(rail, items[4].Title) {
		t.Errorf("the fifth task should not have fitted:\n%s", rail)
	}
	if n := len(strings.Split(rail, "\n")); n != 11 {
		t.Errorf("the rail is %d lines, want 11", n)
	}

	// Given the room, it says nothing about what did not fit.
	if rail := m.renderSide(32, 30); strings.Contains(rail, "more") {
		t.Errorf("everything fitted; the rail should not be counting:\n%s", rail)
	}
	golden(t, "rail-32x11.txt", m.renderSide(32, 11))
}

// An item waiting on the person does not look like one that is done.
// A scout whose report nobody has read is finished and still wants
// you, and a tick says the opposite.
func TestRailSaysWhichOnesNeedYou(t *testing.T) {
	items := []SideItem{
		{ID: "0f3c8811", Title: "anthropic provider", Subtitle: "mote", State: Done},
		{ID: "c41f9a02", Title: "tool registry with policy", Subtitle: "mote", State: Done, Needs: true},
	}
	m := plain(t, 120, 30, Options{Name: "mote", Side: func() []SideItem { return items }, SideTitle: "fleet"})
	rail := ansi.Strip(m.renderSide(48, 12))
	lines := strings.Split(rail, "\n")
	done, needs := lines[2], lines[4]
	if !strings.Contains(done, "✓") {
		t.Errorf("a done task is a tick: %q", done)
	}
	if strings.Contains(needs, "✓") {
		t.Errorf("a task that wants you is not a tick: %q", needs)
	}
	if !strings.Contains(lines[5], "needs you") {
		t.Errorf("the rail does not say what it wants: %q", lines[5])
	}
	if !strings.Contains(lines[5], "done") {
		t.Errorf("it is still done, and the rail should say so: %q", lines[5])
	}
}

// The rail is a rail: it appears on the right, and it goes away when
// the window is too narrow to deserve it.
func TestSidePane(t *testing.T) {
	side := func() []SideItem {
		return []SideItem{{ID: "184a1100", Title: "first milestone", State: Working, Current: true}}
	}
	m := plain(t, 120, 30, Options{Name: "mote", Side: side, SideTitle: "fleet"})
	if !m.sideVisible() {
		t.Fatal("the rail should be open at 120 columns")
	}
	if !strings.Contains(view(m), "fleet") {
		t.Fatal("the rail is not on screen")
	}
	if m.vp.Width() >= 120 {
		t.Fatalf("the transcript did not make room: width %d", m.vp.Width())
	}

	step(m, kmsg("ctrl+t"))
	if m.sideVisible() || strings.Contains(view(m), "fleet") {
		t.Fatal("ctrl+t should hide the rail")
	}
	step(m, kmsg("ctrl+t"))

	step(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.sideVisible() {
		t.Fatal("the rail must hide itself below the width threshold")
	}
	if m.vp.Width() != 80 {
		t.Fatalf("the transcript should take the whole width: %d", m.vp.Width())
	}
}

// The ask card, open and then answered, at the width most windows
// are. It is the one card a person has to act on, so what it looks
// like is worth pinning.
func TestAskCardGolden(t *testing.T) {
	a := &answers{}
	m := plain(t, 80, 30, Options{Name: "mote"})
	m.agent = a
	step(m, events(agent.Asking("call_7", "write",
		`{"path":"/tmp/scratch/notes.md","content":"# what the policy decided\n"}`,
		"outside ~/vera and not a project — the profile says ask"))...)
	open := ansi.Strip(m.renderAsk(m.entries[0], 80))
	press(m, "a")
	answered := ansi.Strip(m.renderAsk(m.entries[0], 80))
	golden(t, "ask-card.txt", open+"\n\n"+answered+"\n")
}
