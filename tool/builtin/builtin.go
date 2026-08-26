// Package builtin is the six tools a coding agent cannot do without:
// read, write, edit, list, search and run.
//
// They are small on purpose. Each one does the obvious thing, says
// what it did in a sentence, and caps what it hands back — a tool
// that returns four megabytes of a log file has not helped anybody,
// least of all the model paying for it by the token. Every cap says
// so where it bit, so nothing is quietly missing.
//
// An error from Run is the tool failing: arguments that cannot be
// read, a file that is not there, a directory that cannot be opened.
// A command that exits 1 is not a failure of the tool — it is a
// Result that says the command exited 1, which is what the model
// needs to know.
package builtin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/incantery/mote/tool"
)

// The caps. They are variables rather than constants so a harness
// with a different appetite can move them, and they are documented in
// the tool descriptions so the model knows what it is getting.
var (
	// MaxResult is how many bytes of anything a tool hands back.
	MaxResult = 48 << 10
	// MaxLines is how many lines read and search return at once.
	MaxLines = 800
	// MaxEntries is how many paths list returns.
	MaxEntries = 500
	// MaxFile is the largest file read will open at all.
	MaxFile = 4 << 20
)

// New is the six, resolving relative paths against dir. An empty dir
// means the process's working directory — but a harness should pass
// one, and pass the same one to Policy.Dir, or a rule and a tool will
// disagree about what "notes.md" means.
func New(dir string) []tool.Tool {
	return []tool.Tool{
		Read{Dir: dir},
		Write{Dir: dir},
		Edit{Dir: dir},
		List{Dir: dir},
		Search{Dir: dir},
		Run{Dir: dir},
	}
}

// Registry is New in a registry.
func Registry(dir string) *tool.Registry { return tool.NewRegistry(New(dir)...) }

// decode reads a tool's arguments, and says which tool could not read
// them — a model that sent the wrong shape is told what to fix.
func decode(name string, args json.RawMessage, into any) error {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(args, into); err != nil {
		return fmt.Errorf("%s: arguments are not readable: %w", name, err)
	}
	return nil
}

// resolve makes a path absolute the way the policy does, so that what
// a rule decided about is what gets opened.
func resolve(dir, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("no path given")
	}
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}
	if !filepath.IsAbs(path) {
		if dir == "" {
			wd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			dir = wd
		}
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path), nil
}

// argPath is what a tool's Paths returns: the resolved path, or
// nothing at all if the arguments could not be read. Nothing is the
// safe answer — a path rule with no path to match does not allow.
func argPath(dir string, args json.RawMessage, field func(json.RawMessage) string) []string {
	raw := field(args)
	if raw == "" {
		return nil
	}
	abs, err := resolve(dir, raw)
	if err != nil {
		return nil
	}
	return []string{abs}
}

// clipLines keeps the first n lines and says how many it dropped.
func clipLines(lines []string, n int, what string) (string, bool) {
	if len(lines) <= n {
		return strings.Join(lines, "\n"), false
	}
	kept := strings.Join(lines[:n], "\n")
	return fmt.Sprintf("%s\n… %d more %s (%d in all)", kept, len(lines)-n, what, len(lines)), true
}

// clipBytes is the last cap, applied to whatever a tool ended up with.
func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndexByte(s[:n], '\n')
	if cut <= 0 {
		cut = n
	}
	return fmt.Sprintf("%s\n… truncated: %s of %s shown",
		s[:cut], size(cut), size(len(s)))
}

func size(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d B", n)
	case n < 1000*1000:
		return fmt.Sprintf("%.1f kB", float64(n)/1000)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/1000/1000)
}

// schema is a JSON Schema written as JSON, because that is what it
// is. Writing them as Go maps and marshalling costs a reader the
// ability to see the thing they are reading about.
func schema(s string) json.RawMessage { return json.RawMessage(s) }
