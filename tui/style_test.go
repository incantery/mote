package tui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/incantery/mote/agent"
)

// The two questions a terminal is asked when somebody wants to know
// what colour it is: the background, and — as the sentinel a blocking
// asker waits on when the first is ignored — the cursor position, or
// the device attributes.
const (
	queryBackground = "\x1b]11;?"
	queryCursor     = "\x1b[6n"
	queryAttributes = "\x1b[c"
)

// The two answers a terminal can give, as colours.
var (
	black     = lipgloss.Color("#000000")
	lightGrey = lipgloss.Color("#eeeeee")
)

// deaf is a terminal that never answers. Reading from it blocks until
// the test is over; everything written to it is kept for the test to
// read back. It is what a rook pane looked like from inside: the
// question goes out and nothing ever comes back.
type deafTTY struct {
	in      *os.File // the program's input: nothing is ever written to it
	out     *os.File // the program's output
	written func() string
	frame   chan struct{} // closed when anything is drawn
}

func deaf(t *testing.T) *deafTTY {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	d := &deafTTY{in: inR, out: outW, frame: make(chan struct{})}
	var (
		mu   sync.Mutex
		buf  bytes.Buffer
		done = make(chan struct{})
		once sync.Once
	)
	go func() {
		defer close(done)
		b := make([]byte, 4096)
		for {
			n, err := outR.Read(b)
			if n > 0 {
				mu.Lock()
				buf.Write(b[:n])
				drawn := strings.Contains(buf.String(), "mote")
				mu.Unlock()
				if drawn {
					once.Do(func() { close(d.frame) })
				}
			}
			if err != nil {
				return
			}
		}
	}()
	d.written = sync.OnceValue(func() string {
		outW.Close()
		<-done
		outR.Close()
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	})
	t.Cleanup(func() {
		d.written()
		inW.Close()
		inR.Close()
	})
	return d
}

// deaf has to have teeth: if it stopped keeping what is written to it,
// or started answering, the tests below would stop testing anything.
func TestDeafTerminalKeepsTheQuestionAndAnswersNothing(t *testing.T) {
	d := deaf(t)
	if _, err := io.WriteString(d.out, queryBackground+"\x07"); err != nil {
		t.Fatal(err)
	}
	if err := d.in.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var b [16]byte
	if n, err := d.in.Read(b[:]); err == nil {
		t.Fatalf("the fake terminal answered %q", b[:n])
	}
	if got := d.written(); !strings.Contains(got, queryBackground) {
		t.Fatalf("the fake terminal did not keep the question: %q", got)
	}
}

// The point of the whole move. On a terminal that never answers, the
// first frame is drawn now — not after somebody's timeout — because
// nothing in the program is waiting for an answer. The background is
// requested, bubbletea writes the query and carries on, and if a
// reply ever lands it is a message like any other.
//
// It used to cost five seconds of blank screen: bubbletea v1 asked in
// its own package init, before main, and termenv waited.
func TestFirstFrameDoesNotWaitForTheTerminal(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "")
	t.Setenv("COLORFGBG", "")
	d := deaf(t)

	pal := DefaultPalette() // Markdown "auto": the terminal decides, or would
	m := New(&agent.Fake{Instant: true}, Options{
		Name: "mote", Model: "fake-1", Conversation: "demo-1",
		Palette:  &pal,
		Greeting: "**mote** — `/help` has the keys.",
	})
	p := tea.NewProgram(m,
		tea.WithInput(d.in),
		tea.WithOutput(d.out),
		tea.WithEnvironment([]string{"TERM=xterm-256color"}),
		tea.WithColorProfile(colorprofile.ANSI),
		tea.WithWindowSize(100, 30),
	)

	start := time.Now()
	drawn := make(chan time.Duration, 1)
	go func() {
		select {
		case <-d.frame:
			drawn <- time.Since(start)
		case <-time.After(10 * time.Second):
		}
		// A moment before quitting: bubbletea buffers what it writes,
		// and the question it wrote for us has to reach the wire for
		// the test to be able to count it.
		time.Sleep(250 * time.Millisecond)
		p.Quit()
	}()
	if _, err := p.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var took time.Duration
	select {
	case took = <-drawn:
	default:
		t.Fatal("nothing was ever drawn")
	}
	// A blocking asker costs two seconds in lipgloss and five in
	// termenv. A frame that waited on nothing takes milliseconds.
	if took > time.Second {
		t.Errorf("the first frame took %s — something waited for an answer", took)
	}

	got := d.written()
	// Ours asks nothing on its own account. The one question on the
	// wire is bubbletea's, written for the RequestBackgroundColor this
	// terminal asked for in Init and never waited on.
	if n := strings.Count(got, queryBackground); n != 1 {
		t.Errorf("%d background queries on the wire, want the one bubbletea writes", n)
	}
	for _, q := range []string{queryCursor, queryAttributes} {
		if strings.Contains(got, q) {
			t.Errorf("something is waiting for an answer: %q is on the wire", q)
		}
	}
	// And nothing answered, so the style is the one New guessed.
	if m.md.style != "dark" {
		t.Errorf("the markdown style is %q; nothing answered, so it should still be the default", m.md.style)
	}
	if m.md.style == autoStyle {
		t.Error("the style is still auto — glamour would have to decide it, and glamour v2 will not")
	}
}

