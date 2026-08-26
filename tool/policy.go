package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Decision is what a policy says about a call.
type Decision string

const (
	// Allow runs it without asking.
	Allow Decision = "allow"
	// Ask puts the question to the person and waits.
	Ask Decision = "ask"
	// Deny refuses, and the reason is what the model is told.
	Deny Decision = "deny"
)

// Call is one tool call, as much of it as a policy needs to see. It
// is a plain struct so a test can write one down: deciding reads no
// files and asks no tool anything.
type Call struct {
	// ID is the model's id for the call. Policy ignores it; the ask
	// and its answer are keyed by it.
	ID string
	// Tool is the name the model used.
	Tool string
	// Args are the arguments as the model wrote them.
	Args json.RawMessage
	// Paths are the files this call touches, from Pather. They are
	// cleaned and made absolute before any rule sees them.
	Paths []string
	// Command is the command line, from Commander.
	Command string
	// Scope is how far an "always" about this call reaches, from
	// Scoper. Empty means the tool has no opinion and the Gate works
	// it out — see grantFor.
	Scope string
}

// NewCall reads a call out of a tool and its arguments.
func NewCall(id string, t Tool, args json.RawMessage) Call {
	return Call{
		ID:      id,
		Tool:    t.Name(),
		Args:    args,
		Paths:   Paths(t, args),
		Command: Command(t, args),
		Scope:   Scope(t, args),
	}
}

// Verdict is the decision and why, in words that can be shown to a
// person and sent to a model unchanged.
type Verdict struct {
	Decision Decision
	// Reason is the profile's own sentence when a rule carried one,
	// and a plain description of what decided otherwise.
	Reason string
	// Rule is what matched: "rule 2", "default for write", "the
	// profile's default", or "always" once a person has said so.
	Rule string
	// Path is the path that decided it, when one did. It is the
	// cleaned, absolute one — which is the one worth showing, since
	// it is the one the rule actually matched.
	Path string
}

// Refused is what a call that did not run says back to the model.
//
// It exists because the failure it describes is invisible otherwise.
// A policy's reason is written for a person — "start a task for that"
// — and a model handed that sentence alone can read it as advice
// beside a write that went through. So the sentence begins by saying
// that nothing happened, and the reason follows it.
//
// The "error: " prefix is the harness's existing convention for a
// call with no result, and the terminal already marks such a card ✗;
// keeping it means a refusal looks like what it is everywhere a
// failure already does.
func Refused(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "the policy did not allow it"
	}
	return "error: nothing was done: " + reason
}

// Refused is the sentence for a call this verdict refused, in the
// profile's own words when it had any.
func (v Verdict) Refused() string { return Refused(v.Reason) }

// Declined is the sentence for a call the person was asked about and
// said no to. The reason is the person, not the policy: the rule said
// ask, and asking is what happened.
func Declined() string { return Refused("you were asked, and said no") }

// Rule is one line of a profile's policy.
//
// A rule matches when every part of it that is set matches: the tool
// by name, the paths by glob, the command by prefix, the arguments by
// equality. A part left empty is not a wildcard that widens the rule
// — it is a question the rule does not ask.
//
// Paths are matched with doublestar, against the cleaned absolute
// path, so `~/vera/**` is everything under it and `**/.git/**` is
// every .git anywhere. A `${root}` in a pattern expands to each of
// the policy's Roots in turn, which is how "any project" is written
// once.
//
// The direction of a rule decides how many paths have to match. An
// allow needs *all* of them — a call that writes inside the sandbox
// and outside it is not inside the sandbox. A deny or an ask needs
// *one*: touching a forbidden path once is enough.
type Rule struct {
	Tools    []string `toml:"tools"`
	Paths    []string `toml:"paths"`
	Commands []string `toml:"commands"`
	// When is arguments this rule is about, by name and value:
	//
	//	when = { action = "stop" }
	//
	// Every pair must match for the rule to. The match is equality on
	// a top-level string argument and nothing cleverer: an argument
	// that is a number, an object or absent does not match, because a
	// rule that guesses what `3` and `"3"` have in common is a rule
	// nobody can predict.
	//
	// It is here because the difference between two calls to one tool
	// is often the whole of the question — `fleet` reporting on a
	// task and `fleet` stopping one are not the same permission, and
	// before this a harness had to smuggle the verb out through
	// Commander to write a rule about it.
	When   map[string]string `toml:"when"`
	Then   Decision          `toml:"then"`
	Reason string            `toml:"reason"`
}

