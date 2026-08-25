package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/incantery/mote/agent"
	"github.com/muesli/termenv"
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
	w := &watched{inner: &agent.Fake{Instant: true}, done: make(chan struct{})}
	m := New(w, Options{
		Name:         "mote",
		Model:        "fake-1",
		Conversation: "test-1",
		Palette:      &pal,
		Renderer:     lipgloss.NewRenderer(io.Discard, termenv.WithProfile(termenv.Ascii)),
		Side:         func() []SideItem { return []SideItem{{ID: "x", Title: "a task", State: Working}} },
		SideTitle:    "fleet",
	})

	in, _ := io.Pipe() // nothing ever arrives; every key goes in by Send
	p := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(io.Discard))

	go func() {
		p.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
		for _, r := range "tell me about tools" {
			p.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		p.Send(tea.KeyMsg{Type: tea.KeyEnter})
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
	if !strings.Contains(got.View(), "fleet") {
		t.Error("the rail is not on screen")
	}
}
