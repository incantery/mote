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
// the Handle instead.
type Result struct {
	// Text is the answer, in the model's context on the next round.
	Text string
	// Meta is what the harness records beside the call and the model
	// never sees: the task a call started, the session it opened,
	// what it cost. Small and JSON-shaped — a harness writes it to a
	// journal and puts it on a wire, and neither survives a channel
	// or a func in it.
	//
	// It is a map rather than fields because the harness owns the
	// vocabulary: mote knows a tool reached something, not what
	// somebody's journal calls it. MetaTask, MetaSession and MetaCost
	// are the three names worth agreeing on.
	Meta map[string]any
}

// The keys a harness and a tool are likely to mean the same thing by.
// Nothing enforces them — Meta is the harness's map — but a tool that
// uses these is understood by a harness that never heard of it.
const (
	// MetaTask is an identifier for work this call started and did
	// not wait for.
	MetaTask = "task"
	// MetaSession is an identifier for a conversation this call
	// opened or spoke to.
	MetaSession = "session"
	// MetaCost is USD this call spent, as a float64.
	MetaCost = "cost"
)

// Handle is what a harness lends a tool for one call.
//
// A tool is given the model's arguments, and that is all the model
// knows. A harness knows more: which of the person's devices asked,
// what directory they are looking at, and how to put a line in front
// of them before there is any result to show. A Handle is that, and
// it is passed by value because it is per call and holds nothing to
// close.
//
// The zero Handle works: writing to it goes nowhere, saying something
// says it to nobody, and every value is missing. A tool never has to
// check.
type Handle struct {
	// Output is what the person watches while the tool works — the
	// harness turns those bytes into tool_output events, so a command
	// that takes a minute is not a minute of silence. Write to the
	// Handle rather than to this: a nil Output is allowed.
	Output io.Writer

	// Status is the harness's own voice, for a line about what is
	// happening while it happens — "Opening a room…". It is not the
	// tool's output: the output is the tool's bytes, shown as such,
	// and this is the harness saying something on the tool's behalf.
	// Call Say rather than this: a nil Status is allowed.
	//
	// A harness that has somewhere to put a status line sets it; one
	// that does not leaves it nil, and the lines are dropped.
	Status func(text string)

	// Values is what this harness knows about this call that the
	// arguments do not say. Device and Cwd are the keys mote
	// documents; a harness with more to say adds its own, and a tool
	// that does not recognise a key ignores it.
	//
	// It is read-only to the tool: the harness fills it per call, and
	// a tool that writes to it is writing to whatever the harness
	// handed it.
	Values map[string]any
}

// The keys a harness fills a Handle's Values with. A tool that wants
// one asks for it by name and copes with it not being there — a
// harness that has no devices has no device to name.
const (
	// Device is which of the person's devices asked: a phone, a
	// terminal, the name of a pane. A tool that reports somewhere
	// other than the reply uses it to report back to the same place.
	Device = "device"
	// Cwd is the directory the person is looking at, which is not
	// necessarily the one the tool resolves paths against — "the repo
	// in front of them" is a thing a person says and a tool has no
	// other way to learn.
	Cwd = "cwd"
)

// Write sends bytes to whoever is watching this call. A Handle with
// no Output swallows them and reports success, because a tool that
// nobody is watching has not failed.
func (h Handle) Write(p []byte) (int, error) {
	if h.Output == nil {
		return len(p), nil
	}
	n, err := h.Output.Write(p)
	if err != nil {
		// The watcher went away; the tool has not. Reporting a short
		// write here would make a tool think its own work failed.
		return len(p), nil
	}
	return n, nil
}

// Say puts one line in the harness's voice while the work happens.
// Each one replaces the last, the way a status line does; an empty
// line, and a Handle with nowhere to put one, say nothing.
func (h Handle) Say(text string) {
	if h.Status == nil || text == "" {
		return
	}
	h.Status(text)
}

// Value is Values[key] as a string: empty when the harness said
// nothing, and empty when it said something that is not a string.
// Most of what a harness knows about a call is a name or a path, and
// a tool asking for one should not have to write a type switch.
func (h Handle) Value(key string) string {
	s, _ := h.Values[key].(string)
	return s
}

// Tool is one thing an agent can do.
//
// Run is given the arguments the model wrote, unvalidated, and a
// Handle: somewhere to write what it wants watched, a way to say a
// line in the harness's voice, and what the harness knows about this
// call. The zero Handle works, so a tool with nothing to stream and
// nothing to ask ignores it.
//
// An error from Run is the tool failing to run at all. A tool that
// ran and found nothing, or ran and the command exited 1, returns a
// Result saying so: the model can work with that, and a stack trace
// is not an answer.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Run(ctx context.Context, args json.RawMessage, h Handle) (Result, error)
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

