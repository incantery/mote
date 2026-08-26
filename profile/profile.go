// Package profile is an agent, written down where a person can read
// it.
//
// A profile is a directory:
//
//	profile.md   the system prompt, with a small front matter
//	policy.toml  what the tools may touch
//	tools/       tools of its own — later
//
// Everything that makes one agent different from another lives here
// rather than in the loop: the prompt, which tools it has, and what
// those tools are allowed to do. Load reads the directory and hands
// back the three, and nothing in it knows what a model is.
package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/incantery/mote/tool"
)

// Profile is a directory, read.
type Profile struct {
	// Dir is where it came from, when it came from the filesystem.
	Dir string
	// Name is what to call this agent.
	Name string
	// Model is the profile's hint about which model suits it. It is a
	// hint: the harness owns the decision, and a profile that names a
	// model nobody has configured should not stop it from running.
	Model string
	// Prompt is profile.md below the front matter — the system prompt,
	// verbatim, with nothing added.
	Prompt string
	// Tools are the names this agent has, in the order it listed them.
	// Empty means the harness decides.
	Tools []string
	// Policy is policy.toml.
	Policy *tool.Policy
}

// Load reads a profile directory.
func Load(dir string) (*Profile, error) {
	p, err := LoadFS(os.DirFS(dir), ".")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	p.Dir = dir
	return p, nil
}

// LoadFS is Load from any filesystem, which is how an embedded
// profile — one compiled into a binary so it is there whatever
// directory the binary is run from — is read by the same code as one
// on disk.
func LoadFS(fsys fs.FS, dir string) (*Profile, error) {
	prompt, err := fs.ReadFile(fsys, path(dir, "profile.md"))
	if err != nil {
		return nil, fmt.Errorf("profile.md: %w", err)
	}
	front, body := split(string(prompt))
	p := &Profile{Prompt: body}
	if err := p.front(front); err != nil {
		return nil, fmt.Errorf("profile.md: %w", err)
	}

	rules, err := fs.ReadFile(fsys, path(dir, "policy.toml"))
	if err != nil {
		return nil, fmt.Errorf("policy.toml: %w", err)
	}
	policy := &tool.Policy{}
	if _, err := toml.Decode(string(rules), policy); err != nil {
		return nil, fmt.Errorf("policy.toml: %w", err)
	}
	if err := check(policy); err != nil {
		return nil, fmt.Errorf("policy.toml: %w", err)
	}
	p.Policy = policy
	return p, nil
}

func path(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}

// Registry narrows a set of tools to the ones this profile lists, in
// the order it listed them. A profile that lists none gets all of
// them, which is what a harness building its own set already meant.
func (p *Profile) Registry(all *tool.Registry) (*tool.Registry, error) {
	if len(p.Tools) == 0 {
		return all, nil
	}
	r, err := all.Only(p.Tools...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.where(), err)
	}
	return r, nil
}

func (p *Profile) where() string {
	if p.Dir != "" {
		return filepath.Join(p.Dir, "profile.md")
	}
	return "profile.md"
}

// --- the front matter ---------------------------------------------------

// split takes the front matter off the top of profile.md. It is the
// three-dash convention and nothing more: a file without one is all
// prompt, which is the right answer for the smallest possible profile.
func split(s string) (front, body string) {
	s = strings.TrimPrefix(s, "\ufeff")
	if !strings.HasPrefix(s, "---\n") {
		return "", s
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", s
	}
	// Past the closing marker: the rest of that line, then the blank
	// line a person leaves after it.
	after := rest[end+len("\n---"):]
	if nl := strings.IndexByte(after, '\n'); nl >= 0 {
		after = after[nl+1:]
	} else {
		after = ""
	}
	return rest[:end], strings.TrimLeft(after, "\r\n")
}

// front reads the three keys a profile's front matter has. It is not
// YAML and does not pretend to be: three keys, one per line, and an
// unknown one is an error rather than a thing that quietly did
// nothing.
func (p *Profile) front(front string) error {
	for _, line := range strings.Split(front, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return fmt.Errorf("front matter: %q is not `key: value`", line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "name":
			p.Name = value
		case "model":
			p.Model = value
		case "tools":
			p.Tools = words(value)
		default:
			return fmt.Errorf("front matter: no such key %q — name, model, tools", key)
		}
	}
	return nil
}

// words reads a list written either way a person would write one:
// `[read, write]` or `read, write` or `read write`.
func words(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	var out []string
	for _, f := range fields {
		if f = strings.Trim(f, `"'`); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// --- checking -----------------------------------------------------------

// check finds the mistakes worth finding when the file is read rather
// than when a tool call is decided. A policy that denies at midnight
// because of a typo made at noon is the failure this prevents.
func check(p *tool.Policy) error {
	if p.Default == "" {
		return errors.New("no `default` — say allow, ask or deny")
	}
	if err := decision(p.Default, "default"); err != nil {
		return err
	}
	for name, d := range p.Tools {
		if err := decision(d, "tools."+name); err != nil {
			return err
		}
	}
	for i, r := range p.Rules {
		at := fmt.Sprintf("rule %d", i+1)
		if err := decision(r.Then, at+".then"); err != nil {
			return err
		}
		if len(r.Tools) == 0 && len(r.Paths) == 0 && len(r.Commands) == 0 && i != len(p.Rules)-1 {
			return fmt.Errorf("%s matches everything but is not the last rule", at)
		}
		for _, pat := range r.Paths {
			if strings.Contains(pat, "${root}") && len(p.Roots) == 0 {
				return fmt.Errorf("%s uses ${root} and there are no roots", at)
			}
		}
	}
	return nil
}

func decision(d tool.Decision, at string) error {
	switch d {
	case tool.Allow, tool.Ask, tool.Deny:
		return nil
	case "":
		return fmt.Errorf("%s: missing — say allow, ask or deny", at)
	}
	return fmt.Errorf("%s: %q is not allow, ask or deny", at, d)
}
