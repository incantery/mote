package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/incantery/mote/tool"
)

// --- delete -------------------------------------------------------------

// Delete removes a file, or a few of them.
//
// It is the tool a curated set of notes needs and nothing more: no
// tree, no glob, no recursion. A directory is only removed when the
// call asks for one *and* it is already empty, so the worst a typo
// can do is take one file with it — and the policy decides which
// files are reachable at all, the same way it does for write and
// edit.
//
// Everything is checked before anything is removed: a call that names
// four paths and cannot have the third one removes none of them. Half
// a curation is worse than none, because nothing says which half.
type Delete struct{ Dir string }

type deleteArgs struct {
	Path  string   `json:"path"`
	Paths []string `json:"paths"`
	Dir   bool     `json:"dir"`
}

func (Delete) Name() string { return "delete" }

func (Delete) Description() string {
	return "Delete a file: `path` for one, `paths` for a few at once. It removes files, " +
		"not trees — an empty directory needs `dir` true, and a directory with anything " +
		"in it is refused. Nothing is removed unless every path can be. There is no undo."
}

func (Delete) Schema() json.RawMessage {
	return schema(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "The file to delete."},
    "paths": {"type": "array", "items": {"type": "string"},
      "description": "Several files to delete in one call. Use instead of, or as well as, path."},
    "dir": {"type": "boolean", "description": "Allow an empty directory to be removed. Default false."}
  }
}`)
}

// Paths is every path the call names, so a rule about where this
// agent may delete sees all of them — an allow needs all of them to
// match, and a deny needs only one.
func (d Delete) Paths(args json.RawMessage) []string {
	var v deleteArgs
	if json.Unmarshal(args, &v) != nil {
		return nil
	}
	out, err := d.resolveAll(v)
	if err != nil {
		return nil
	}
	return out
}

// resolveAll is the call's paths, absolute, cleaned and in the order
// they were given, with a repeat dropped rather than removed twice.
func (d Delete) resolveAll(v deleteArgs) ([]string, error) {
	given := make([]string, 0, len(v.Paths)+1)
	if strings.TrimSpace(v.Path) != "" {
		given = append(given, v.Path)
	}
	for _, p := range v.Paths {
		if strings.TrimSpace(p) != "" {
			given = append(given, p)
		}
	}
	if len(given) == 0 {
		return nil, fmt.Errorf("delete: no path — give `path`, or `paths`")
	}
	if len(given) > MaxPaths {
		return nil, fmt.Errorf("delete: %d paths, more than one call takes (%d) — nothing was removed",
			len(given), MaxPaths)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(given))
	for _, p := range given {
		abs, err := resolve(d.Dir, p)
		if err != nil {
			return nil, fmt.Errorf("delete: %w", err)
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	return out, nil
}

// doomed is one path, checked and ready to go.
type doomed struct {
	path  string
	dir   bool
	bytes int64
}

func (d doomed) what() string {
	if d.dir {
		return d.path + " — an empty directory"
	}
	return fmt.Sprintf("%s — %s", d.path, size(int(d.bytes)))
}

func (d Delete) Run(ctx context.Context, args json.RawMessage, out io.Writer) (tool.Result, error) {
	var v deleteArgs
	if err := decode(d.Name(), args, &v); err != nil {
		return tool.Result{}, err
	}
	paths, err := d.resolveAll(v)
	if err != nil {
		return tool.Result{}, err
	}

	// Look at all of them first. Every refusal below says that
	// nothing was removed, because nothing has been.
	list := make([]doomed, 0, len(paths))
	for _, path := range paths {
		if path == filepath.Dir(path) {
			return tool.Result{}, fmt.Errorf("delete: %s is the root of a filesystem — nothing was removed", path)
		}
		// Lstat, not Stat: a symlink is a file, whatever it points
		// at, and removing it removes the link and not the target.
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return tool.Result{}, fmt.Errorf("delete: %s is not there — nothing was removed", path)
			}
			return tool.Result{}, fmt.Errorf("delete: %s: %w — nothing was removed", path, err)
		}
		if info.IsDir() {
			if !v.Dir {
				return tool.Result{}, fmt.Errorf(
					"delete: %s is a directory — pass `dir` true to remove an empty one; nothing was removed", path)
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				return tool.Result{}, fmt.Errorf("delete: %s: %w — nothing was removed", path, err)
			}
			if len(entries) > 0 {
				return tool.Result{}, fmt.Errorf(
					"delete: %s has %s in it — delete removes an empty directory, never a tree; nothing was removed",
					path, plural(len(entries), "thing"))
			}
		}
		list = append(list, doomed{path: path, dir: info.IsDir(), bytes: info.Size()})
	}

	for i, d := range list {
		if err := os.Remove(d.path); err != nil {
			// Past the first one this is no longer all-or-nothing, so
			// say exactly how far it got.
			if i == 0 {
				return tool.Result{}, fmt.Errorf("delete: %s: %w — nothing was removed", d.path, err)
			}
			return tool.Result{}, fmt.Errorf("delete: removed %s, then %s: %w — %s still there",
				names(list[:i]), d.path, err, plural(len(list)-i, "path"))
		}
	}

	if len(list) == 1 {
		return tool.Result{Text: "removed " + list[0].what()}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "removed %s:\n", plural(len(list), "path"))
	for _, d := range list {
		b.WriteString("  " + d.what() + "\n")
	}
	return tool.Result{Text: clipBytes(strings.TrimRight(b.String(), "\n"), MaxResult)}, nil
}

// names is a few paths in a sentence.
func names(list []doomed) string {
	out := make([]string, 0, len(list))
	for _, d := range list {
		out = append(out, d.path)
	}
	return strings.Join(out, ", ")
}
