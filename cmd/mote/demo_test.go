package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/incantery/mote/agent"
	"github.com/incantery/mote/session"
	"github.com/incantery/mote/tui"
)

// screen builds the demo's terminal — the same fleet, the same
// greeting, the same options — with the colour switched off, so a test
// can read what a person would see.
func screen(t *testing.T, w, h int) (*tui.Model, *fleet) {
	t.Helper()
	sess, err := session.Open(t.TempDir(), "demo-1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	f := &fleet{items: seed()}
	pal := tui.DefaultPalette()
	pal.Markdown = "ascii"
	m := tui.New(&agent.Fake{Instant: true}, tui.Options{
		Name: "mote", Model: "fake-1", Conversation: "demo-1",
		Session:     sess,
		Palette:     &pal,
		Greeting:    greeting(sess, "/src/mote", "/src/mote/profiles/supervisor", "/tmp/mote-demo-x"),
		Side:        f.snapshot,
		SideTitle:   "fleet",
		StatusRight: f.summary,
	})
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m, f
}

// view is the frame's content with the colour taken off, which is
// what a person reads.
func view(m *tui.Model) string { return ansi.Strip(m.View().Content) }

// rail is the right-hand column of the screen, on its own.
func rail(view string) string {
	var b strings.Builder
	for _, line := range strings.Split(view, "\n") {
		if i := strings.LastIndex(line, "│ "); i >= 0 {
			b.WriteString(strings.TrimRight(line[i+len("│ "):], " ") + "\n")
		}
	}
	return b.String()
}

// What this milestone added is on the demo's screen, at a window of
// the size people actually have.
func TestDemoShowsTheLot(t *testing.T) {
	m, f := screen(t, 120, 30)
	screen := view(m)
	t.Logf("\n%s", screen)

	// A rail longer than the pane, and a count of what did not fit.
	side := rail(screen)
	if !strings.Contains(side, " more") {
		t.Errorf("the rail fitted all %d tasks at 120x30, so it shows nothing:\n%s", len(f.items), side)
	}
	if strings.Contains(side, f.items[len(f.items)-1].Title) {
		t.Errorf("the last task fitted; the rail is not truncating:\n%s", side)
	}

	// The fleet in a phrase, on the right of the status line.
	want := "3 blocked · 1 failed · 2 working · 2 done · 6 idle"
	if got := f.summary(); got != want {
		t.Errorf("summary() = %q, want %q", got, want)
	}
	last := screen[strings.LastIndex(screen, "\n")+1:]
	if !strings.Contains(last, want) {
		t.Errorf("the status line does not carry the fleet: %q", last)
	}

	// Given the room, the rail says nothing about what did not fit.
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if side := rail(view(m)); strings.Contains(side, " more") {
		t.Errorf("everything fits at 120x40; the rail should not be counting:\n%s", side)
	}
}

// A task waiting on the person is on the rail as one, and not as a
// tick: the demo seeds a scout that is done and whose report nobody
// has read, because that is the case a tick gets wrong.
func TestDemoRailSaysWhatWantsYou(t *testing.T) {
	var needs []tui.SideItem
	for _, it := range seed() {
		if it.Needs {
			needs = append(needs, it)
		}
	}
	if len(needs) == 0 {
		t.Fatal("nothing on the demo's rail is waiting on anybody")
	}
	var done bool
	for _, it := range needs {
		done = done || it.State == tui.Done
	}
	if !done {
		t.Error("the case worth showing is the one that is done and still wants you")
	}
	m, _ := screen(t, 120, 30)
	side := rail(view(m))
	if !strings.Contains(side, "◆") || !strings.Contains(side, "needs you") {
		t.Errorf("the rail does not say what wants you:\n%s", side)
	}
	if strings.Contains(side, "✓ "+needs[0].Title) {
		t.Errorf("a task that wants you was drawn as a tick:\n%s", side)
	}
}

// The card sentence is the harness's job: it has the call, the
// checkout and the words for both. The terminal only ever had JSON.
func TestDemoCallsSayWhatTheyAreDoing(t *testing.T) {
	for _, c := range []struct {
		call call
		want string
	}{
		{call{tool: "read", args: map[string]any{"path": "/src/mote/GAPS.md"}}, "read GAPS.md"},
		{call{tool: "run", args: map[string]any{"command": "git status --short"}}, "ran git status --short"},
		{call{tool: "write", args: map[string]any{"path": "/tmp/x/a.md"}}, "wrote /tmp/x/a.md"},
		{call{tool: "room", args: map[string]any{"action": "open", "what": "the mcp milestone"}},
			"open — the mcp milestone"},
	} {
		if got := says(c.call, "/src/mote"); got != c.want {
			t.Errorf("says(%s) = %q, want %q", c.call.tool, got, c.want)
		}
	}
	// Every call in the round has one, or the demo shows the fallback
	// where it meant to show the sentence.
	r := &round{repo: "/src/mote", scratch: "/tmp/scratch"}
	for _, c := range r.script() {
		if says(c, r.repo) == "" {
			t.Errorf("the %s call has nothing to say for itself", c.tool)
		}
	}
}

// What /registers prints names every one of them.
func TestDemoRegistersBlockNamesThemAll(t *testing.T) {
	block := registerBlock("/tmp/sessions")
	for _, want := range []string{"you", "reply", "tool", "event", "result", "error"} {
		if !strings.Contains(block, "| "+want+" |") {
			t.Errorf("/registers does not name the %s register:\n%s", want, block)
		}
	}
}

// The demo's notices are about tasks, so a task that changes four
// times keeps one line in the transcript.
func TestDemoNoticesAreAboutTasks(t *testing.T) {
	f := &fleet{items: seed()}
	out := make(chan agent.Event, 8)
	stop := make(chan struct{})
	go f.run(out, stop, time.Millisecond)
	ev := <-out
	close(stop)
	if ev.Kind != agent.KindNotice {
		t.Fatalf("kind %q", ev.Kind)
	}
	if ev.ID == "" {
		t.Errorf("the notice does not say which task it is about: %+v", ev)
	}
	if !strings.HasPrefix(ev.Text, ev.ID) {
		t.Errorf("notice %q is not about %q", ev.Text, ev.ID)
	}
}

// /new moves the conversation and the demo hears about it, which is
// what /sessions marks.
func TestDemoFollowsTheConversation(t *testing.T) {
	here := &current{id: "demo-1"}
	if here.get() != "demo-1" {
		t.Fatal(here.get())
	}
	here.set("demo-2")
	if here.get() != "demo-2" {
		t.Fatalf("the demo did not follow the terminal: %q", here.get())
	}
}