// Policy is a profile's rules. The zero Policy denies everything,
// which is the right zero: a harness that forgot to load a profile
// should do nothing, not everything.
type Policy struct {
	// Default is the answer when no rule and no tool default matched.
	Default Decision `toml:"default"`
	// Tools is the default per tool, tried after the rules.
	Tools map[string]Decision `toml:"tools"`
	// Rules are tried in order; the first match wins.
	Rules []Rule `toml:"rules"`
	// Roots are the repositories `${root}` stands for.
	Roots []string `toml:"roots"`

	// Home is what `~` expands to. Empty means the person's. It is a
	// field so a test — and the demo — can put the sandbox somewhere
	// that is not somebody's home directory.
	Home string `toml:"-"`
	// Dir is what a relative path in a call is relative to. Empty
	// means the working directory.
	Dir string `toml:"-"`
}

// Decide answers a call. It is a pure function of the policy and the
// call: no file is opened, nothing is looked up, and the same call
// twice gets the same answer.
func (p *Policy) Decide(c Call) Verdict {
	c.Paths = p.Clean(c.Paths)
	c.Command = strings.TrimSpace(c.Command)

	args := stringArgs(c.Args, p.Rules)
	for i, r := range p.Rules {
		if path, ok := p.matches(r, c, args); ok {
			return Verdict{
				Decision: valid(r.Then),
				Reason:   p.reason(r, c, path),
				Rule:     fmt.Sprintf("rule %d", i+1),
				Path:     path,
			}
		}
	}
	if d, ok := p.Tools[c.Tool]; ok {
		return Verdict{
			Decision: valid(d),
			Reason:   fmt.Sprintf("%s is %s by default", c.Tool, valid(d)),
			Rule:     "default for " + c.Tool,
		}
	}
	d := valid(p.Default)
	return Verdict{
		Decision: d,
		Reason:   fmt.Sprintf("nothing said otherwise, and this profile %ss", d),
		Rule:     "the profile's default",
	}
}

// valid keeps an unreadable decision from becoming permission. A
// profile with a typo in it asks; it does not allow.
func valid(d Decision) Decision {
	switch d {
	case Allow, Ask, Deny:
		return d
	}
	return Deny
}

// reason is the rule's own sentence when it wrote one, and otherwise
// the shortest true description of what matched.
func (p *Policy) reason(r Rule, c Call, path string) string {
	if r.Reason != "" {
		return r.Reason
	}
	switch {
	case path != "":
		return fmt.Sprintf("%s %ss %s", c.Tool, valid(r.Then), path)
	case len(r.Commands) > 0:
		return fmt.Sprintf("%q is %s", c.Command, past(r.Then))
	case len(r.When) > 0:
		return fmt.Sprintf("%s %s is %s", c.Tool, when(r.When), past(r.Then))
	}
	return fmt.Sprintf("%s is %s", c.Tool, past(r.Then))
}

// past is a decision as a thing that happened to this call.
func past(d Decision) string {
	switch valid(d) {
	case Allow:
		return "allowed"
	case Ask:
		return "asked about"
	}
	return "denied"
}

// matches says whether a rule covers a call, and which path decided.
func (p *Policy) matches(r Rule, c Call, args map[string]string) (string, bool) {
	if len(r.Tools) > 0 && !contains(r.Tools, c.Tool) {
		return "", false
	}
	for name, want := range r.When {
		if got, ok := args[name]; !ok || got != want {
			return "", false
		}
	}
	path := ""
	if len(r.Paths) > 0 {
		var ok bool
		path, ok = p.matchPaths(r, c.Paths)
		if !ok {
			return "", false
		}
	}
	if len(r.Commands) > 0 && !matchCommand(r.Commands, c.Command) {
		return "", false
	}
	// A rule with nothing but a tool list matches that tool. A rule
	// with nothing at all matches everything, which is how a profile
	// writes a catch-all as its last line.
	return path, true
}

// matchPaths applies the all-or-any rule. It returns the path worth
// naming: for an allow, the first one, since they all matched; for a
// deny or an ask, the one that tripped it.
func (p *Policy) matchPaths(r Rule, paths []string) (string, bool) {
	if len(paths) == 0 {
		return "", false // a rule about paths cannot decide a call with none
	}
	globs := p.globs(r.Paths)
	if valid(r.Then) == Allow {
		for _, path := range paths {
			if !matchAny(globs, path) {
				return "", false
			}
		}
		return paths[0], true
	}
	for _, path := range paths {
		if matchAny(globs, path) {
			return path, true
		}
	}
	return "", false
}

