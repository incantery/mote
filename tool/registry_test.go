package tool

import (
	"context"
	"encoding/json"
	"io"
	"testing"
)

// stub is a tool that does nothing, so the registry can be tested
// without anything touching a disk.
type stub struct {
	name  string
	paths []string
	cmd   string
}

func (s stub) Name() string                   { return s.name }
func (s stub) Description() string            { return s.name + " does " + s.name }
func (s stub) Schema() json.RawMessage        { return json.RawMessage(`{"type":"object"}`) }
func (s stub) Paths(json.RawMessage) []string { return s.paths }
func (s stub) Command(json.RawMessage) string { return s.cmd }

func (s stub) Run(context.Context, json.RawMessage, io.Writer) (Result, error) {
	return Result{Text: s.name + " ran"}, nil
}

// bare has neither Paths nor Command, which is the case policy has to
// survive: a tool that cannot say what it touches.
type bare struct{}

func (bare) Name() string            { return "bare" }
func (bare) Description() string     { return "says nothing about itself" }
func (bare) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (bare) Run(context.Context, json.RawMessage, io.Writer) (Result, error) {
	return Result{}, nil
}

func TestRegistry(t *testing.T) {
	r := NewRegistry(stub{name: "read"}, stub{name: "write"})
	if _, ok := r.Get("read"); !ok {
		t.Fatal("read is missing")
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("nope is not a tool")
	}
	if got := len(r.List()); got != 2 {
		t.Fatalf("%d tools", got)
	}
	if err := r.Add(stub{name: "read"}); err == nil {
		t.Fatal("a duplicate name is an error")
	}
	if err := r.Add(stub{}); err == nil {
		t.Fatal("a nameless tool is an error")
	}
}

// Definitions are the OpenAI function-tool shape, which is what a
// harness puts straight into the request body.
func TestDefinitionsAreTheWireShape(t *testing.T) {
	r := NewRegistry(stub{name: "read"})
	buf, err := json.Marshal(r.Definitions())
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"function","function":{"name":"read","description":"read does read","parameters":{"type":"object"}}}]`
	if string(buf) != want {
		t.Fatalf("got  %s\nwant %s", buf, want)
	}
}

// A tool with no schema still gets parameters: an endpoint that is
// sent none either rejects the call or invents one.
func TestAToolWithNoSchemaStillHasParameters(t *testing.T) {
	r := NewRegistry(noSchema{})
	buf, _ := json.Marshal(r.Definitions()[0].Function.Parameters)
	if string(buf) != `{"type":"object","properties":{}}` {
		t.Fatalf("%s", buf)
	}
}

type noSchema struct{ stub }

func (noSchema) Name() string            { return "n" }
func (noSchema) Schema() json.RawMessage { return nil }

// Only is how a profile's tools list narrows a set of built-ins.
func TestOnly(t *testing.T) {
	r := NewRegistry(stub{name: "read"}, stub{name: "write"}, stub{name: "run"})
	few, err := r.Only("run", "read")
	if err != nil {
		t.Fatal(err)
	}
	if got := few.Names(); len(got) != 2 {
		t.Fatalf("%v", got)
	}
	if got := few.List()[0].Name(); got != "run" {
		t.Fatalf("order is the profile's: %q", got)
	}
	if _, err := r.Only("nonesuch"); err == nil {
		t.Fatal("a name nothing answers to is an error")
	}
}

// NewCall reads the paths and the command off the tool, so the policy
// never has to know what a tool calls its arguments.
func TestNewCallReadsTheTool(t *testing.T) {
	c := NewCall("c1", stub{name: "run", cmd: "git status"}, json.RawMessage(`{}`))
	if c.Command != "git status" || c.Tool != "run" || c.ID != "c1" {
		t.Fatalf("%+v", c)
	}
	c = NewCall("c2", stub{name: "write", paths: []string{"/tmp/x"}}, nil)
	if len(c.Paths) != 1 || c.Paths[0] != "/tmp/x" {
		t.Fatalf("%+v", c)
	}
}

// A tool that declares neither is decided by name alone — and a path
// rule, having no path to match, does not decide it.
func TestAToolThatSaysNothingAboutItself(t *testing.T) {
	c := NewCall("c1", bare{}, json.RawMessage(`{"path":"/etc/passwd"}`))
	if len(c.Paths) != 0 || c.Command != "" {
		t.Fatalf("%+v", c)
	}
	p := &Policy{Default: Ask, Rules: []Rule{{Paths: []string{"/**"}, Then: Allow}}}
	if got := p.Decide(c); got.Decision != Ask {
		t.Fatalf("%s, want ask", got.Decision)
	}
}
