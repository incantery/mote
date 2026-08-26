package profiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/incantery/mote/profile"
	"github.com/incantery/mote/tool"
)

// The supervisor's rules, checked against the sentences the profile
// itself makes. This is the worked example, so it is the one policy
// whose decisions are asserted end to end from the file.
func TestSupervisorDecides(t *testing.T) {
	p, err := Supervisor()
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "supervisor" {
		t.Fatalf("name %q", p.Name)
	}
	if len(p.Tools) != 7 {
		t.Fatalf("tools %v", p.Tools)
	}
	if !strings.Contains(p.Prompt, "start a task for that") {
		t.Fatal("the prompt should say what the policy enforces")
	}

	pol := p.Policy
	pol.Home = "/home/v"
	pol.Dir = "/tmp"

	cases := []struct {
		what string
		call tool.Call
		want tool.Decision
	}{
		{"her own home", write("/home/v/vera/notes/a.md"), tool.Allow},
		{"a project root", write("/home/v/go/src/github.com/incantery/mote/GAPS.md"), tool.Deny},
		{"another project root", write("/home/v/go/src/github.com/incantery/rook/x.go"), tool.Deny},
		{"a .git anywhere", write("/tmp/scratch/.git/config"), tool.Deny},
		{"out through ../", write("/home/v/vera/../go/src/github.com/incantery/vera/x"), tool.Deny},
		{"somewhere else", write("/tmp/scratch/note.md"), tool.Ask},
		{"reading a project", read("/home/v/go/src/github.com/incantery/mote/GAPS.md"), tool.Allow},
		{"git status", run("git status --short"), tool.Allow},
		{"git push", run("git push origin main"), tool.Ask},
		{"a chained command", run("ls; rm -rf /"), tool.Ask},

		// Retracting a fact is deleting a file, and it reaches the
		// same three places a write does.
		{"retracting a fact", del("/home/v/vera/memory/mote-is-private.md"), tool.Allow},
		{"a file in a project", del("/home/v/go/src/github.com/incantery/mote/GAPS.md"), tool.Deny},
		{"a file in a .git", del("/tmp/p/.git/HEAD"), tool.Deny},
		{"a scratch file", del("/tmp/scratch/note.md"), tool.Ask},
		// A delete names more than one path, so the all-or-any rule
		// is not academic: an allow needs every path inside her home,
		// and a deny needs only one path in a project.
		{"a list that leaves her home", del("/home/v/vera/a.md", "/tmp/x.md"), tool.Ask},
		{"a list that reaches a project", del("/home/v/vera/a.md",
			"/home/v/go/src/github.com/incantery/rook/x.go"), tool.Deny},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			got := pol.Decide(c.call)
			if got.Decision != c.want {
				t.Fatalf("%s (%s: %s), want %s", got.Decision, got.Rule, got.Reason, c.want)
			}
		})
	}

	if got := pol.Decide(write("/home/v/go/src/github.com/incantery/vera/x")); got.Reason != "start a task for that" {
		t.Fatalf("the deny carries the profile's words: %q", got.Reason)
	}
}

// The embedded copy is the directory, byte for byte. It is the thing
// a person edits, so nothing may drift between them.
func TestEmbeddedIsTheDirectory(t *testing.T) {
	onDisk, err := profile.Load("supervisor")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := Supervisor()
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Prompt != embedded.Prompt {
		t.Error("the prompts differ")
	}
	for _, name := range []string{"profile.md", "policy.toml"} {
		want, err := os.ReadFile(filepath.Join("supervisor", name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := FS().(interface{ ReadFile(string) ([]byte, error) }).ReadFile("supervisor/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("%s differs between the directory and the embedded copy", name)
		}
	}
}

func write(path string) tool.Call {
	return tool.Call{ID: "c1", Tool: "write", Paths: []string{path}}
}

func read(path string) tool.Call {
	return tool.Call{ID: "c1", Tool: "read", Paths: []string{path}}
}

func del(paths ...string) tool.Call {
	return tool.Call{ID: "c1", Tool: "delete", Paths: paths}
}

func run(cmd string) tool.Call {
	return tool.Call{ID: "c1", Tool: "run", Command: cmd}
}
