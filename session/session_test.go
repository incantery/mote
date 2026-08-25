package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/incantery/mote/agent"
)

func at(min int) time.Time {
	return time.Date(2026, 8, 25, 19, min, 0, 0, time.UTC)
}

func scene() []Turn {
	return []Turn{{
		At:    at(1),
		Ended: at(2),
		Said:  "tell me about tools",
		Events: []agent.Event{
			agent.Delta("Looking.\n"),
			agent.Call("c1", "read_file", `{"path":"README.md"}`),
			agent.Result("c1", "# mote\nline two", 1420*time.Millisecond, 0.0021),
			agent.Notice("task 184a1100 finished"),
			agent.Delta("Read it. The first milestone is the terminal.\n"),
			agent.Fail("upstream: 429"),
		},
		Cost:         0.0063,
		InputTokens:  1200,
		OutputTokens: 310,
	}, {
		At:    at(3),
		Ended: at(4),
		Said:  "run the tests",
		Events: []agent.Event{
			agent.Call("c2", "shell", `{"cmd":"go test ./..."}`),
			agent.Output("c2", "ok\tmote/agent\nok\tmote/tui\n"),
			agent.Result("c2", "", 9*time.Second, 0.0007),
			agent.Delta("Green.\n"),
		},
		Cost: 0.0007,
	}}
}

