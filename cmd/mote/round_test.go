package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/incantery/mote/agent"
	"github.com/incantery/mote/profiles"
	"github.com/incantery/mote/tool/builtin"
)

// fakeRepo is a checkout to run the round against: real files, in a
// directory nothing else cares about, so a policy that regressed
// cannot damage anything.
func fakeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for path, body := range map[string]string{
		"GAPS.md":        "# Gaps\n\none\ntwo\nthree\n",
		"agent/agent.go": "package agent\n\nfunc Send() {}\n",
		"tui/tui.go":     "package tui\n",
	} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func newTestRound(t *testing.T) (*round, string, string) {
	t.Helper()
	repo, scratch := fakeRepo(t), t.TempDir()
	prof, err := profiles.Supervisor()
	if err != nil {
		t.Fatal(err)
	}
	r, err := newRound(&agent.Fake{Instant: true}, repo, scratch, builtin.Registry(repo), prof)
	if err != nil {
		t.Fatal(err)
	}
	return r, repo, scratch
}

// play runs the round, answering the first ask with choice and any
// later one with "no", and returns everything it said.
func play(t *testing.T, r *round, choice string) []agent.Event {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := r.Send(ctx, "c", "show me what the policy does")
	if err != nil {
		t.Fatal(err)
	}
	var out []agent.Event
	answered := false
	for ev := range ch {
		out = append(out, ev)
		if ev.Kind != agent.KindAsk {
			continue
		}
		say := agent.No
		if !answered {
			say, answered = choice, true
		}
		if err := r.Answer(ctx, ev.ID, say); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func byID(evs []agent.Event, kind agent.Kind, id string) *agent.Event {
	for i := range evs {
		if evs[i].Kind == kind && evs[i].ID == id {
			return &evs[i]
		}
	}
	return nil
}

// The eight calls, decided by the profile as written: five allowed
// outright, one denied in the profile's own words, and two that stop
// and ask.
func TestRoundDecisions(t *testing.T) {
	r, repo, scratch := newTestRound(t)
	evs := play(t, r, agent.Yes)

	for _, id := range []string{"c1", "c2", "c3", "c4", "c5"} {
		if byID(evs, agent.KindAsk, id) != nil {
			t.Errorf("%s should not have asked", id)
		}
		res := byID(evs, agent.KindToolResult, id)
		if res == nil {
			t.Fatalf("%s never ran", id)
		}
		if strings.HasPrefix(res.Result, "error:") {
			t.Errorf("%s: %s", id, res.Result)
		}
	}

	// The denied one is shown, and the model is told the profile's
	// sentence rather than a stack trace.
	deny := byID(evs, agent.KindToolResult, "c6")
	if deny == nil || !strings.Contains(deny.Result, "start a task for that") {
		t.Fatalf("c6: %+v", deny)
	}
	if _, err := os.ReadFile(filepath.Join(repo, "GAPS.md")); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(filepath.Join(repo, "GAPS.md")); string(body) != "# Gaps\n\none\ntwo\nthree\n" {
		t.Fatal("a denied write must not have happened")
	}

	// The allowed write really wrote.
	if body, err := os.ReadFile(filepath.Join(scratch, "vera", "notes.md")); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(body), "her own home") {
		t.Fatalf("%q", body)
	}

	// Both of the last two asked, because yes is not always.
	for _, id := range []string{"c7", "c8"} {
		ask := byID(evs, agent.KindAsk, id)
		if ask == nil {
			t.Fatalf("%s should have asked", id)
		}
		if ask.Name != "write" || ask.Text == "" {
			t.Fatalf("%s: %+v", id, *ask)
		}
	}
	// The first was answered yes and ran; the second was answered no
	// and did not.
	if byID(evs, agent.KindToolResult, "c7") == nil {
		t.Error("c7 was allowed and should have run")
	}
	if byID(evs, agent.KindToolCall, "c8") != nil {
		t.Error("c8 was declined and should not have run")
	}
	if _, err := os.Stat(filepath.Join(scratch, "elsewhere", "b.md")); !os.IsNotExist(err) {
		t.Error("a declined write must not have happened")
	}
}

// Always is a grant with a reach: the second write to the same
// directory is not asked about at all.
func TestRoundAlwaysStopsAsking(t *testing.T) {
	r, _, scratch := newTestRound(t)
	evs := play(t, r, agent.Always)

	if byID(evs, agent.KindAsk, "c8") != nil {
		t.Fatal("always should have covered the second write")
	}
	if _, err := os.Stat(filepath.Join(scratch, "elsewhere", "b.md")); err != nil {
		t.Fatalf("it should have run: %v", err)
	}
	if got := r.gate.Grants(); len(got) != 1 ||
		!strings.HasPrefix(got[0].String(), "write under ") {
		t.Fatalf("grants %v", got)
	}
	// And the reply says so.
	var reply strings.Builder
	for _, ev := range evs {
		if ev.Kind == agent.KindDelta {
			reply.WriteString(ev.Text)
		}
	}
	if !strings.Contains(reply.String(), "You said **always** to") {
		t.Fatalf("reply:\n%s", reply.String())
	}
	for _, want := range []string{"what the policy decided", "denied", "start a task for that"} {
		if !strings.Contains(reply.String(), want) {
			t.Errorf("the reply should say %q:\n%s", want, reply.String())
		}
	}
}

// A run streams what the command prints while it prints it.
func TestRoundStreamsCommandOutput(t *testing.T) {
	r, _, _ := newTestRound(t)
	evs := play(t, r, agent.No)
	var streamed strings.Builder
	for _, ev := range evs {
		if ev.Kind == agent.KindToolOutput && ev.ID == "c4" {
			streamed.WriteString(ev.Text)
		}
	}
	if byID(evs, agent.KindToolResult, "c4") == nil {
		t.Fatal("the command never ran")
	}
	// git in a directory that is not a repository still says
	// something, and what it says arrives as output rather than only
	// at the end.
	if streamed.Len() == 0 {
		t.Fatal("nothing was streamed")
	}
}

// Anything without "policy" in it is the scripted Fake, so the rest
// of the demo is unchanged.
func TestRoundFallsBackToTheFake(t *testing.T) {
	r, _, _ := newTestRound(t)
	ch, err := r.Send(context.Background(), "c", "tell me about the harness")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []agent.Kind
	for ev := range ch {
		kinds = append(kinds, ev.Kind)
	}
	if len(kinds) == 0 || kinds[len(kinds)-1] != agent.KindDone {
		t.Fatalf("%v", kinds)
	}
}

// An exchange cancelled while a question is open ends, and ends with
// done — the terminal is waiting for one.
func TestRoundCancelledOnAnAsk(t *testing.T) {
	r, _, _ := newTestRound(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := r.Send(ctx, "c", "policy, please")
	if err != nil {
		t.Fatal(err)
	}
	var evs []agent.Event
	for ev := range ch {
		evs = append(evs, ev)
		if ev.Kind == agent.KindAsk {
			cancel()
		}
	}
	if last := evs[len(evs)-1]; last.Kind != agent.KindDone {
		t.Fatalf("ended with %q", last.Kind)
	}
}

// The profile the demo shows is the profile the demo obeys.
func TestPolicyTextIsTheProfile(t *testing.T) {
	prof, err := profiles.Supervisor()
	if err != nil {
		t.Fatal(err)
	}
	got := policyText(prof, "profiles/supervisor")
	for _, want := range []string{
		"supervisor", "profiles/supervisor",
		"start a task for that", "git status", "${root}/**", "~/vera/**",
		"`read` — **allow**", "`run` — **ask**",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}
