package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/incantery/mote/tool"
)

// --- read ---------------------------------------------------------------

// Read is a file, or a range of its lines.
type Read struct{ Dir string }

type readArgs struct {
	Path string `json:"path"`
	From int    `json:"from"`
	To   int    `json:"to"`
}

func (Read) Name() string { return "read" }

func (Read) Description() string {
	return "Read a file. Give `from` and `to` (1-based, inclusive) for a range of lines; " +
		"leave them out for the whole file. Long files come back clipped, and say so."
}

func (Read) Schema() json.RawMessage {
	return schema(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "The file to read."},
    "from": {"type": "integer", "description": "First line, 1-based. Optional."},
    "to": {"type": "integer", "description": "Last line, inclusive. Optional."}
  },
  "required": ["path"]
}`)
}

func (r Read) Paths(args json.RawMessage) []string {
	return argPath(r.Dir, args, func(a json.RawMessage) string {
		var v readArgs
		_ = json.Unmarshal(a, &v)
		return v.Path
	})
}

func (r Read) Run(ctx context.Context, args json.RawMessage, h tool.Handle) (tool.Result, error) {
	var v readArgs
	if err := decode(r.Name(), args, &v); err != nil {
		return tool.Result{}, err
	}
	path, err := resolve(r.Dir, v.Path)
	if err != nil {
		return tool.Result{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return tool.Result{}, err
	}
	if info.IsDir() {
		return tool.Result{}, fmt.Errorf("%s is a directory — use list", path)
	}
	if info.Size() > int64(MaxFile) {
		return tool.Result{}, fmt.Errorf("%s is %s, larger than read will open (%s) — use search",
			path, size(int(info.Size())), size(MaxFile))
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return tool.Result{}, err
	}

	lines := strings.Split(strings.TrimSuffix(string(buf), "\n"), "\n")
	total := len(lines)
	from, to := 1, total
	if v.From > 0 {
		from = v.From
	}
	if v.To > 0 && v.To < to {
		to = v.To
	}
	if from > total {
		return tool.Result{Text: fmt.Sprintf("%s has %d lines; there is no line %d.", path, total, from)}, nil
	}
	if to < from {
		to = from
	}
	window := lines[from-1 : to]

	text, clipped := clipLines(window, MaxLines, "lines")
	head := fmt.Sprintf("%s — lines %d–%d of %d\n", path, from, min(to, from+len(window)-1), total)
	if from == 1 && to == total && !clipped {
		head = fmt.Sprintf("%s — %d lines\n", path, total)
	}
	return tool.Result{Text: clipBytes(head+text, MaxResult)}, nil
}

// --- write --------------------------------------------------------------

// Write is a whole file, replaced or created. Parent directories are
// created with it: a tool that fails because a directory is missing
// costs a round trip to say so.
type Write struct{ Dir string }

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (Write) Name() string { return "write" }

func (Write) Description() string {
	return "Write a file, replacing whatever was there. Parent directories are created. " +
		"To change part of a file, use edit instead — it does not need the whole thing."
}

func (Write) Schema() json.RawMessage {
	return schema(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "The file to write."},
    "content": {"type": "string", "description": "The whole new contents."}
  },
  "required": ["path", "content"]
}`)
}

func (w Write) Paths(args json.RawMessage) []string {
	return argPath(w.Dir, args, func(a json.RawMessage) string {
		var v writeArgs
		_ = json.Unmarshal(a, &v)
		return v.Path
	})
}

func (w Write) Run(ctx context.Context, args json.RawMessage, h tool.Handle) (tool.Result, error) {
	var v writeArgs
	if err := decode(w.Name(), args, &v); err != nil {
		return tool.Result{}, err
	}
	path, err := resolve(w.Dir, v.Path)
	if err != nil {
		return tool.Result{}, err
	}
	// "replaced" rather than "wrote": a model that reads "wrote" and
	// meant to append has no way to tell it lost the old contents.
	verb := "replaced"
	if _, err := os.Stat(path); err != nil {
		verb = "created"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return tool.Result{}, err
	}
	if err := os.WriteFile(path, []byte(v.Content), 0o644); err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Text: fmt.Sprintf("%s %s — %s, %d lines",
		verb, path, size(len(v.Content)), countLines(v.Content))}, nil
}

// --- edit ---------------------------------------------------------------

// Edit replaces one exact piece of a file with another. Exactly one:
// text that appears twice is ambiguous, and a tool that guesses which
// one was meant is a tool that silently edits the wrong line.
type Edit struct{ Dir string }

type editArgs struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

func (Edit) Name() string { return "edit" }

