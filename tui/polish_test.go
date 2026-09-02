package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/incantery/mote/agent"
)

// These are the parts of the chat's design reference that were the
// terminal's to give rather than the application's: the notice that
// can say what kind it is, the notice that can be opened, the status
// line the application lays out, the popup with its own keys, and
// the wrap that keeps a slash command whole.

// A notice's tone is a colour on its gutter and nothing else: with the
// escapes off, an ask and a failure read exactly like a plain notice.
func TestANoticeTintsOnlyItsGutter(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	step(m, events(
		agent.About("a", "Task · ship it"),
		agent.About("b", "Task · ship it").WithTone(agent.ToneNeeds),
		agent.About("c", "Task · ship it").WithTone(agent.ToneFailed),
	)...)
	raw := make([]string, 3)
	for i, e := range m.entries[len(m.entries)-3:] {
		raw[i] = m.renderEntry(e, 80, false)
		if !strings.HasPrefix(ansi.Strip(raw[i]), "  ▏ Task") {
			t.Errorf("notice %d has no gutter: %q", i, ansi.Strip(raw[i]))
		}
	}
	gutter := func(s string) string { return s[:strings.Index(s, "▏")] }
	if gutter(raw[0]) == gutter(raw[1]) || gutter(raw[0]) == gutter(raw[2]) || gutter(raw[1]) == gutter(raw[2]) {
		t.Errorf("the three tones paint the same gutter:\n%q\n%q\n%q", gutter(raw[0]), gutter(raw[1]), gutter(raw[2]))
	}
	// Same words, same colour on them, whatever the gutter says.
	for i := 1; i < 3; i++ {
		if after(raw[i], "▏") != after(raw[0], "▏") {
			t.Errorf("notice %d colours its words, not just its gutter:\n%q\n%q", i, after(raw[0], "▏"), after(raw[i], "▏"))
		}
	}
}

func after(s, mark string) string { return s[strings.Index(s, mark)+len(mark):] }

// sameStyle says two rendered strings open with the same escape
// sequence — the same style, whatever the text.
func sameStyle(a, b string) bool {
	esc := func(s string) string {
		if i := strings.Index(s, "m"); strings.HasPrefix(s, "\x1b[") && i > 0 {
			return s[:i+1]
		}
		return ""
	}
	return esc(a) == esc(b)
}

// A notice with something to open is a stop for tab, and enter over an
// empty box runs it. With something typed, enter sends that instead.
func TestAFocusedNoticeOpensWithEnter(t *testing.T) {
	var ran []string
	m := plain(t, 100, 30, Options{Name: "mote",
		Commands: []Command{{Name: "report", Help: "read a report"}},
		Handle: func(name, args string) tea.Cmd {
			ran = append(ran, name+" "+args)
			return nil
		},
	})
	step(m, events(
		agent.About("a", "◐ Task working · ship it"),
		agent.About("b", "✓ Scout reported · why is it slow ●").WithOpen("/report b"),
	)...)
	step(m, kmsg("tab"))
	e := m.focused()
	if e == nil || e.kind != entryNotice || e.open != "/report b" {
		t.Fatalf("tab did not land on the openable notice: focus %d", m.focus)
	}
	if !strings.Contains(m.hints(), "enter open") {
		t.Errorf("the hints do not say what enter does: %q", m.hints())
	}
	if !strings.HasPrefix(ansi.Strip(m.renderEntry(e, 80, true)), "  ▏ ") || sameStyle(m.renderEntry(e, 80, true), m.renderEntry(e, 80, false)) {
		t.Error("a focused notice wears the same gutter as an unfocused one")
	}
	// The one without a command is not a stop.
	step(m, kmsg("shift+tab"))
	if m.focus >= 0 {
		t.Errorf("shift+tab landed on a notice with nothing to open: %d", m.focus)
	}
	step(m, kmsg("tab"), kmsg("enter"))
	if len(ran) != 1 || ran[0] != "report b" {
		t.Fatalf("enter ran %v, want [report b]", ran)
	}
	if m.focus >= 0 {
		t.Error("the focus stayed after the command ran")
	}
	// With text in the box, enter sends the text and leaves the notice alone.
	step(m, kmsg("tab"))
	typeIn(m, "hello")
	step(m, kmsg("enter"))
	if len(ran) != 1 {
		t.Fatalf("enter over a typed message ran a command: %v", ran)
	}
	if last := m.entries[len(m.entries)-1]; last.kind != entryUser && last.kind != entryReply {
		t.Errorf("the message was not sent: %v", last.kind)
	}
}