// The answer, when it comes, lands as a message and moves exactly one
// thing: which glamour style the transcript is drawn with. What is
// already on screen is drawn again, so a conversation does not end up
// half in one style and half in the other.
func TestBackgroundColourAnswerRestyles(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "")
	t.Setenv("COLORFGBG", "")
	m := New(&agent.Fake{Instant: true}, Options{Name: "mote", Greeting: "# hello"})
	step(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if m.md.style != "dark" {
		t.Fatalf("the safe default is dark, got %q", m.md.style)
	}
	before := m.transcript()
	drawn := m.entries[0].cache

	step(m, tea.BackgroundColorMsg{Color: lightGrey})
	if m.md.style != "light" {
		t.Fatalf("a light terminal should have moved the style, got %q", m.md.style)
	}
	if m.entries[0].cache == drawn {
		t.Error("the greeting was kept, so what is on screen is still in the old style")
	}
	if after := m.transcript(); after == before {
		t.Error("the style moved and nothing was drawn again")
	}

	// Back the other way, and the entry cache does not lie about it.
	step(m, tea.BackgroundColorMsg{Color: black})
	if m.md.style != "dark" {
		t.Fatalf("style %q", m.md.style)
	}
	if got := m.transcript(); got != before {
		t.Errorf("back on a dark terminal the transcript differs:\n%s\n--- was ---\n%s", got, before)
	}
}

// A style said outright is the person's word, and an answer from the
// terminal does not overrule it. This is `mote demo -light` on a dark
// terminal, which is a thing people do.
func TestAnExplicitStyleWinsOverTheTerminal(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "")
	t.Setenv("COLORFGBG", "")
	pal := DefaultPalette()
	pal.Markdown = "light"
	m := New(&agent.Fake{Instant: true}, Options{Name: "mote", Palette: &pal})
	step(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	step(m, tea.BackgroundColorMsg{Color: black}, tea.ColorProfileMsg{Profile: colorprofile.NoTTY})
	if m.md.style != "light" {
		t.Errorf("the terminal overruled the person: style %q", m.md.style)
	}
}

// Nothing to paint with at all — output to a file, or TERM=dumb — is
// glamour's notty style, and bubbletea is the one who knows.
func TestNoColourAtAllIsNotty(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "")
	t.Setenv("COLORFGBG", "")
	m := New(&agent.Fake{Instant: true}, Options{Name: "mote"})
	step(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	step(m, tea.ColorProfileMsg{Profile: colorprofile.NoTTY})
	if m.md.style != "notty" {
		t.Errorf("style %q, want notty", m.md.style)
	}
}

func TestResolveStyle(t *testing.T) {
	for _, c := range []struct {
		name     string
		style    string
		noColor  bool
		dark     bool
		colorFGB string
		glamour  string
		want     string
	}{
		{name: "a style said outright is kept", style: "dracula", dark: true, want: "dracula"},
		{name: "light is kept", style: "light", dark: true, want: "light"},
		{name: "auto on a dark terminal is dark", style: "auto", dark: true, want: "dark"},
		{name: "auto on a light terminal is light", style: "auto", want: "light"},
		{name: "unset is auto", style: "", dark: true, want: "dark"},
		{name: "auto with no colour at all is notty", style: "auto", noColor: true, dark: true, want: "notty"},
		{name: "GLAMOUR_STYLE wins over the guess", style: "auto", dark: true, glamour: "pink", want: "pink"},
		{name: "an explicit style wins over GLAMOUR_STYLE", style: "ascii", dark: true, glamour: "pink", want: "ascii"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("GLAMOUR_STYLE", c.glamour)
			if got := resolveStyle(c.style, c.noColor, c.dark); got != c.want {
				t.Errorf("resolveStyle(%q, %v, %v) = %q, want %q", c.style, c.noColor, c.dark, got, c.want)
			}
		})
	}
}

// What the terminal says about itself in the environment, for the
// terminals that say it — read before the first frame, and the only
// reason the default is ever anything but dark.
func TestDarkDefaultReadsCOLORFGBG(t *testing.T) {
	for _, c := range []struct {
		colorFGBG string
		want      bool
	}{
		{"", true},
		{"15;0", true},
		{"0;15", false},
		{"0;default;15", false}, // rxvt writes three fields
		{"nonsense", true},
	} {
		t.Setenv("COLORFGBG", c.colorFGBG)
		if got := darkDefault(); got != c.want {
			t.Errorf("COLORFGBG=%q: darkDefault() = %v, want %v", c.colorFGBG, got, c.want)
		}
	}
}
