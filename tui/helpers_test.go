package tui

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/incantery/mote/agent"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// plain builds a model whose markdown is glamour's ascii style, so
// that nothing in the transcript is coloured by a style config. The
// palette still paints — in lipgloss v2 a style always writes its
// colour and bubbletea downsamples on the way out — so what the golden
// files hold is the frame with the escapes taken off. What is left is
// the layout, which is what they are for.
func plain(t *testing.T, w, h int, opts Options) *Model {
	t.Helper()
	pal := DefaultPalette()
	pal.Markdown = "ascii"
	opts.Palette = &pal
	m := New(&agent.Fake{Instant: true}, opts)
	step(m, tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

// view is the frame's content, which is what a person sees and what
// the tests measure.
func view(m *Model) string { return m.View().Content }

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

// kmsg turns a name into the key press bubbletea would deliver. A key
// in v2 is a code and a set of modifiers, and its name is what
// String() makes of them — so the names here are exactly the ones the
// terminal switches on.
func kmsg(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "alt+enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+o":
		return tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}
	case "ctrl+t":
		return tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}
	case "ctrl+l":
		return tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

// typeIn feeds a string a rune at a time, the way a person does.
func typeIn(m *Model, s string) {
	for _, r := range s {
		step(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// golden compares against testdata, or rewrites it under -update. The
// escapes come off first: the golden files are the layout, and colour
// is the terminal's business.
func golden(t *testing.T, name, got string) {
	t.Helper()
	got = ansi.Strip(got)
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

// waitFor spins until cond holds, for the few things a terminal does
// off its own goroutine.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for range 200 {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out")
}
