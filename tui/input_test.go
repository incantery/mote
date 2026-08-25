package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func box(t *testing.T) *Model {
	t.Helper()
	return plain(t, 100, 30, Options{
		Name: "mote",
		Commands: []Command{
			{Name: "report", Help: "what a task wrote"},
			{Name: "resume", Help: "pick a task back up"},
			{Name: "tasks", Help: "the fleet"},
		},
		Handle: func(string, string) tea.Cmd { return nil },
	})
}

// enter sends; the newline is the deliberate keystroke.
func TestInputMultiline(t *testing.T) {
	m := box(t)
	typeIn(m, "first")
	step(m, kmsg("alt+enter"))
	typeIn(m, "second")
	if got := m.in.value(); got != "first\nsecond" {
		t.Fatalf("value %q", got)
	}
	if m.in.height() != 2 {
		t.Fatalf("the box should have grown to 2 lines, got %d", m.in.height())
	}
	step(m, kmsg("enter"))
	if m.in.value() != "" {
		t.Fatalf("enter should have sent and cleared, left %q", m.in.value())
	}
	last := m.entries[len(m.entries)-1]
	if last.kind != entryUser || last.text != "first\nsecond" {
		t.Fatalf("the transcript got %q", last.text)
	}
	if m.in.height() != 1 {
		t.Fatal("the box should have shrunk back")
	}
	// The line is on screen before the agent has said anything.
	if !strings.Contains(m.transcript(), "first") {
		t.Fatalf("the question is not in the transcript yet:\n%s", m.transcript())
	}
}

// The box only grows so far, or it eats the transcript.
func TestInputHeightIsCapped(t *testing.T) {
	m := box(t)
	for range 20 {
		step(m, kmsg("alt+enter"))
	}
	if h := m.in.height(); h != inputHeight {
		t.Fatalf("height %d, want the cap %d", h, inputHeight)
	}
}

func TestInputHistory(t *testing.T) {
	m := box(t)
	for _, s := range []string{"first thing", "second thing"} {
		typeIn(m, s)
		step(m, kmsg("enter"))
	}

	step(m, kmsg("up"))
	if got := m.in.value(); got != "second thing" {
		t.Fatalf("one up gave %q", got)
	}
	step(m, kmsg("up"))
	if got := m.in.value(); got != "first thing" {
		t.Fatalf("two up gave %q", got)
	}
	step(m, kmsg("up")) // already at the oldest
	if got := m.in.value(); got != "first thing" {
		t.Fatalf("past the oldest gave %q", got)
	}
	step(m, kmsg("down"), kmsg("down"))
	if got := m.in.value(); got != "" {
		t.Fatalf("back past the newest should be empty, got %q", got)
	}
}

// A typed line is not a history entry, so the arrows belong to it.
func TestInputHistoryLeavesTypingAlone(t *testing.T) {
	m := box(t)
	typeIn(m, "old")
	step(m, kmsg("enter"))
	typeIn(m, "half written")
	step(m, kmsg("up"))
	if got := m.in.value(); got != "half written" {
		t.Fatalf("up while typing changed the text to %q", got)
	}
}

// Sending the same thing twice does not put it in the history twice.
func TestInputHistoryDedupes(t *testing.T) {
	m := box(t)
	for range 2 {
		typeIn(m, "again")
		step(m, kmsg("enter"))
	}
	if n := len(m.in.history); n != 1 {
		t.Fatalf("history has %d entries, want 1", n)
	}
}

func TestSlashCompletion(t *testing.T) {
	m := box(t)
	typeIn(m, "/")
	if n := len(m.in.sugg); n != 4 { // three of the app's, plus /help
		t.Fatalf("a bare slash offered %d commands, want 4", n)
	}
	typeIn(m, "re")
	got := []string{}
	for _, c := range m.in.sugg {
		got = append(got, c.Name)
	}
	if strings.Join(got, ",") != "report,resume" {
		t.Fatalf("/re offered %v", got)
	}
	if !strings.Contains(strings.Join(m.renderSuggestions(100), "\n"), "what a task wrote") {
		t.Fatal("the help text is not on screen")
	}

	// The arrows walk the list while it is open, not the history.
	step(m, kmsg("down"))
	if m.in.sel != 1 {
		t.Fatalf("selection %d, want 1", m.in.sel)
	}
	step(m, kmsg("tab"))
	if got := m.in.value(); got != "/resume " {
		t.Fatalf("tab completed to %q", got)
	}
	if m.in.completing() {
		t.Fatal("the list should have closed after accepting")
	}
}

// Once there are arguments there is nothing left to complete.
func TestSlashCompletionStopsAtArguments(t *testing.T) {
	m := box(t)
	typeIn(m, "/report 184a")
	if m.in.completing() {
		t.Fatal("still completing after an argument")
	}
}

// enter on an open list accepts rather than sends: the first enter
// finishes the word, the second runs it.
func TestSlashCompletionEnterAccepts(t *testing.T) {
	m := box(t)
	typeIn(m, "/tas")
	step(m, kmsg("enter"))
	if got := m.in.value(); got != "/tasks " {
		t.Fatalf("enter gave %q", got)
	}
	if n := len(m.entries); n != 0 {
		t.Fatalf("enter should not have sent anything, transcript has %d", n)
	}
}

func TestSlashCommandDispatch(t *testing.T) {
	var gotName, gotArgs string
	m := plain(t, 100, 30, Options{
		Name:     "mote",
		Commands: []Command{{Name: "report", Help: "a report"}},
		Handle: func(name, args string) tea.Cmd {
			gotName, gotArgs = name, args
			return nil
		},
	})
	typeIn(m, "/report 184a1100 please")
	step(m, kmsg("enter"))
	if gotName != "report" || gotArgs != "184a1100 please" {
		t.Fatalf("Handle got (%q, %q)", gotName, gotArgs)
	}
	if n := len(m.entries); n != 0 {
		t.Fatalf("a command is not a turn; transcript has %d entries", n)
	}
}

// /help is the terminal's own when the application does not claim it.
func TestBuiltinHelp(t *testing.T) {
	m := box(t)
	typeIn(m, "/help")
	step(m, kmsg("enter"))
	last := m.entries[len(m.entries)-1]
	if last.kind != entryBlock || !strings.Contains(last.text, "/report") {
		t.Fatalf("built-in help was %q", last.text)
	}
}

func TestUnknownCommandWithoutHandler(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	typeIn(m, "/nope")
	step(m, kmsg("enter"))
	last := m.entries[len(m.entries)-1]
	if last.kind != entryError || !strings.Contains(last.text, "/nope") {
		t.Fatalf("got %v %q", last.kind, last.text)
	}
}