// ctrl+o is the card's key, and a notice is not a card: it has nothing
// to unfold, so the key does nothing there rather than something odd.
func TestCtrlOLeavesANoticeAlone(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	step(m, events(agent.About("b", "✓ done").WithOpen("/report b"))...)
	step(m, kmsg("tab"), kmsg("ctrl+o"))
	if m.focused() == nil || m.focused().expanded {
		t.Error("ctrl+o expanded a notice")
	}
}

// The application can lay the status line out itself, and then the
// terminal's own facts are only there if it put them there.
func TestTheApplicationLaysOutTheStatusLine(t *testing.T) {
	var seen Status
	m := plain(t, 200, 30, Options{Name: "mote", Model: "fake-1", Conversation: "demo-1",
		StatusRight: func() string { return "3 tasks" },
		Status: func(s Status) (string, string) {
			seen = s
			return s.Model, FormatCost(s.Cost) + " est · ctx " + FormatTokens(s.InputTokens) + " · " + s.Right
		},
	})
	m.total, m.totalIn = 0.0094, 40800
	line := ansi.Strip(m.statusLine())
	if !strings.HasPrefix(line, "fake-1") {
		t.Errorf("the left is not the application's: %q", line)
	}
	if strings.Contains(line, "mote") || strings.Contains(line, "demo-1") {
		t.Errorf("the terminal put its own facts on a line the application laid out: %q", line)
	}
	if !strings.HasSuffix(strings.TrimSpace(line), "$0.0094 est · ctx 40.8k · 3 tasks") {
		t.Errorf("the right is not the application's: %q", line)
	}
	if seen.Name != "mote" || seen.Conversation != "demo-1" || seen.Spent != "$0.0094 · 40.8k tok" {
		t.Errorf("the application was not told what the terminal knows: %+v", seen)
	}
	// The hints still go in front of the right when there is room.
	if !strings.Contains(line, "enter send") {
		t.Errorf("the hints are gone: %q", line)
	}
}

// The completion popup is a box of its own with its own keys, and the
// chosen command is a block rather than a colour.
func TestTheCompletionPopupIsABoxWithItsOwnKeys(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote", Commands: []Command{
		{Name: "report", Help: "what a task wrote"},
		{Name: "resume", Help: "pick a task back up"},
	}})
	typeIn(m, "/re")
	lines := m.renderSuggestions(100)
	if len(lines) != 5 {
		t.Fatalf("%d lines, want two commands, the keys and a border:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	flat := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.HasPrefix(flat, "╭") || !strings.Contains(flat, suggestHint) {
		t.Errorf("the popup is not a box with its keys in it:\n%s", flat)
	}
	if !strings.Contains(lines[1], "\x1b[7") && !strings.Contains(lines[1], ";7m") && !strings.Contains(lines[1], ";7;") {
		t.Errorf("the chosen row is not reversed: %q", lines[1])
	}
	// The editor under it is the one in the accent; the popup is dim.
	screen := ansi.Strip(view(m))
	if strings.Count(screen, "╭") != 2 {
		t.Errorf("want the popup and the editor both boxed:\n%s", screen)
	}
	if sameStyle(lines[0], m.renderInput()) {
		t.Error("the popup and the focused editor wear the same border")
	}
}

// The editor's border says where the keyboard is: the accent while it
// is in the box, dim when a card has it or a question does.
func TestTheEditorsBorderFollowsTheFocus(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	focused := m.renderInput()
	step(m, events(agent.About("b", "✓ done").WithOpen("/report b"))...)
	step(m, kmsg("tab"))
	if sameStyle(focused, m.renderInput()) {
		t.Error("the border did not dim when a notice took the focus")
	}
	step(m, kmsg("esc"))
	if !sameStyle(focused, m.renderInput()) {
		t.Error("the border did not come back when the focus did")
	}
}

// A slash command is the one token on a notice that must survive a
// wrap whole: it never breaks at its own slash.
func TestASlashCommandNeverBreaksAtItsSlash(t *testing.T) {
	m := plain(t, 100, 30, Options{Name: "mote"})
	text := "◇ Scout needs you · Investigate Vera's /effort handling\n" +
		"Luna has no reasoning-effort control at all · /answer a3f2 <text>"
	for w := 30; w < 70; w++ {
		out := ansi.Strip(hang(m.st.event, "  ▏ ", "  ▏ ", text, w))
		for _, l := range strings.Split(out, "\n") {
			if strings.HasSuffix(strings.TrimRight(l, " "), "/") {
				t.Fatalf("at %d columns a line ends in the slash of a command:\n%s", w, out)
			}
		}
	}
	// A path in prose still breaks at a slash, as it always did.
	out := ansi.Strip(hang(m.st.event, "  ", "  ", "see github.com/incantery/mote/tui/transcript.go for the wrap", 30))
	if !strings.Contains(out, "/\n") {
		t.Errorf("a path lost its breakpoints:\n%s", out)
	}
}
