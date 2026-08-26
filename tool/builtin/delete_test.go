package builtin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree is a few files to remove, and the delete rooted at them.
func tree(t *testing.T) (string, Delete) {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "memory", "a.md"), "one\n")
	write(t, filepath.Join(dir, "memory", "b.md"), "two\n")
	write(t, filepath.Join(dir, "keep.md"), "kept\n")
	return dir, Delete{Dir: dir}
}

func gone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s is still there (%v)", path, err)
	}
}

func there(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("%s should still be there: %v", path, err)
	}
}

// One file: it goes, and the result says which and how much.
func TestDeleteOneFile(t *testing.T) {
	dir, d := tree(t)
	got := run(t, d, `{"path":"memory/a.md"}`)
	if !strings.HasPrefix(got, "removed ") {
		t.Fatalf("%q", got)
	}
	if !strings.Contains(got, filepath.Join(dir, "memory", "a.md")) {
		t.Fatalf("the result names the path it removed: %q", got)
	}
	if !strings.Contains(got, "4 B") {
		t.Fatalf("the result says how much went: %q", got)
	}
	gone(t, filepath.Join(dir, "memory", "a.md"))
	there(t, filepath.Join(dir, "memory", "b.md"))
}

// A few at once, because a curation retracts a handful of facts.
func TestDeleteSeveral(t *testing.T) {
	dir, d := tree(t)
	got := run(t, d, `{"paths":["memory/a.md","memory/b.md"]}`)
	if !strings.Contains(got, "removed 2 paths") {
		t.Fatalf("%q", got)
	}
	for _, name := range []string{"a.md", "b.md"} {
		if !strings.Contains(got, filepath.Join(dir, "memory", name)) {
			t.Errorf("the result names %s: %q", name, got)
		}
		gone(t, filepath.Join(dir, "memory", name))
	}
	there(t, filepath.Join(dir, "keep.md"))
}

// `path` and `paths` are one list, and the same path twice is removed
// once rather than failing the second time.
func TestDeleteTakesBothAndDeduplicates(t *testing.T) {
	dir, d := tree(t)
	got := run(t, d, `{"path":"memory/a.md","paths":["memory/b.md","memory/a.md"]}`)
	if !strings.Contains(got, "removed 2 paths") {
		t.Fatalf("%q", got)
	}
	gone(t, filepath.Join(dir, "memory", "a.md"))
	gone(t, filepath.Join(dir, "memory", "b.md"))
}

// Files only by default. The refusal says how to mean a directory,
// and says that nothing happened.
func TestDeleteRefusesADirectory(t *testing.T) {
	dir, d := tree(t)
	got := fails(t, d, `{"path":"memory"}`)
	for _, want := range []string{"is a directory", "`dir` true", "nothing was removed"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal should say %q: %q", want, got)
		}
	}
	there(t, filepath.Join(dir, "memory", "a.md"))
}

// An empty directory, when the call asks for one.
func TestDeleteAnEmptyDirectory(t *testing.T) {
	dir, d := tree(t)
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	got := run(t, d, `{"path":"empty","dir":true}`)
	if !strings.Contains(got, "an empty directory") || !strings.Contains(got, empty) {
		t.Fatalf("%q", got)
	}
	gone(t, empty)
}

// Never a tree, even with `dir` true.
func TestDeleteRefusesAFullDirectory(t *testing.T) {
	dir, d := tree(t)
	got := fails(t, d, `{"path":"memory","dir":true}`)
	for _, want := range []string{"2 things in it", "never a tree", "nothing was removed"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal should say %q: %q", want, got)
		}
	}
	there(t, filepath.Join(dir, "memory", "a.md"))
}

// A path that is not there is a refusal, not a silent success: a
// model told "removed" about a file that was never there has learned
// something false.
func TestDeleteMissingPath(t *testing.T) {
	_, d := tree(t)
	got := fails(t, d, `{"path":"memory/nope.md"}`)
	if !strings.Contains(got, "is not there") || !strings.Contains(got, "nothing was removed") {
		t.Fatalf("%q", got)
	}
}

// All of them or none. The second path cannot go, so the first one
// stays: half a curation is worse than none, because nothing says
// which half.
func TestDeleteIsAllOrNothing(t *testing.T) {
	dir, d := tree(t)
	got := fails(t, d, `{"paths":["memory/a.md","memory/nope.md"]}`)
	if !strings.Contains(got, "nothing was removed") {
		t.Fatalf("%q", got)
	}
	there(t, filepath.Join(dir, "memory", "a.md"))
}

// A symlink is a file, whatever it points at: the link goes and the
// target does not, and no `dir` is needed for a link to a directory.
func TestDeleteASymlinkTakesTheLinkOnly(t *testing.T) {
	dir, d := tree(t)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "memory"), link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	run(t, d, `{"path":"link"}`)
	gone(t, link)
	there(t, filepath.Join(dir, "memory", "a.md"))
}

// The caps, and the empty call.
func TestDeleteBounds(t *testing.T) {
	_, d := tree(t)
	if got := fails(t, d, `{}`); !strings.Contains(got, "no path") {
		t.Errorf("%q", got)
	}
	if got := fails(t, d, `{"path":"   "}`); !strings.Contains(got, "no path") {
		t.Errorf("%q", got)
	}
	many := make([]string, MaxPaths+1)
	for i := range many {
		many[i] = "f" + string(rune('a'+i%26)) + ".md"
	}
	buf, _ := json.Marshal(map[string]any{"paths": many})
	if got := fails(t, d, string(buf)); !strings.Contains(got, "nothing was removed") ||
		!strings.Contains(got, "more than one call takes") {
		t.Errorf("%q", got)
	}
	if got := fails(t, Delete{Dir: "/"}, `{"path":"/"}`); !strings.Contains(got, "root of a filesystem") {
		t.Errorf("%q", got)
	}
}

// Every path the call names is declared, so a rule about where this
// agent may delete sees all of them — and arguments that cannot be
// read declare nothing, which is the direction that does not allow.
func TestDeleteDeclaresItsPaths(t *testing.T) {
	dir, d := tree(t)
	got := d.Paths(json.RawMessage(`{"path":"a.md","paths":["memory/b.md","~"]}`))
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "a.md"), filepath.Join(dir, "memory", "b.md"), home}
	if len(got) != len(want) {
		t.Fatalf("%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("path %d = %q, want %q", i, got[i], want[i])
		}
	}
	if got := d.Paths(json.RawMessage(`{{{`)); got != nil {
		t.Errorf("unreadable arguments declared %v", got)
	}
	if got := d.Paths(json.RawMessage(`{}`)); got != nil {
		t.Errorf("no path declared %v", got)
	}
}