// Everything written comes back, in order, through a closed file and
// a second Open.
func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "demo-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Turns(); len(got) != 0 {
		t.Fatalf("a new conversation has %d turns", len(got))
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Fatal("opening a conversation nobody said anything to left a file behind")
	}

	want := scene()
	for _, turn := range want {
		if err := s.Remember(turn.Said); err != nil {
			t.Fatal(err)
		}
		if err := s.Append(turn); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Remember("/tasks"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := Open(dir, "demo-1")
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	got := again.Turns()
	if len(got) != len(want) {
		t.Fatalf("read back %d turns, want %d", len(got), len(want))
	}
	for i := range want {
		if !equalTurn(got[i], want[i]) {
			t.Errorf("turn %d differs:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
	wantHistory := []string{"tell me about tools", "run the tests", "/tasks"}
	if h := again.History(); !equalStrings(h, wantHistory) {
		t.Errorf("history %q, want %q", h, wantHistory)
	}
}

// Appending to a reopened session keeps what was already there.
func TestAppendAfterReopen(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, "c")
	if err := s.Append(scene()[0]); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, _ := Open(dir, "c")
	if err := s2.Append(scene()[1]); err != nil {
		t.Fatal(err)
	}
	if n := len(s2.Turns()); n != 2 {
		t.Fatalf("in memory: %d turns", n)
	}
	s2.Close()

	s3, _ := Open(dir, "c")
	defer s3.Close()
	if n := len(s3.Turns()); n != 2 {
		t.Fatalf("on disk: %d turns", n)
	}
}

// The file is what the package comment says it is: one header, then a
// line per input and per turn, each one JSON on its own line.
func TestFileFormat(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, "demo-1")
	s.Remember("hello")
	s.Append(scene()[0])
	s.Close()

	b, err := os.ReadFile(filepath.Join(dir, "demo-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("%d lines, want header + input + turn:\n%s", len(lines), b)
	}
	var head struct {
		Type         string `json:"type"`
		Version      int    `json:"v"`
		Conversation string `json:"conversation"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &head); err != nil {
		t.Fatal(err)
	}
	if head.Type != "open" || head.Version != Version || head.Conversation != "demo-1" {
		t.Fatalf("header %+v", head)
	}
	for i, want := range []string{"input", "turn"} {
		var l struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(lines[i+1]), &l); err != nil {
			t.Fatal(err)
		}
		if l.Type != want {
			t.Fatalf("line %d is %q, want %q", i+1, l.Type, want)
		}
	}
	// Reopening does not write a second header.
	s2, _ := Open(dir, "demo-1")
	s2.Remember("again")
	s2.Close()
	b2, _ := os.ReadFile(filepath.Join(dir, "demo-1.jsonl"))
	if n := strings.Count(string(b2), `"type":"open"`); n != 1 {
		t.Fatalf("%d headers", n)
	}
}

// A half-written last line must not hide the rest of the file.
func TestTornLineIsSkipped(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, "c")
	s.Remember("first")
	s.Append(scene()[0])
	s.Close()

	f, err := os.OpenFile(filepath.Join(dir, "c.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"turn","said":"the power went o`)
	f.Close()

	again, err := Open(dir, "c")
	if err != nil {
		t.Fatalf("a torn line broke the read: %v", err)
	}
	defer again.Close()
	if n := len(again.Turns()); n != 1 {
		t.Fatalf("%d turns, want the one whole line", n)
	}
	if h := again.History(); len(h) != 1 || h[0] != "first" {
		t.Fatalf("history %q", h)
	}
	// And the file still takes appends after the torn line.
	if err := again.Append(scene()[1]); err != nil {
		t.Fatal(err)
	}
}

// A repeated line is not remembered twice, in memory or on disk.
func TestHistorySkipsRepeats(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, "c")
	for _, line := range []string{"a", "a", "b", "a", "  ", ""} {
		s.Remember(line)
	}
	s.Close()
	s2, _ := Open(dir, "c")
	defer s2.Close()
	if h := s2.History(); !equalStrings(h, []string{"a", "b", "a"}) {
		t.Fatalf("history %q", h)
	}
}

// An id is a file name, whatever the caller thought it was.
func TestPathSanitisesTheID(t *testing.T) {
	for id, want := range map[string]string{
		"demo-1":       "demo-1.jsonl",
		"":             "stateless.jsonl",
		"../../etc/pw": ".._.._etc_pw.jsonl",
		"a b/c":        "a_b_c.jsonl",
	} {
		if got := Path("/d", id); got != filepath.Join("/d", want) {
			t.Errorf("Path(%q) = %q, want .../%s", id, got, want)
		}
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	if got, err := List(dir); err != nil || len(got) != 0 {
		t.Fatalf("an empty directory listed %v, %v", got, err)
	}
	if got, err := List(filepath.Join(dir, "nope")); err != nil || got != nil {
		t.Fatalf("a missing directory listed %v, %v", got, err)
	}

	older, _ := Open(dir, "older")
	older.Append(scene()[0])
	older.Close()
	newer, _ := Open(dir, "newer")
	for _, turn := range scene() {
		newer.Append(turn)
	}
	newer.Close()
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a session"), 0o600)

	got, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("listed %d, want 2: %+v", len(got), got)
	}
	// Most recently used first: "newer"'s last turn is at 19:03.
	if got[0].ID != "newer" || got[1].ID != "older" {
		t.Fatalf("order %q, %q", got[0].ID, got[1].ID)
	}
	if got[0].Turns != 2 || got[1].Turns != 1 {
		t.Fatalf("turn counts %d, %d", got[0].Turns, got[1].Turns)
	}
	// Started is when the file was opened; Last is the newest turn.
	if got[0].Started.IsZero() || !got[0].Last.Equal(at(3)) || !got[1].Last.Equal(at(1)) {
		t.Fatalf("times %v … %v, %v", got[0].Started, got[0].Last, got[1].Last)
	}
	if c := got[0].Cost; c < 0.0069 || c > 0.0071 {
		t.Fatalf("cost %v, want the two turns summed", c)
	}
}

func TestTurnHelpers(t *testing.T) {
	turn := scene()[0]
	if got := turn.Answered(); got != "Looking.\nRead it. The first milestone is the terminal.\n" {
		t.Fatalf("Answered %q", got)
	}
	if got := turn.Took(); got != time.Minute {
		t.Fatalf("Took %v", got)
	}
	if (Turn{}).Took() != 0 {
		t.Fatal("an unfinished turn took no time")
	}
}

func equalTurn(a, b Turn) bool {
	if !a.At.Equal(b.At) || !a.Ended.Equal(b.Ended) || a.Said != b.Said {
		return false
	}
	if a.Cost != b.Cost || a.InputTokens != b.InputTokens || a.OutputTokens != b.OutputTokens {
		return false
	}
	if len(a.Events) != len(b.Events) {
		return false
	}
	for i := range a.Events {
		if a.Events[i] != b.Events[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
