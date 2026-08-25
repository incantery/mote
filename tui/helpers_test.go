package tui

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/incantery/mote/agent"
	"github.com/muesli/termenv"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// plain builds a model that paints no colour at all: an Ascii renderer
// and glamour's ascii style. What is left is the layout, which is what
// the golden files are for.
func plain(t *testing.T, w, h int, opts Options) *Model {
	t.Helper()
	pal := DefaultPalette()
	pal.Markdown = "ascii"
	opts.Palette = &pal
	opts.Renderer = lipgloss.NewRenderer(io.Discard, termenv.WithProfile(termenv.Ascii))
	m := New(&agent.Fake{Instant: true}, opts)
	step(m, tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

// step runs messages through Update, dropping the commands: nothing in
// these tests needs a running program, only the model's reaction.
func step(m *Model, msgs ...tea.Msg) {
	for _, msg := range msgs {
		m.Update(msg)
	}
}

func events(evs ...agent.Event) []tea.Msg {
	out := make([]tea.Msg, 0, len(evs))
	for _, e := range evs {
		out = append(out, eventMsg{e})
	}
	return out
}

func keys(ss ...string) []tea.Msg {
	out := make([]tea.Msg, 0, len(ss))
	for _, s := range ss {
		out = append(out, kmsg(s))
	}
	return out
}

// kmsg turns a name into the KeyMsg bubbletea would deliver.
func kmsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "alt+enter":
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+o":
		return tea.KeyMsg{Type: tea.KeyCtrlO}
	case "ctrl+t":
		return tea.KeyMsg{Type: tea.KeyCtrlT}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// typeIn feeds a string a rune at a time, the way a person does.
func typeIn(m *Model, s string) {
	for _, r := range s {
		step(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// golden compares against testdata, or rewrites it under -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run: go test ./tui -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the golden file.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func lipglossWidth(s string) int { return lipgloss.Width(s) }
