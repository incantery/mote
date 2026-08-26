package tool

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func gate() *Gate { return &Gate{Policy: supervisor()} }

// The ordinary shape: the policy says ask, the person says yes, the
// call may run — and nothing was remembered.
func TestAskAnsweredYes(t *testing.T) {
	g := gate()
	c := write("/tmp/note.md")
	if v := g.Decide(c); v.Decision != Ask {
		t.Fatalf("%s", v.Decision)
	}
	go answer(t, g, c.ID, Yes)
	ok, err := g.Wait(context.Background(), c)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if len(g.Grants()) != 0 {
		t.Fatalf("a yes remembers nothing: %v", g.Grants())
	}
	if v := g.Decide(c); v.Decision != Ask {
		t.Fatal("and the next one is asked again")
	}
}

func TestAskAnsweredNo(t *testing.T) {
	g := gate()
	c := write("/tmp/note.md")
	go answer(t, g, c.ID, No)
	ok, err := g.Wait(context.Background(), c)
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

// "always" is a grant with a reach: this tool, in that directory and
// under it — not that one file, and not everywhere.
func TestAlwaysCoversTheDirectory(t *testing.T) {
	g := gate()
	c := write("/tmp/work/note.md")
	go answer(t, g, c.ID, Always)
	if ok, err := g.Wait(context.Background(), c); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}

	same := g.Decide(write("/tmp/work/other.md"))
	if same.Decision != Allow || same.Rule != "always" {
		t.Fatalf("same directory: %+v", same)
	}
	if deeper := g.Decide(write("/tmp/work/sub/x.md")); deeper.Decision != Allow {
		t.Fatalf("under it: %s", deeper.Decision)
	}
	if beside := g.Decide(write("/tmp/elsewhere/x.md")); beside.Decision != Ask {
		t.Fatalf("beside it: %s, want ask", beside.Decision)
	}
	if other := g.Decide(Call{Tool: "edit", Paths: []string{"/tmp/work/note.md"}}); other.Decision != Ask {
		t.Fatalf("another tool: %s, want ask", other.Decision)
	}
	// And it never overrides a deny.
	if denied := g.Decide(write("/src/mote/x")); denied.Decision != Deny {
		t.Fatalf("a root: %s, want deny", denied.Decision)
	}
}

// For a command the reach is the program, whatever its arguments.
func TestAlwaysCoversTheCommand(t *testing.T) {
	g := gate()
	c := run("go test ./...")
	go answer(t, g, c.ID, Always)
	if ok, _ := g.Wait(context.Background(), c); !ok {
		t.Fatal("always should allow")
	}
	if v := g.Decide(run("go build ./...")); v.Decision != Allow {
		t.Fatalf("same program: %s", v.Decision)
	}
	if v := g.Decide(run("curl example.com")); v.Decision != Ask {
		t.Fatalf("another program: %s, want ask", v.Decision)
	}
	if got := g.Grants(); len(got) != 1 || got[0].String() != "run go" {
		t.Fatalf("grants %v", got)
	}
}

// Grant says, before the answer, what an always would cover — which
// is what the card shows next to the key.
func TestGrantIsReadableBeforeAnswering(t *testing.T) {
	g := gate()
	if got := g.Grant(write("/tmp/work/note.md")).String(); got != "write under /tmp/work" {
		t.Fatalf("%q", got)
	}
	if got := g.Grant(run("go test ./...")).String(); got != "run go" {
		t.Fatalf("%q", got)
	}
}

// An answer that arrives before anybody waits is kept. Nothing in the
// types promises the order, and an ask that hangs on a race is the
// worst bug this could have.
func TestAnswerBeforeWait(t *testing.T) {
	g := gate()
	c := write("/tmp/note.md")
	if err := g.Answer(context.Background(), c.ID, Yes); err != nil {
		t.Fatal(err)
	}
	ok, err := g.Wait(context.Background(), c)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

// The second answer to one question is dropped.
func TestAnswerIsIdempotent(t *testing.T) {
	g := gate()
	c := write("/tmp/note.md")
	go func() {
		_ = g.Answer(context.Background(), c.ID, Yes)
		_ = g.Answer(context.Background(), c.ID, No)
	}()
	if ok, _ := g.Wait(context.Background(), c); !ok {
		t.Fatal("the first answer is the answer")
	}
}

func TestAnswerRejectsNonsense(t *testing.T) {
	g := gate()
	if err := g.Answer(context.Background(), "c1", "perhaps"); err == nil {
		t.Fatal("want an error")
	}
}

// A cancelled exchange is a no: nobody is going to answer now.
func TestCancelledAskIsANo(t *testing.T) {
	g := gate()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	ok, err := g.Wait(ctx, write("/tmp/note.md"))
	if ok || err == nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	// And the ask is gone, so a late answer does not resurrect it.
	if err := g.Answer(context.Background(), "c1", Yes); err != nil {
		t.Fatal(err)
	}
}

func TestWaitNeedsAnID(t *testing.T) {
	g := gate()
	if _, err := g.Wait(context.Background(), Call{Tool: "write"}); err == nil {
		t.Fatal("want an error")
	}
}

// A nil policy denies rather than allowing.
func TestNilPolicyGateDenies(t *testing.T) {
	var g Gate
	if v := g.Decide(write("/tmp/x")); v.Decision != Deny {
		t.Fatalf("%s", v.Decision)
	}
}

func answer(t *testing.T, g *Gate, id, choice string) {
	t.Helper()
	// Give Wait a moment to be waiting; the point of the other test
	// is that it does not have to.
	time.Sleep(5 * time.Millisecond)
	if err := g.Answer(context.Background(), id, choice); err != nil {
		t.Error(err)
	}
}

// A tool that knows which of its calls are the same question says so,
// and an "always" covers that rather than the first word of a command
// line it may not have.
type verbed struct{ stub }

func (verbed) Scope(args json.RawMessage) string {
	var v struct {
		Action string `json:"action"`
	}
	_ = json.Unmarshal(args, &v)
	return v.Action
}

func TestAlwaysCoversTheScopeTheToolStated(t *testing.T) {
	g := &Gate{Policy: &Policy{Default: Ask}}
	tl := verbed{stub{name: "fleet"}}

	start := NewCall("c1", tl, json.RawMessage(`{"action":"start","repo":"mote"}`))
	if got := g.Grant(start).String(); got != "fleet start" {
		t.Fatalf("an always would cover %q", got)
	}
	go answer(t, g, start.ID, Always)
	if ok, err := g.Wait(context.Background(), start); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}

	// Another start, and it is not asked about again.
	again := NewCall("c2", tl, json.RawMessage(`{"action":"start","repo":"vera"}`))
	if v := g.Decide(again); v.Decision != Allow || v.Rule != "always" {
		t.Fatalf("a second start is %v (%s)", v.Decision, v.Rule)
	}
	// A stop is a different question, and is still asked.
	stop := NewCall("c3", tl, json.RawMessage(`{"action":"stop","task":"a1"}`))
	if v := g.Decide(stop); v.Decision != Ask {
		t.Fatalf("stop is %v — an always about start does not cover it", v.Decision)
	}
}
