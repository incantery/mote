package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/incantery/mote/agent"
	"github.com/incantery/mote/session"
)

// watched wraps an agent and says when an exchange has finished, so a
// test can wait for one without sleeping and hoping.
type watched struct {
	inner agent.Agent
	done  chan struct{}
}

func (w *watched) Send(ctx context.Context, conversation, text string) (<-chan agent.Event, error) {
	ch, err := w.inner.Send(ctx, conversation, text)
	if err != nil {
		return nil, err
	}
	out := make(chan agent.Event)
	go func() {
		defer close(out)
		defer func() { close(w.done) }()
		for ev := range ch {
			out <- ev
		}
	}()
	return out, nil
}

// The whole thing, through a real bubbletea program: keys in, an
// exchange with an agent, a transcript out. No terminal required.
func TestProgramRunsAnExchange(t *testing.T) {
	pal := DefaultPalette()
	pal.Markdown = "ascii"
	dir := t.TempDir()
	sess, err := session.Open(dir, "test-1")
	if err != nil {
		t.Fatal(err)
	}
	w := &watched{inner: &agent.Fake{Instant: true}, done: make(chan struct{})}
	m := New(w, Options{
		Name:         "mote",
		Model:        "fake-1",
		Conversation: "test-1",
		Session:      sess,
		Palette:      &pal,
		Side:         func() []SideItem { return []SideItem{{ID: "x", Title: "a task", State: Working}} },
		SideTitle:    "fleet",
	})

	in, _ := io.Pipe() // nothing ever arrives; every key goes in by Send
	// The window is given rather than measured: nothing here is a
	// terminal, and a program that measured one would start at 0x0.
	p := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(io.Discard),
		tea.WithWindowSize(120, 30))

	go func() {
		for _, r := range "tell me about tools" {
			p.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
		p.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
		select {
		case <-w.done:
		case <-time.After(10 * time.Second):
			t.Error("the exchange never finished")
		}
		// The agent is finished; give the loop a moment to drain what
		// it queued, then stop it.
		time.Sleep(300 * time.Millisecond)
		p.Quit()
	}()

	final, err := p.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := final.(*Model)
	if got.inflight {
		t.Error("still in flight after the exchange ended")
	}
	text := got.transcript()
	for _, want := range []string{
		"tell me about tools", // the question
		"read_file",           // a tool card
		"task 184a1100",       // the notice that arrived mid-exchange
		"first milestone",     // the reply
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the transcript is missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(view(got), "fleet") {
		t.Error("the rail is not on screen")
	}

	// And it is on disk: the same transcript, from a second process's
	// point of view.
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := session.Open(dir, "test-1")
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if n := len(again.Turns()); n != 1 {
		t.Fatalf("%d turns on disk", n)
	}
	if h := again.History(); len(h) != 1 || h[0] != "tell me about tools" {
		t.Fatalf("history %q", h)
	}
	reopened := New(&agent.Fake{Instant: true}, Options{
		Name: "mote", Model: "fake-1", Conversation: "test-1", Session: again,
		Palette:   &pal,
		Side:      func() []SideItem { return []SideItem{{ID: "x", Title: "a task", State: Working}} },
		SideTitle: "fleet",
	})
	reopened.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if a, b := reopened.transcript(), text; a != b {
		t.Errorf("the reopened transcript differs.\n--- reopened ---\n%s\n--- was ---\n%s", a, b)
	}
}
