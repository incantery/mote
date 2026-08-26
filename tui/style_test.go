package tui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/incantery/mote/agent"
	"github.com/muesli/termenv"
)

// The two questions a terminal is asked when somebody wants to know
// what colour it is: the background, and — as the sentinel for a
// terminal that ignores the first — where the cursor is.
const (
	queryBackground = "\x1b]11;?"
	queryCursor     = "\x1b[6n"
)

// deaf is a terminal that never answers. Everything written to it is
// kept; nothing ever comes back. termenv is told to believe it is a
// real one (WithUnsafe skips the ioctls a pipe cannot do), so any code
// that decides to ask it a question leaves the question in the buffer
// for the test to find — twelve bytes, exactly what a rook pane saw.
func deaf(t *testing.T) (*lipgloss.Renderer, func() string) {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu   sync.Mutex
		buf  bytes.Buffer
		done = make(chan struct{})
	)
	go func() {
		defer close(done)
		b, _ := io.ReadAll(pr)
		mu.Lock()
		buf.Write(b)
		mu.Unlock()
	}()
	written := sync.OnceValue(func() string {
		pw.Close()
		<-done
		pr.Close()
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	})
	t.Cleanup(func() { written() })
	return lipgloss.NewRenderer(pw, termenv.WithUnsafe(), termenv.WithProfile(termenv.ANSI)), written
}

// deaf has to have teeth: if this stops writing the query, the test
// below stops testing anything.
func TestDeafTerminalHearsAQuery(t *testing.T) {
	r, written := deaf(t)
	r.HasDarkBackground()
	if got := written(); !strings.Contains(got, queryBackground) {
		t.Fatalf("the fake terminal was not asked anything: %q", got)
	}
}

// Nothing the terminal draws may ask the terminal a question. The one
// that used to — glamour's "auto" style, resolved the first time a
// reply needed drawing — landed its answer in the input box on a
// terminal that replied, and cost five seconds of blank screen on one
// that did not.
func TestNoTerminalQueries(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "")
	t.Setenv("COLORFGBG", "")

	r, written := deaf(t)
	pal := DefaultPalette() // Markdown: "auto", the way an application gets it
	m := New(&agent.Fake{Instant: true}, Options{
		Name: "mote", Model: "fake-1", Conversation: "demo-1",
		Palette:  &pal,
		Renderer: r,
		Greeting: "**mote** — `/help` has the keys.",
	})
	// Every width builds another glamour renderer, which is where the
	// question used to be asked again.
	for _, w := range []int{80, 120, 200} {
		step(m, tea.WindowSizeMsg{Width: w, Height: 30})
		step(m, conversation()...)
		m.View()
	}

	if got := written(); strings.Contains(got, queryBackground) || strings.Contains(got, queryCursor) {
		t.Errorf("the terminal was asked something: %q", got)
	}
	// The buffer only sees what goes through the renderer; glamour
	// would have asked os.Stdout instead. That it cannot is the style
	// being a concrete one by the time glamour ever sees it.
	if m.md.style == "" || m.md.style == "auto" {
		t.Errorf("the markdown style is still %q — glamour will ask the terminal", m.md.style)
	}
}

func TestResolveStyle(t *testing.T) {
	for _, c := range []struct {
		name     string
		style    string
		profile  termenv.Profile
		colorFGB string
		glamour  string
		want     string
	}{
		{name: "a style said outright is kept", style: "dracula", profile: termenv.ANSI, want: "dracula"},
		{name: "light is kept", style: "light", profile: termenv.TrueColor, want: "light"},
		{name: "auto on a terminal is dark", style: "auto", profile: termenv.ANSI256, want: "dark"},
		{name: "unset is auto", style: "", profile: termenv.ANSI256, want: "dark"},
		{name: "auto with no colour at all is notty", style: "auto", profile: termenv.Ascii, want: "notty"},
		{name: "COLORFGBG says light", style: "auto", profile: termenv.ANSI, colorFGB: "0;15", want: "light"},
		{name: "COLORFGBG says dark", style: "auto", profile: termenv.ANSI, colorFGB: "15;0", want: "dark"},
		{name: "rxvt's three fields", style: "auto", profile: termenv.ANSI, colorFGB: "0;default;15", want: "light"},
		{name: "GLAMOUR_STYLE wins over the guess", style: "auto", profile: termenv.ANSI, glamour: "pink", want: "pink"},
		{name: "an explicit style wins over GLAMOUR_STYLE", style: "ascii", profile: termenv.ANSI, glamour: "pink", want: "ascii"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("COLORFGBG", c.colorFGB)
			t.Setenv("GLAMOUR_STYLE", c.glamour)
			if got := resolveStyle(c.style, c.profile); got != c.want {
				t.Errorf("resolveStyle(%q, %v) = %q, want %q", c.style, c.profile, got, c.want)
			}
		})
	}
}
