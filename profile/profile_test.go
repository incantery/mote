package profile

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/incantery/mote/tool"
)

func load(t *testing.T, prompt, policy string) *Profile {
	t.Helper()
	p, err := LoadFS(fstest.MapFS{
		"profile.md":  {Data: []byte(prompt)},
		"policy.toml": {Data: []byte(policy)},
	}, ".")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const minimal = `default = "deny"`

func TestFrontMatter(t *testing.T) {
	p := load(t, `---
name: supervisor
model: gpt-5
tools: [read, list, run]
---

You are a supervisor.

Be brief.
`, minimal)
	if p.Name != "supervisor" || p.Model != "gpt-5" {
		t.Fatalf("%+v", p)
	}
	if strings.Join(p.Tools, ",") != "read,list,run" {
		t.Fatalf("tools %v", p.Tools)
	}
	if p.Prompt != "You are a supervisor.\n\nBe brief.\n" {
		t.Fatalf("prompt %q", p.Prompt)
	}
}

// A profile with no front matter is all prompt, which is the right
// answer for the smallest one anybody would write.
func TestNoFrontMatter(t *testing.T) {
	p := load(t, "You are helpful.\n", minimal)
	if p.Prompt != "You are helpful.\n" || p.Name != "" || len(p.Tools) != 0 {
		t.Fatalf("%+v", p)
	}
}

// The list is read however a person wrote it.
func TestToolsListSpellings(t *testing.T) {
	for _, spelling := range []string{
		"tools: [read, write]",
		"tools: read, write",
		"tools: read write",
		`tools: ["read", "write"]`,
	} {
		p := load(t, "---\n"+spelling+"\n---\nhi\n", minimal)
		if strings.Join(p.Tools, ",") != "read,write" {
			t.Errorf("%q gave %v", spelling, p.Tools)
		}
	}
}

func TestAnUnknownFrontMatterKeyIsAnError(t *testing.T) {
	_, err := LoadFS(fstest.MapFS{
		"profile.md":  {Data: []byte("---\nvoice: soft\n---\nhi\n")},
		"policy.toml": {Data: []byte(minimal)},
	}, ".")
	if err == nil || !strings.Contains(err.Error(), "no such key") {
		t.Fatalf("%v", err)
	}
}

func TestPolicyDecodes(t *testing.T) {
	p := load(t, "hi\n", `
default = "ask"
roots = ["/src/mote"]

[tools]
read = "allow"
write = "ask"

[[rules]]
tools = ["write"]
paths = ["${root}/**"]
then = "deny"
reason = "start a task for that"

[[rules]]
tools = ["run"]
commands = ["git status"]
then = "allow"
`)
	pol := p.Policy
	pol.Home = "/home/v"
	if pol.Default != tool.Ask || pol.Tools["read"] != tool.Allow {
		t.Fatalf("%+v", pol)
	}
	if len(pol.Rules) != 2 || pol.Rules[0].Reason != "start a task for that" {
		t.Fatalf("%+v", pol.Rules)
	}
	if got := pol.Decide(tool.Call{Tool: "write", Paths: []string{"/src/mote/x"}}); got.Decision != tool.Deny {
		t.Fatalf("%+v", got)
	}
	if got := pol.Decide(tool.Call{Tool: "run", Command: "git status"}); got.Decision != tool.Allow {
		t.Fatalf("%+v", got)
	}
}

// A typo in a profile is found when the profile is read, not at
// midnight when a tool call is decided.
func TestBadPolicyIsCaughtAtLoad(t *testing.T) {
	cases := map[string]string{
		"no default":       `[tools]` + "\nread = \"allow\"\n",
		"bad default":      `default = "maybe"`,
		"bad tool default": "default = \"ask\"\n[tools]\nread = \"sometimes\"\n",
		"bad rule":         "default = \"ask\"\n[[rules]]\ntools = [\"run\"]\nthen = \"perhaps\"\n",
		"missing then":     "default = \"ask\"\n[[rules]]\ntools = [\"run\"]\n",
		"root with no roots": "default = \"ask\"\n[[rules]]\ntools = [\"write\"]\n" +
			"paths = [\"${root}/**\"]\nthen = \"deny\"\n",
		"a catch-all that is not last": "default = \"ask\"\n[[rules]]\nthen = \"deny\"\n" +
			"[[rules]]\ntools = [\"run\"]\nthen = \"allow\"\n",
	}
	for name, policy := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := LoadFS(fstest.MapFS{
				"profile.md":  {Data: []byte("hi\n")},
				"policy.toml": {Data: []byte(policy)},
			}, ".")
			if err == nil {
				t.Fatal("wanted an error")
			}
		})
	}
}

func TestAMissingFileIsNamed(t *testing.T) {
	_, err := LoadFS(fstest.MapFS{"profile.md": {Data: []byte("hi")}}, ".")
	if err == nil || !strings.Contains(err.Error(), "policy.toml") {
		t.Fatalf("%v", err)
	}
	_, err = LoadFS(fstest.MapFS{"policy.toml": {Data: []byte(minimal)}}, ".")
	if err == nil || !strings.Contains(err.Error(), "profile.md") {
		t.Fatalf("%v", err)
	}
}

// The profile's tools list narrows a registry, in the order it named.
func TestRegistryNarrows(t *testing.T) {
	all := tool.NewRegistry(named("read"), named("write"), named("run"))
	p := load(t, "---\ntools: [run, read]\n---\nhi\n", minimal)
	few, err := p.Registry(all)
	if err != nil {
		t.Fatal(err)
	}
	if got := few.List(); len(got) != 2 || got[0].Name() != "run" {
		t.Fatalf("%v", got)
	}
	// A profile that lists none gets what the harness built.
	none := load(t, "hi\n", minimal)
	if same, _ := none.Registry(all); same != all {
		t.Fatal("an empty list is not a narrowing")
	}
	// A name nothing answers to is an error, and it says where.
	bad := load(t, "---\ntools: [nonesuch]\n---\nhi\n", minimal)
	if _, err := bad.Registry(all); err == nil {
		t.Fatal("wanted an error")
	}
}