// Scoper is a tool that says how far an "always" about one of its
// calls should reach.
//
// Without it, a Gate works the scope out from the call: the directory
// for a file, the program for a command. That is right for a tool
// that reads like a shell, and wrong for one whose calls differ in a
// way only it understands — a tool whose `action` is the whole of the
// question ("always may you *start* a task", not "always may you talk
// to the fleet") says so here.
//
// The string is opaque: two calls with the same scope are the same
// question, and a person who said always to one has said always to
// the other. An empty string means "no opinion", and the Gate works
// it out as before.
type Scoper interface {
	Scope(args json.RawMessage) string
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

// Scope is Scoper.Scope for a tool that may not have it.
func Scope(t Tool, args json.RawMessage) string {
	if s, ok := t.(Scoper); ok {
		return s.Scope(args)
	}
	return ""
}

// --- the registry -------------------------------------------------------

// Registry is the tools a harness has.
//
// It is safe for concurrent use, and that is not only about a loop
// reading it per round: a registry changes while it is being served
// from. An MCP server that says its tool list changed is a Replace
// and a Remove on a registry another goroutine is in the middle of
// sending to a model, and every method here takes the lock.
type Registry struct {
	mu     sync.RWMutex
	order  []string
	byName map[string]Tool
	// owned is the harness's own tools: the ones a profile's `tools:`
	// list cannot drop, because a harness that cannot hand work away
	// is not a harness whatever the profile forgot to say.
	owned map[string]bool
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
	name, err := nameOf(t)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byName[name]; dup {
		return fmt.Errorf("tool %q is already registered", name)
	}
	r.add(name, t)
	return nil
}

// Own registers tools the harness brought itself, which a profile's
// `tools:` list narrows around rather than through.
//
// The distinction is about who chose. The built-ins are a set a
// profile picks from — a profile that lists three of them has three.
// A harness's own tools are not on that menu: they are what this
// harness *is*, and a profile that never heard of them would silently
// take them away. Own says so, and Only keeps them.
//
// They come first in the registry, which is the order the model reads
// them in.
func (r *Registry) Own(tools ...Tool) error {
	for _, t := range tools {
		name, err := nameOf(t)
		if err != nil {
			return err
		}
		r.mu.Lock()
		if _, dup := r.byName[name]; dup {
			r.mu.Unlock()
			return fmt.Errorf("tool %q is already registered", name)
		}
		r.add(name, t)
		if r.owned == nil {
			r.owned = map[string]bool{}
		}
		r.owned[name] = true
		r.mu.Unlock()
	}
	return nil
}

// Replace puts a tool in, whether or not one of that name was there.
// One that was keeps its place in the order and whether it was owned,
// so a tool that changed under a harness — an MCP server that
// re-listed with a new schema — does not move about in front of the
// model.
func (r *Registry) Replace(t Tool) error {
	name, err := nameOf(t)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, had := r.byName[name]; had {
		r.byName[name] = t
		return nil
	}
	r.add(name, t)
	return nil
}

// Remove takes tools out by name. A name nothing answers to is not an
// error: removing what is not there is what was asked for.
func (r *Registry) Remove(names ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range names {
		if _, had := r.byName[name]; !had {
			continue
		}
		delete(r.byName, name)
		delete(r.owned, name)
		for i, n := range r.order {
			if n == name {
				r.order = append(r.order[:i], r.order[i+1:]...)
				break
			}
		}
	}
}

// Owns says whether a tool of that name is the harness's own.
func (r *Registry) Owns(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.owned[name]
}

// add puts a named tool in. Called with the lock.
func (r *Registry) add(name string, t Tool) {
	if r.byName == nil {
		r.byName = map[string]Tool{}
	}
	r.byName[name] = t
	r.order = append(r.order, name)
}

func nameOf(t Tool) (string, error) {
	if t == nil {
		return "", errors.New("nil tool")
	}
	name := t.Name()
	if name == "" {
		return "", errors.New("tool with no name")
	}
	return name, nil
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
//
// Anything the harness Owns comes too, first and whether or not it
// was named. A profile chooses among the tools it was offered; it
// does not get to put the harness's hands behind its back.
func (r *Registry) Only(names ...string) (*Registry, error) {
	out := &Registry{}
	r.mu.RLock()
	owned := make([]Tool, 0, len(r.owned))
	for _, n := range r.order {
		if r.owned[n] {
			owned = append(owned, r.byName[n])
		}
	}
	r.mu.RUnlock()
	if err := out.Own(owned...); err != nil {
		return nil, err
	}
	for _, n := range names {
		t, ok := r.Get(n)
		if !ok {
			return nil, fmt.Errorf("no tool named %q", n)
		}
		if out.Owns(n) {
			continue // already here, and not the profile's to reorder
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
