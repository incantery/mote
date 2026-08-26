package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The zero Handle is the one a test, and a harness with nowhere to
// put any of it, hands over. Nothing in a tool should have to check.
func TestZeroHandleSwallowsEverything(t *testing.T) {
	var h Handle
	n, err := h.Write([]byte("hello"))
	if n != 5 || err != nil {
		t.Fatalf("wrote %d, %v", n, err)
	}
	h.Say("nobody is listening")
	if got := h.Value(Device); got != "" {
		t.Fatalf("a missing value is %q", got)
	}
}

// A watcher that goes away is not the tool's failure: a command that
// is still running should not be told its own output failed.
type broken struct{}

func (broken) Write([]byte) (int, error) { return 0, errors.New("gone") }

func TestHandleHidesAWatcherThatWentAway(t *testing.T) {
	h := Handle{Output: broken{}}
	n, err := h.Write([]byte("still running"))
	if err != nil || n != len("still running") {
		t.Fatalf("wrote %d, %v", n, err)
	}
}

func TestHandleSaysAndValues(t *testing.T) {
	var said []string
	h := Handle{
		Status: func(text string) { said = append(said, text) },
		Values: map[string]any{Device: "phone", Cwd: "/repo", "count": 3},
	}
	h.Say("Opening a room…")
	h.Say("") // an empty line is not a line
	if len(said) != 1 || said[0] != "Opening a room…" {
		t.Fatalf("said %q", said)
	}
	if h.Value(Device) != "phone" || h.Value(Cwd) != "/repo" {
		t.Fatalf("values %v", h.Values)
	}
	// A value that is not a string is not a string, and asking for
	// one should not panic or lie.
	if got := h.Value("count"); got != "" {
		t.Fatalf("count as a string is %q", got)
	}
}

// A tool writes to the Handle rather than to Output, which is what
// makes a nil Output safe.
type talker struct{}

func (talker) Name() string            { return "talker" }
func (talker) Description() string     { return "says things" }
func (talker) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (talker) Run(_ context.Context, _ json.RawMessage, h Handle) (Result, error) {
	h.Say("thinking about it…")
	h.Write([]byte("working\n"))
	return Result{
		Text: "done, for " + h.Value(Device),
		Meta: map[string]any{MetaTask: "t-1", MetaCost: 0.02},
	}, nil
}

func TestResultCarriesWhatTheHarnessRecords(t *testing.T) {
	var out strings.Builder
	var status []string
	res, err := talker{}.Run(context.Background(), nil, Handle{
		Output: &out,
		Status: func(s string) { status = append(status, s) },
		Values: map[string]any{Device: "the terminal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "done, for the terminal" {
		t.Fatalf("text %q", res.Text)
	}
	if res.Meta[MetaTask] != "t-1" || res.Meta[MetaCost] != 0.02 {
		t.Fatalf("meta %v", res.Meta)
	}
	// Meta is what the harness records; it must survive a journal and
	// a wire, which means it has to be JSON.
	if _, err := json.Marshal(res.Meta); err != nil {
		t.Fatalf("meta is not JSON-shaped: %v", err)
	}
	if out.String() != "working\n" {
		t.Fatalf("output %q", out.String())
	}
	if len(status) != 1 || status[0] != "thinking about it…" {
		t.Fatalf("status %q", status)
	}
}