func (Edit) Description() string {
	return "Replace an exact piece of text in a file with another. The old text must appear " +
		"exactly once — include enough surrounding lines to make it unique. Whitespace counts."
}

func (Edit) Schema() json.RawMessage {
	return schema(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "The file to change."},
    "old": {"type": "string", "description": "The exact text to replace. Must occur once."},
    "new": {"type": "string", "description": "What to put there. Empty deletes it."}
  },
  "required": ["path", "old", "new"]
}`)
}

func (e Edit) Paths(args json.RawMessage) []string {
	return argPath(e.Dir, args, func(a json.RawMessage) string {
		var v editArgs
		_ = json.Unmarshal(a, &v)
		return v.Path
	})
}

func (e Edit) Run(ctx context.Context, args json.RawMessage, h tool.Handle) (tool.Result, error) {
	var v editArgs
	if err := decode(e.Name(), args, &v); err != nil {
		return tool.Result{}, err
	}
	if v.Old == "" {
		return tool.Result{}, fmt.Errorf("edit: `old` is empty — use write for a whole file")
	}
	path, err := resolve(e.Dir, v.Path)
	if err != nil {
		return tool.Result{}, err
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return tool.Result{}, err
	}
	body := string(buf)
	switch n := strings.Count(body, v.Old); n {
	case 1:
	case 0:
		return tool.Result{}, fmt.Errorf("edit: that text is not in %s — read it and copy the lines exactly", path)
	default:
		return tool.Result{}, fmt.Errorf("edit: that text is in %s %d times — include more of what is around it", path, n)
	}
	info, err := os.Stat(path)
	if err != nil {
		return tool.Result{}, err
	}
	next := strings.Replace(body, v.Old, v.New, 1)
	if err := os.WriteFile(path, []byte(next), info.Mode().Perm()); err != nil {
		return tool.Result{}, err
	}
	at := strings.Count(body[:strings.Index(body, v.Old)], "\n") + 1
	return tool.Result{Text: fmt.Sprintf("edited %s at line %d — %s became %s",
		path, at, plural(countLines(v.Old), "line"), plural(countLines(v.New), "line"))}, nil
}

// --- list ---------------------------------------------------------------

// List is what is in a directory, to a depth.
type List struct{ Dir string }

type listArgs struct {
	Dir   string `json:"dir"`
	Depth int    `json:"depth"`
}

func (List) Name() string { return "list" }

func (List) Description() string {
	return "List what is in a directory. `depth` 1 is the directory itself (the default), " +
		"2 includes its subdirectories, and so on. .git and node_modules are skipped."
}

func (List) Schema() json.RawMessage {
	return schema(`{
  "type": "object",
  "properties": {
    "dir": {"type": "string", "description": "The directory to list."},
    "depth": {"type": "integer", "description": "How many levels down. Default 1, most 8."}
  },
  "required": ["dir"]
}`)
}

func (l List) Paths(args json.RawMessage) []string {
	return argPath(l.Dir, args, func(a json.RawMessage) string {
		var v listArgs
		_ = json.Unmarshal(a, &v)
		return v.Dir
	})
}

// skipped are the directories nobody means. A listing that is nine
// tenths .git objects is a listing of nothing.
var skipped = map[string]bool{".git": true, "node_modules": true}

func (l List) Run(ctx context.Context, args json.RawMessage, h tool.Handle) (tool.Result, error) {
	var v listArgs
	if err := decode(l.Name(), args, &v); err != nil {
		return tool.Result{}, err
	}
	root, err := resolve(l.Dir, v.Dir)
	if err != nil {
		return tool.Result{}, err
	}
	depth := v.Depth
	if depth <= 0 {
		depth = 1
	}
	depth = min(depth, 8)

	var lines []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable corner is not a failed listing
		}
		if path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() && skipped[d.Name()] {
			return fs.SkipDir
		}
		if n := len(strings.Split(rel, string(filepath.Separator))); n > depth {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			lines = append(lines, rel+"/")
			return nil
		}
		suffix := ""
		if info, err := d.Info(); err == nil {
			suffix = "  " + size(int(info.Size()))
		}
		lines = append(lines, rel+suffix)
		return nil
	})
	if err != nil {
		return tool.Result{}, err
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return tool.Result{Text: root + " is empty."}, nil
	}
	text, _ := clipLines(lines, MaxEntries, "entries")
	head := fmt.Sprintf("%s — %d entries, depth %d\n", root, len(lines), depth)
	return tool.Result{Text: clipBytes(head+text, MaxResult)}, nil
}

func plural(n int, what string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}
