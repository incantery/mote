package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
		agent.Delta("Read it. The first milestone is the terminal.\n"),
		agent.Fail("upstream: 429 rate limited — retry in 12s"),
		agent.Done(),
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

// The transcript has to be right at the widths people actually use.
func TestTranscriptGolden(t *testing.T) {
	for _, w := range []int{80, 120, 200} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			m := withScene(t, w, 40, Options{Name: "mote", Model: "fake-1", Conversation: "demo-1"})
			golden(t, fmt.Sprintf("transcript-%d.txt", w), m.transcript())
		})
	}
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

// An expanded card shows the arguments and a window on the result.
func TestExpandedCardGolden(t *testing.T) {
	m := withScene(t, 100, 40, Options{Name: "mote", Model: "fake-1", Conversation: "demo-1"})
	step(m, kmsg("tab"), kmsg("ctrl+o"))
	golden(t, "card-expanded.txt", m.transcript())
}

// The whole screen, at the two sizes the brief names. The input is
// blurred so no cursor lands in the golden file.
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
			golden(t, fmt.Sprintf("screen-%dx%d.txt", sz.w, sz.h), m.View())
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
		lines := strings.Split(m.View(), "\n")
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
		lines := strings.Split(m.View(), "\n")
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
	if !strings.Contains(m.View(), "fleet") {
		t.Fatal("the rail is not on screen")
	}
	if m.vp.Width >= 120 {
		t.Fatalf("the transcript did not make room: width %d", m.vp.Width)
	}

	step(m, kmsg("ctrl+t"))
	if m.sideVisible() || strings.Contains(m.View(), "fleet") {
		t.Fatal("ctrl+t should hide the rail")
	}
	step(m, kmsg("ctrl+t"))

	step(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.sideVisible() {
		t.Fatal("the rail must hide itself below the width threshold")
	}
	if m.vp.Width != 80 {
		t.Fatalf("the transcript should take the whole width: %d", m.vp.Width)
	}
}
