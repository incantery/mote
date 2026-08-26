package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/incantery/mote/agent"
	"github.com/incantery/mote/profile"
	"github.com/incantery/mote/tool"
)

// round is the demo's other agent: the one that actually runs things.
//
// There is no model here — the calls are scripted — but everything
// after the call is the real thing: the real built-in tools, against
// this checkout, decided by the real supervisor policy, with the real
// ask when the profile says ask. What a harness with a model does
// differently is where the calls come from; the rest of this file is
// the loop verad will write.
type round struct {
	fake *agent.Fake
	reg  *tool.Registry
	gate *tool.Gate
	prof *profile.Profile

	repo    string // the checkout the calls are about
	scratch string // what ~ means for the demo, so nothing writes to a real home
}

// call is one scripted tool call and a line about why it is here.
type call struct {
	id   string
	tool string
	args map[string]any
	why  string
}

// pause is the demo's own timing. A round driven by a test has none:
// the Fake's Instant flag is the switch for both halves of the demo,
// so a test never waits for a pace that is there to be watched.
func (r *round) pause(ctx context.Context, d time.Duration) {
	if r.fake != nil && r.fake.Instant {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (c call) json() json.RawMessage {
	buf, err := json.Marshal(c.args)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return buf
}

// script is the round, in the order it makes a point. Read down the
// `why` column and it is the supervisor profile, demonstrated.
func (r *round) script() []call {
	scratch := func(rest ...string) string {
		return filepath.Join(append([]string{r.scratch}, rest...)...)
	}
	return []call{
		{"c1", "list", map[string]any{"dir": r.repo, "depth": 1},
			"looking costs nothing — `list` is allowed outright"},
		{"c2", "read", map[string]any{"path": filepath.Join(r.repo, "GAPS.md"), "from": 1, "to": 10},
			"so is `read`, anywhere at all"},
		{"c3", "search", map[string]any{"pattern": "func Send", "dir": r.repo, "glob": "**/*.go"},
			"and `search`"},
		{"c4", "run", map[string]any{"command": "git status --short", "cwd": r.repo},
			"`run` is ask by default, but `git status` is one of the six commands that only read"},
		{"c5", "write", map[string]any{"path": scratch("vera", "notes.md"),
			"content": "# what the policy decided\n\nher own home, so nobody was asked\n"},
			"a write under `~/vera` — her own home"},
		{"c6", "write", map[string]any{"path": filepath.Join(r.repo, "GAPS.md"), "content": "nope\n"},
			"a write inside a project root — denied, in the profile's own words"},
		{"c7", "write", map[string]any{"path": scratch("elsewhere", "a.md"), "content": "one\n"},
			"neither her home nor a project: **this is the ask**"},
		{"c8", "write", map[string]any{"path": scratch("elsewhere", "b.md"), "content": "two\n"},
			"the same directory again — asked again after a *yes*, not after an *always*"},
	}
}

// Send is agent.Agent. A line with "policy" in it runs the real round;
// anything else goes to the scripted Fake, which is the rest of the
// demo.
func (r *round) Send(ctx context.Context, conversation, text string) (<-chan agent.Event, error) {
	if !strings.Contains(strings.ToLower(text), "policy") {
		return r.fake.Send(ctx, conversation, text)
	}
	out := make(chan agent.Event, 64)
	go func() {
		defer close(out)
		r.run(ctx, out)
	}()
	return out, nil
}

// Answer is agent.Answerer. Both halves of the demo can be waiting —
// the Fake on a scripted ask, the gate on a real one — and neither
// minds being told about an id that is not theirs.
func (r *round) Answer(ctx context.Context, id, choice string) error {
	if err := r.gate.Answer(ctx, id, choice); err != nil {
		return err
	}
	return r.fake.Answer(ctx, id, choice)
}

// run is the loop. This is the shape a harness with a model has,
// with the model's tool_calls in place of the script.
func (r *round) run(ctx context.Context, out chan<- agent.Event) {
	say := func(ev agent.Event) bool {
		select {
		case <-ctx.Done():
			return false
		case out <- ev:
			return true
		}
	}
	// Done goes out whatever happened, cancellation included: the
	// terminal is waiting for one, and the channel is buffered, so
	// this cannot block on a reader that has gone away.
	defer func() {
		select {
		case out <- agent.Done():
		default:
		}
	}()

	var notes []string
	for _, c := range r.script() {
		if ctx.Err() != nil {
			return
		}
		t, ok := r.reg.Get(c.tool)
		if !ok {
			say(agent.Failf("no tool named %q", c.tool))
			continue
		}
		args := c.json()

		// 1. What the policy says about this call.
		id := c.id
		policyCall := tool.NewCall(id, t, args)
		verdict := r.gate.Decide(policyCall)
		say(agent.Status(fmt.Sprintf("%s — the policy says %s", c.tool, verdict.Decision)))
		r.pause(ctx, 400*time.Millisecond)

		switch verdict.Decision {
		case tool.Deny:
			// A denied call still shows: the person sees what was
			// asked for, and the model is told why in the profile's
			// own words rather than in a stack trace.
			say(agent.Call(id, c.tool, string(args)))
			say(agent.Result(id, "error: "+verdict.Reason, 0, 0))
			notes = append(notes, note(c, verdict, "denied"))
			continue

		case tool.Ask:
			say(agent.Asking(id, c.tool, string(args),
				verdict.Reason+" — always would cover "+r.gate.Grant(policyCall).String()))
			allowed, err := r.gate.Wait(ctx, policyCall)
			if err != nil {
				return // the exchange ended; the card says so
			}
			if !allowed {
				notes = append(notes, note(c, verdict, "you said no"))
				continue
			}
		}

		// 2. Run it, streaming whatever it prints into the card.
		say(agent.Call(id, c.tool, string(args)))
		started := time.Now()
		res, err := t.Run(ctx, args, writerTo(id, out, ctx))
		text := res.Text
		if err != nil {
			text = "error: " + err.Error()
		}
		say(agent.Result(id, text, time.Since(started), 0))
		notes = append(notes, note(c, verdict, "ran"))
		r.pause(ctx, 250*time.Millisecond)
	}

	for _, chunk := range chunks(r.summary(notes)) {
		if !say(agent.Delta(chunk)) {
			return
		}
		r.pause(ctx, 30*time.Millisecond)
	}
}

func note(c call, v tool.Verdict, what string) string {
	return fmt.Sprintf("| `%s` | %s | %s | %s |", c.tool, v.Decision, what, v.Reason)
}

func (r *round) summary(notes []string) string {
	var b strings.Builder
	b.WriteString("## what the policy decided\n\n")
	fmt.Fprintf(&b, "Eight calls, through `%s` — the profile in `%s`.\n\n",
		r.prof.Name, r.where())
	b.WriteString("| tool | policy | what happened | why |\n| --- | --- | --- | --- |\n")
	for _, n := range notes {
		b.WriteString(n + "\n")
	}
	if grants := r.gate.Grants(); len(grants) > 0 {
		b.WriteString("\nYou said **always** to:\n\n")
		for _, g := range grants {
			b.WriteString("- `" + g.String() + "`\n")
		}
	}
	fmt.Fprintf(&b, "\nAnything written went to `%s`, which is this run's `~` "+
		"and is deleted when the demo quits. Nothing in `%s` was changed: the "+
		"checkout is a project root, and the profile says so.\n", r.scratch, r.repo)
	return b.String()
}

func (r *round) where() string {
	if r.prof.Dir != "" {
		return r.prof.Dir
	}
	return "the copy compiled into this binary"
}

// writerTo turns what a tool prints into tool_output events. A write
// that nobody is reading any more is dropped rather than blocking the
// tool that is still running.
func writerTo(id string, out chan<- agent.Event, ctx context.Context) *streamWriter {
	return &streamWriter{id: id, out: out, ctx: ctx}
}

type streamWriter struct {
	id  string
	out chan<- agent.Event
	ctx context.Context
}

func (w *streamWriter) Write(p []byte) (int, error) {
	select {
	case <-w.ctx.Done():
	case w.out <- agent.Output(w.id, string(p)):
	}
	return len(p), nil
}

// chunks cuts the reply the way a model emits it, so the summary
// arrives like an answer rather than appearing all at once.
func chunks(s string) []string {
	const n = 24
	var out []string
	runes := []rune(s)
	for i := 0; i < len(runes); {
		end := min(i+n, len(runes))
		for end < len(runes) && runes[end-1] != ' ' && runes[end-1] != '\n' {
			end++
		}
		out = append(out, string(runes[i:end]))
		i = end
	}
	return out
}

// --- what the demo runs against -----------------------------------------

// newRound wires the real thing: this checkout, the supervisor
// profile, the built-in tools it lists, and a scratch directory
// standing in for a home so that a demo cannot write to a real one.
func newRound(fake *agent.Fake, repo, scratch string, all *tool.Registry, prof *profile.Profile) (*round, error) {
	reg, err := prof.Registry(all)
	if err != nil {
		return nil, err
	}
	// Two changes to the profile as written, both so the demo is
	// honest rather than merely plausible:
	//
	//   Home     — `~/vera` is a directory in this run's scratch, so
	//              the auto-write is real and lands nowhere real.
	//   Roots    — this checkout is added, so the deny is about the
	//              repository you are actually looking at.
	prof.Policy.Home = scratch
	prof.Policy.Dir = repo
	prof.Policy.Roots = append(prof.Policy.Roots, repo)
	if err := os.MkdirAll(filepath.Join(scratch, "vera"), 0o755); err != nil {
		return nil, err
	}
	return &round{
		fake:    fake,
		reg:     reg,
		gate:    &tool.Gate{Policy: prof.Policy},
		prof:    prof,
		repo:    repo,
		scratch: scratch,
	}, nil
}

// findRepo walks up from the working directory looking for mote's own
// go.mod, so the demo can read the checkout it was run from. A demo
// run from somewhere else falls back to the working directory, which
// is still a real directory with real files in it.
func findRepo() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for at := dir; ; {
		if buf, err := os.ReadFile(filepath.Join(at, "go.mod")); err == nil {
			if strings.Contains(string(buf), "module github.com/incantery/mote") {
				return at
			}
		}
		parent := filepath.Dir(at)
		if parent == at {
			return dir
		}
		at = parent
	}
}
