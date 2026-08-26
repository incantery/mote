// Package tool is what an agent can do, and what it is allowed to do.
//
// A tool is a name, a description, a JSON Schema for its arguments,
// and something to run. That is the whole of it — nothing here knows
// about a model, a loop, or a terminal. A harness registers tools,
// sends the registry's Definitions to the model, gets tool calls
// back, asks the Policy about each one, and runs the ones it may.
//
// Policy is the other half. A tool that takes paths says which of its
// arguments are paths; a tool that runs a command says what the
// command line is. A profile writes rules about those, and the answer
// is Allow, Ask or Deny with a reason a person can read. Deciding
// touches no files: a rule is matched against a cleaned, absolute
// path, and a path that tried to leave by way of ../ has already been
// resolved by the time any rule sees it.
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
)

// Result is what a tool returns to the model. Text is what the model
// is told; anything the person should watch while it happens went to
// the io.Writer instead.
type Result struct {
	Text string
}

// Tool is one thing an agent can do.
//
// Run is given the arguments the model wrote, unvalidated, and a
// writer for whatever it wants to show while it works — the harness
// turns those bytes into tool_output events, so a command that takes
// a minute is not a minute of silence. The writer is never nil; a
// tool with nothing to stream ignores it.
//
// An error from Run is the tool failing to run at all. A tool that
// ran and found nothing, or ran and the command exited 1, returns a
// Result saying so: the model can work with that, and a stack trace
// is not an answer.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Run(ctx context.Context, args json.RawMessage, out io.Writer) (Result, error)
}

// Pather is a tool whose arguments name files. Policy asks for them
// so a profile can say "ask before writing outside here" without
// knowing what any particular tool calls its argument.
//
// A tool that cannot read its own arguments returns nothing, and the
// call is decided by name alone — which is the safe direction: a path
// rule that cannot see a path does not allow it.
type Pather interface {
	Paths(args json.RawMessage) []string
}

// Commander is a tool that runs a command line, and can say what it
// would be. Policy matches prefixes of it: `git status` is reading,
// `git push` is not, and the difference is the first two words.
type Commander interface {
	Command(args json.RawMessage) string
}

// Paths is Pather.Paths for a tool that may not have it.
func Paths(t Tool, args json.RawMessage) []string {
	if p, ok := t.(Pather); ok {
		return p.Paths(args)
	}
	return nil
}

// Command is Commander.Command for a tool that may not have it.
func Command(t Tool, args json.RawMessage) string {
	if c, ok := t.(Commander); ok {
		return c.Command(args)
	}
	return ""
}

// --- the registry -------------------------------------------------------

// Registry is the tools a harness has. It is safe for concurrent use:
// a profile builds one at startup and a loop reads it per round.
type Registry struct {
	mu     sync.RWMutex
	order  []string
	byName map[string]Tool
}

// NewRegistry builds one holding the given tools. It panics on a
// duplicate name, which is a wiring mistake and not a runtime one.
func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{}
	for _, t := range tools {
		if err := r.Add(t); err != nil {
			panic("tool: " + err.Error())
		}
	}
	return r
}

// Add registers a tool. A second tool by the same name is an error:
// two tools with one name means the model's call is ambiguous, and
// silently keeping one of them is the worst way to resolve it.
func (r *Registry) Add(t Tool) error {
	if t == nil {
		return errors.New("nil tool")
	}
	name := t.Name()
	if name == "" {
		return errors.New("tool with no name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byName == nil {
		r.byName = map[string]Tool{}
	}
	if _, dup := r.byName[name]; dup {
		return fmt.Errorf("tool %q is already registered", name)
	}
	r.byName[name] = t
	r.order = append(r.order, name)
	return nil
}

// Get finds a tool by the name the model used.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.byName[name]
	return t, ok
}

// List is every tool, in the order they were added.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.byName[n])
	}
	return out
}

// Only is a registry holding the named tools of this one, in the
// order named — which is how a profile's `tools:` list turns a
// process-wide set of built-ins into the few this agent has. A name
// nothing answers to is an error rather than a silence.
func (r *Registry) Only(names ...string) (*Registry, error) {
	out := &Registry{}
	for _, n := range names {
		t, ok := r.Get(n)
		if !ok {
			return nil, fmt.Errorf("no tool named %q", n)
		}
		if err := out.Add(t); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// --- what the model is sent ---------------------------------------------

// Definition is one tool in the shape the chat-completions API wants:
// {"type":"function","function":{name, description, parameters}}. It
// is here rather than in a provider package because it is the same
// JSON for every OpenAI-compatible endpoint, and because it is the
// only thing about a tool the model ever sees.
type Definition struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// Function is a Definition's body.
type Function struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// emptyObject is the schema for a tool that takes no arguments. A
// missing `parameters` is rejected by some endpoints and quietly
// mishandled by others.
var emptyObject = json.RawMessage(`{"type":"object","properties":{}}`)

// Definitions is every tool as the model is told about it.
func (r *Registry) Definitions() []Definition {
	list := r.List()
	out := make([]Definition, 0, len(list))
	for _, t := range list {
		schema := t.Schema()
		if len(schema) == 0 {
			schema = emptyObject
		}
		out = append(out, Definition{
			Type: "function",
			Function: Function{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  schema,
			},
		})
	}
	return out
}

// Names is every registered name, sorted — for a profile that wants
// to say what it has, and for an error message that wants to say what
// it does not.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := append([]string(nil), r.order...)
	sort.Strings(out)
	return out
}
