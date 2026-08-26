package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/incantery/mote/tool"
)

// Both providers answer the same scripted exchange, each in its own
// wire format, and a loop watching only the Provider interface cannot
// tell which one it is talking to. That is the whole claim this
// package makes, so it is one test over a table of two.
var providers = []struct {
	name string
	// scene is a provider whose endpoint plays the exchange: a
	// sentence and a tool call, then — once the tool result goes
	// back — a sentence.
	scene func(t *testing.T) (Provider, *wire)
	// hangs is one whose endpoint starts a stream and never ends it.
	hangs func(t *testing.T) Provider
}{
	{"openai", openAIScene, openAIHangs},
	{"anthropic", anthropicScene, anthropicHangs},
}

func TestConformance(t *testing.T) {
	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			t.Run("exchange", func(t *testing.T) { exchange(t, p.scene) })
			t.Run("cancellation", func(t *testing.T) { cancellation(t, p.hangs) })
		})
	}
}

// exchange is the round trip a harness makes: text, a tool call, the
// tool result, text. What the two rounds cost is the same number
// either way, because each provider corrects its own wire's idea of
// what an input token is.
func exchange(t *testing.T, scene func(*testing.T) (Provider, *wire)) {
	p, w := scene(t)
	reg := tool.NewRegistry(stub{name: "read"})

	req := Request{
		System:      "you are a supervisor",
		Messages:    []Message{User("how many gaps?")},
		Tools:       reg.Definitions(),
		CacheSystem: true,
	}

	var first collect
	used, err := p.Stream(t.Context(), req, first.on)
	if err != nil {
		t.Fatal(err)
	}
	if first.text.String() != "Let me look. " {
		t.Fatalf("said %q", first.text.String())
	}
	if len(first.calls) != 1 {
		t.Fatalf("%d calls, want 1", len(first.calls))
	}
	call := first.calls[0]
	if call.ID == "" || call.Name != "read" {
		t.Fatalf("call is %+v", call)
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil || args.Path != "GAPS.md" {
		t.Fatalf("arguments are %q (%v)", call.Arguments, err)
	}
	if used.Input != 12 || used.CacheRead != 2 || used.Output != 7 {
		t.Fatalf("the first round cost %+v", used)
	}
	if used.Model == "" || used.StopReason == "" {
		t.Fatalf("the first round says nothing about who answered or why it stopped: %+v", used)
	}
	total := used

	// The second round is the first one plus what the model asked for
	// and what the tool said — and whatever the provider asked to be
	// given back, which the harness carries without reading. Every
	// provider has its own idea of how that goes on the wire; none of
	// that is here, and a provider with nothing to keep sent nothing
	// and gets nil.
	assistant := Assistant(first.text.String(), call)
	assistant.Raw = first.raw
	req.Messages = append(req.Messages, assistant, Answer(call.ID, "nine rows"))

	var second collect
	used, err = p.Stream(t.Context(), req, second.on)
	if err != nil {
		t.Fatal(err)
	}
	if second.text.String() != "Nine rows." {
		t.Fatalf("said %q", second.text.String())
	}
	if len(second.calls) != 0 {
		t.Fatalf("asked for %d more tools", len(second.calls))
	}
	if used.Input != 30 || used.Output != 5 {
		t.Fatalf("the second round cost %+v", used)
	}
	total.Input += used.Input
	total.Output += used.Output
	if total.Input != 42 || total.Output != 12 || total.CacheRead != 2 {
		t.Fatalf("the exchange cost %+v", total)
	}
	if w.count() != 2 {
		t.Fatalf("%d requests for a two-round exchange", w.count())
	}
}

// Cancelling the context ends the stream — Stream returns, with an
// error, and stops calling back. Without that a terminal's escape key
// is a suggestion.
func cancellation(t *testing.T, hangs func(*testing.T) Provider) {
	p := hangs(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	got := collect{each: func(ev Event) {
		if ev.Kind == KindDelta {
			cancel()
		}
	}}
	done := make(chan error, 1)
	go func() {
		_, err := p.Stream(ctx, Request{Messages: []Message{User("how many gaps?")}}, got.on)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled stream ended without an error")
		}
	case <-t.Context().Done():
		t.Fatal("cancelling the context did not end the stream")
	}
	if got.text.String() != "Let me look. " {
		t.Fatalf("said %q", got.text.String())
	}
}