// when is a rule's argument test as a person would read it back:
// `action=stop`, and in a stable order so two runs say it the same.
func when(m map[string]string) string {
	pairs := make([]string, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, " ")
}

// stringArgs is a call's top-level string arguments, read once for
// all the rules and only when some rule asks about one. A tool's
// arguments are whatever the model wrote: unreadable JSON, or JSON
// that is not an object, has no arguments to match, which is the safe
// direction — a rule about an argument it cannot see does not fire,
// and every rule that would have allowed on one is a rule that now
// falls through to something stricter.
func stringArgs(args json.RawMessage, rules []Rule) map[string]string {
	asked := false
	for _, r := range rules {
		if len(r.When) > 0 {
			asked = true
			break
		}
	}
	if !asked || len(args) == 0 {
		return nil
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(args, &raw) != nil {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		var s string
		if json.Unmarshal(v, &s) == nil {
			out[k] = s
		}
	}
	return out
}

func matchAny(globs []string, path string) bool {
	for _, g := range globs {
		if ok, err := doublestar.Match(g, path); err == nil && ok {
			return true
		}
	}
	return false
}

// matchCommand is a prefix on a word boundary: `git status` covers
// `git status --short` and not `git statusfoo`, and `ls` covers `ls`
// itself. Anything with a shell operator in it is not a prefix match
// of anything — `git log; rm -rf /` is not `git log`.
func matchCommand(prefixes []string, cmd string) bool {
	cmd = strings.Join(strings.Fields(cmd), " ")
	if cmd == "" || strings.ContainsAny(cmd, ";|&<>`$(){}\n") {
		return false
	}
	for _, p := range prefixes {
		p = strings.Join(strings.Fields(p), " ")
		if p == "" {
			continue
		}
		if cmd == p || strings.HasPrefix(cmd, p+" ") {
			return true
		}
	}
	return false
}

// globs expands `~` and `${root}` and cleans what is left, so that
// what a rule is matched with is the same shape as what it is matched
// against.
func (p *Policy) globs(patterns []string) []string {
	out := make([]string, 0, len(patterns))
	for _, pat := range patterns {
		if strings.Contains(pat, "${root}") {
			for _, root := range p.Roots {
				out = append(out, cleanGlob(strings.ReplaceAll(pat, "${root}", p.expand(root))))
			}
			continue
		}
		out = append(out, cleanGlob(p.expand(pat)))
	}
	return out
}

// cleanGlob tidies a pattern without letting filepath.Clean eat the
// meaning out of it: `**` stays two stars, and a pattern that is not
// anchored anywhere stays unanchored.
func cleanGlob(pat string) string {
	if pat == "" {
		return pat
	}
	cleaned := filepath.Clean(pat)
	// filepath.Clean turns "a/**" into "a/**" but "a/**/" into "a/**";
	// either is fine. What it must not do is make a relative pattern
	// absolute, and it does not.
	return cleaned
}

// Clean is every path made absolute and lexically resolved, which is
// the form every rule is matched against. A `../` that meant to climb
// out of a sandbox is gone by the time anything is compared, and a
// relative path is relative to the policy's Dir rather than to
// whatever the process happened to chdir to since.
//
// Symlinks are not followed: that would be I/O, and a decision that
// reads the disk is a decision that changes under you. See GAPS.
func (p *Policy) Clean(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		out = append(out, p.CleanPath(path))
	}
	return out
}

// CleanPath is Clean for one.
func (p *Policy) CleanPath(path string) string {
	path = p.expand(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.dir(), path)
	}
	return filepath.Clean(path)
}

// expand replaces a leading ~ with the home this policy is written
// against. Only a leading one: a file called "~" in a directory is a
// file called "~".
func (p *Policy) expand(path string) string {
	switch {
	case path == "~":
		return p.home()
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(p.home(), path[2:])
	}
	return path
}

func (p *Policy) home() string {
	if p.Home != "" {
		return p.Home
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return string(filepath.Separator)
}

func (p *Policy) dir() string {
	if p.Dir != "" {
		return p.Dir
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return string(filepath.Separator)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
