package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

// drain reads a whole exchange.
func drain(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var out []Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timeout:
			t.Fatal("exchange did not end")
			return nil
		}
	}
}

func send(t *testing.T, f *Fake, text string) []Event {
	t.Helper()
	ch, err := f.Send(context.Background(), "c", text)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	return drain(t, ch)
}

// An instant Fake must produce the same events for the same turn every
// time — that is the whole reason the flag exists.
func TestFakeIsDeterministic(t *testing.T) {
	for turn := range 6 {
		a, b := &Fake{Instant: true}, &Fake{Instant: true}
		a.SetTurn(turn)
		b.SetTurn(turn)
		x, y := send(t, a, "hello there"), send(t, b, "hello there")
		if !reflect.DeepEqual(x, y) {
			t.Fatalf("turn %d: two runs disagree (%d vs %d events)", turn, len(x), len(y))
		}
		if len(x) == 0 {
			t.Fatalf("turn %d: no events", turn)
		}
		if last := x[len(x)-1]; last.Kind != KindDone {
			t.Fatalf("turn %d: ends with %q, want done", turn, last.Kind)
		}
	}
}

// Deltas must reassemble into exactly the scripted markdown: chunking
// is a display detail, not a transformation.
func TestFakeDeltasReassemble(t *testing.T) {
	f := &Fake{Instant: true}
	var b strings.Builder
	for _, ev := range send(t, f, "tell me about the harness") {
		if ev.Kind == KindDelta {
			b.WriteString(ev.Text)
		}
	}
	if got := b.String(); got != markdownReply {
		t.Fatalf("reassembled reply differs:\n%q", got)
	}
}

func TestFakeScenes(t *testing.T) {
	kinds := func(evs []Event) map[Kind]int {
		m := map[Kind]int{}
		for _, e := range evs {
			m[e.Kind]++
		}
		return m
	}

	t.Run("markdown", func(t *testing.T) {
		k := kinds(send(t, &Fake{Instant: true}, "anything"))
		if k[KindDelta] == 0 || k[KindStatus] == 0 {
			t.Fatalf("want deltas and status lines, got %v", k)
		}
	})

	t.Run("tools", func(t *testing.T) {
		evs := send(t, &Fake{Instant: true}, "run a tool for me")
		k := kinds(evs)
		if k[KindToolCall] < 2 || k[KindToolResult] < 2 {
			t.Fatalf("want a tool round, got %v", k)
		}
		if k[KindNotice] == 0 {
			t.Fatalf("want a notice mid-exchange, got %v", k)
		}
		// Every result must name a call that came before it.
		open := map[string]bool{}
		for _, e := range evs {
			switch e.Kind {
			case KindToolCall:
				open[e.ID] = true
			case KindToolResult:
				if !open[e.ID] {
					t.Fatalf("result for unknown call %q", e.ID)
				}
				if e.Duration <= 0 {
					t.Fatalf("result %q has no duration", e.ID)
				}
			}
		}
		// The notice must land before the exchange ends.
		var sawNotice, sawDone bool
		for _, e := range evs {
			switch e.Kind {
			case KindNotice:
				sawNotice = true
			case KindDone:
				sawDone = true
				if !sawNotice {
					t.Fatal("notice arrived after done")
				}
			}
		}
		if !sawDone {
			t.Fatal("no done")
		}
	})

	t.Run("error", func(t *testing.T) {
		evs := send(t, &Fake{Instant: true}, "make it fail")
		k := kinds(evs)
		if k[KindError] == 0 {
			t.Fatalf("want an error, got %v", k)
		}
		if evs[len(evs)-1].Kind != KindDone {
			t.Fatal("an error must still be followed by done")
		}
	})
}

func TestFakeRejectsEmpty(t *testing.T) {
	if _, err := (&Fake{Instant: true}).Send(context.Background(), "c", "   "); err == nil {
		t.Fatal("want an error for empty text")
	}
}

// A cancelled exchange must still end, and end with done, or the
// terminal waits forever on a turn that will never finish.
func TestFakeCancels(t *testing.T) {
	f := &Fake{} // real timing, so there is something to interrupt
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := f.Send(ctx, "c", "tell me everything")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	evs := drain(t, ch)
	if len(evs) == 0 || evs[len(evs)-1].Kind != KindDone {
		t.Fatalf("cancelled exchange ended with %v", evs)
	}
}

// Turns cycle so a demo shows everything without being told to.
func TestFakeCyclesScenes(t *testing.T) {
	f := &Fake{Instant: true}
	seen := map[Kind]bool{}
	for range 3 {
		for _, ev := range send(t, f, "hello") {
			seen[ev.Kind] = true
		}
	}
	for _, k := range []Kind{KindDelta, KindStatus, KindToolCall, KindToolResult, KindNotice, KindError, KindDone} {
		if !seen[k] {
			t.Fatalf("three turns never produced a %s", k)
		}
	}
}
