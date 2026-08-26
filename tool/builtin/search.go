package builtin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/incantery/mote/tool"
)

// Search is grep, with ripgrep's speed when ripgrep is installed and
// Go's own regexp when it is not. The two agree on what a match is —
// file:line:text, in path order — so a harness that has rg and one
// that does not give the model the same shape of answer.
type Search struct {
	Dir string
	// RG is the ripgrep to use. Empty means look for one on PATH;
	// "-" means do not, which is how the fallback is tested.
	RG string
}

type searchArgs struct {
	Pattern string `json:"pattern"`
	Dir     string `json:"dir"`
	Glob    string `json:"glob"`
}

func (Search) Name() string { return "search" }

func (Search) Description() string {
	return "Search files for a regular expression. `glob` narrows it by path — `**/*.go`, " +
		"`cmd/**`. Answers are file:line:text; long answers come back clipped, and say so."
}

func (Search) Schema() json.RawMessage {
	return schema(`{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "A regular expression."},
    "dir": {"type": "string", "description": "Where to look. Default: the working directory."},
    "glob": {"type": "string", "description": "Only files whose path matches, e.g. **/*.go. Optional."}
  },
  "required": ["pattern"]
}`)
}

func (s Search) Paths(args json.RawMessage) []string {
	var v searchArgs
	_ = json.Unmarshal(args, &v)
	dir := v.Dir
	if dir == "" {
		dir = "."
	}
	abs, err := resolve(s.Dir, dir)
	if err != nil {
		return nil
	}
	return []string{abs}
}

func (s Search) Run(ctx context.Context, args json.RawMessage, h tool.Handle) (tool.Result, error) {
	var v searchArgs
	if err := decode(s.Name(), args, &v); err != nil {
		return tool.Result{}, err
	}
	if strings.TrimSpace(v.Pattern) == "" {
		return tool.Result{}, fmt.Errorf("search: no pattern")
	}
	dir := v.Dir
	if dir == "" {
		dir = "."
	}
	root, err := resolve(s.Dir, dir)
	if err != nil {
		return tool.Result{}, err
	}
	if _, err := os.Stat(root); err != nil {
		return tool.Result{}, err
	}

	lines, err := s.ripgrep(ctx, v, root)
	if err != nil {
		lines, err = s.walk(ctx, v, root)
	}
	if err != nil {
		return tool.Result{}, err
	}
	if len(lines) == 0 {
		where := root
		if v.Glob != "" {
			where += " matching " + v.Glob
		}
		return tool.Result{Text: fmt.Sprintf("no match for %q in %s", v.Pattern, where)}, nil
	}
	text, _ := clipLines(lines, MaxLines, "matches")
	head := fmt.Sprintf("%q in %s — %d matches\n", v.Pattern, root, len(lines))
	return tool.Result{Text: clipBytes(head+text, MaxResult)}, nil
}

// ripgrep runs rg if there is one. An error here is not a failure of
// the search — it is "no rg", and the caller walks instead.
func (s Search) ripgrep(ctx context.Context, v searchArgs, root string) ([]string, error) {
	if s.RG == "-" {
		return nil, fmt.Errorf("no rg")
	}
	rg := s.RG
	if rg == "" {
		found, err := exec.LookPath("rg")
		if err != nil {
			return nil, err
		}
		rg = found
	}
	argv := []string{"--line-number", "--no-heading", "--color", "never", "--max-columns", "300"}
	if v.Glob != "" {
		argv = append(argv, "--glob", v.Glob)
	}
	argv = append(argv, "--regexp", v.Pattern, root)
	cmd := exec.CommandContext(ctx, rg, argv...)
	buf, err := cmd.Output()
	// rg exits 1 for "no matches", which is an answer and not a
	// failure; anything else means fall back.
	if err != nil {
		var ee *exec.ExitError
		if !asExit(err, &ee) || ee.ExitCode() != 1 {
			return nil, err
		}
	}
	return splitLines(string(buf)), nil
}

// walk is the search Go can do on its own: every file under root that
// the glob admits, line by line.
func (s Search) walk(ctx context.Context, v searchArgs, root string) ([]string, error) {
	re, err := regexp.Compile(v.Pattern)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	var lines []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if skipped[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if v.Glob != "" && !globMatches(v.Glob, root, path) {
			return nil
		}
		if len(lines) >= MaxLines*2 {
			return fs.SkipAll
		}
		lines = append(lines, grep(path, re)...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return lines, nil
}

// globMatches tries the glob against the path relative to the search
// root and against the whole path, because a person writes `**/*.go`
// meaning either.
func globMatches(glob, root, path string) bool {
	if ok, _ := doublestar.Match(glob, path); ok {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	ok, _ := doublestar.Match(glob, rel)
	return ok
}

func grep(path string, re *regexp.Regexp) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	// A file that is not text is not searched: a binary "match" is a
	// line of terminal garbage nobody can act on.
	head := make([]byte, 512)
	n, _ := f.Read(head)
	if isBinary(head[:n]) {
		return nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for line := 1; sc.Scan(); line++ {
		if text := sc.Text(); re.MatchString(text) {
			out = append(out, fmt.Sprintf("%s:%d:%s", path, line, clipColumns(text, 300)))
		}
	}
	return out
}

func isBinary(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

func clipColumns(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
