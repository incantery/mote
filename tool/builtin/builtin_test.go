package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/incantery/mote/tool"
)

// repo is a small tree to work on, and the tools rooted at it.
func repo(t *testing.T) (string, map[string]tool.Tool) {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "README.md"), "# mote\n\nA small agent harness.\n")
	write(t, filepath.Join(dir, "agent", "agent.go"), "package agent\n\nfunc Send() {}\n\nfunc Done() {}\n")
	write(t, filepath.Join(dir, "tui", "tui.go"), "package tui\n\nfunc Run() {}\n")
	write(t, filepath.Join(dir, ".git", "config"), "[core]\n")
	byName := map[string]tool.Tool{}
	for _, tl := range New(dir) {
		byName[tl.Name()] = tl
	}
	return dir, byName
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, tl tool.Tool, args string) string {
	t.Helper()
	res, err := tl.Run(context.Background(), json.RawMessage(args), tool.Handle{})
	if err != nil {
		t.Fatalf("%s: %v", tl.Name(), err)
	}
	return res.Text
}

func fails(t *testing.T, tl tool.Tool, args string) string {
	t.Helper()
	_, err := tl.Run(context.Background(), json.RawMessage(args), tool.Handle{})
	if err == nil {
		t.Fatalf("%s: wanted an error", tl.Name())
	}
	return err.Error()
}

func TestReadWholeAndRange(t *testing.T) {
	dir, tools := repo(t)
	whole := run(t, tools["read"], `{"path":"README.md"}`)
	if !strings.Contains(whole, "A small agent harness.") {
		t.Fatalf("%q", whole)
	}
	if !strings.Contains(whole, "3 lines") {
		t.Fatalf("the header says how much there is: %q", whole)
	}
	part := run(t, tools["read"], `{"path":"README.md","from":3,"to":3}`)
	if strings.Contains(part, "# mote") || !strings.Contains(part, "A small agent harness.") {
		t.Fatalf("%q", part)
	}
	if !strings.Contains(part, "lines 3–3 of 3") {
		t.Fatalf("a range says which: %q", part)
	}
	// Absolute paths work too, and a directory is not a file.
	run(t, tools["read"], `{"path":`+quote(filepath.Join(dir, "README.md"))+`}`)
	if got := fails(t, tools["read"], `{"path":"agent"}`); !strings.Contains(got, "use list") {
		t.Fatalf("%q", got)
	}
	if got := fails(t, tools["read"], `{"path":"nope.md"}`); !strings.Contains(got, "no such file") {
		t.Fatalf("%q", got)
	}
	// Past the end is an answer, not a failure.
	if got := run(t, tools["read"], `{"path":"README.md","from":99}`); !strings.Contains(got, "no line 99") {
		t.Fatalf("%q", got)
	}
}

func TestReadClipsALongFile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "long.txt"), strings.Repeat("a line\n", MaxLines+50))
	got := run(t, Read{Dir: dir}, `{"path":"long.txt"}`)
	if !strings.Contains(got, "… 50 more lines") {
		t.Fatalf("a clip says so: %q", got[len(got)-120:])
	}
}

func TestWriteCreatesParents(t *testing.T) {
	dir := t.TempDir()
	w := Write{Dir: dir}
	got := run(t, w, `{"path":"a/b/c.md","content":"hello\n"}`)
	if !strings.Contains(got, "created") {
		t.Fatalf("%q", got)
	}
	body, err := os.ReadFile(filepath.Join(dir, "a", "b", "c.md"))
	if err != nil || string(body) != "hello\n" {
		t.Fatalf("%q %v", body, err)
	}
	if got := run(t, w, `{"path":"a/b/c.md","content":"again\n"}`); !strings.Contains(got, "replaced") {
		t.Fatalf("the second time it is replaced, and says so: %q", got)
	}
}

func TestEditIsExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	write(t, path, "package p\n\nfunc A() {}\nfunc B() {}\n")
	e := Edit{Dir: dir}

	got := run(t, e, `{"path":"f.go","old":"func A() {}","new":"func A() { return }"}`)
	if !strings.Contains(got, "line 3") {
		t.Fatalf("it says where: %q", got)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "func A() { return }") {
		t.Fatalf("%q", body)
	}

	if got := fails(t, e, `{"path":"f.go","old":"nowhere","new":"x"}`); !strings.Contains(got, "not in") {
		t.Fatalf("%q", got)
	}
	write(t, path, "x\nx\n")
	if got := fails(t, e, `{"path":"f.go","old":"x","new":"y"}`); !strings.Contains(got, "2 times") {
		t.Fatalf("%q", got)
	}
	if got := fails(t, e, `{"path":"f.go","old":"","new":"y"}`); !strings.Contains(got, "use write") {
		t.Fatalf("%q", got)
	}
}

// An edit keeps the file's mode: a script that was executable still is.
func TestEditKeepsTheMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.sh")
	write(t, path, "echo one\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, Edit{Dir: dir}, `{"path":"s.sh","old":"one","new":"two"}`)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("%v %v", info.Mode(), err)
	}
}

func TestList(t *testing.T) {
	_, tools := repo(t)
	shallow := run(t, tools["list"], `{"dir":"."}`)
	for _, want := range []string{"README.md", "agent/", "tui/"} {
		if !strings.Contains(shallow, want) {
			t.Errorf("%q is missing %q", shallow, want)
		}
	}
	if strings.Contains(shallow, "agent/agent.go") {
		t.Error("depth 1 does not descend")
	}
	if strings.Contains(shallow, ".git") {
		t.Error(".git is skipped")
	}
	deep := run(t, tools["list"], `{"dir":".","depth":2}`)
	if !strings.Contains(deep, "agent/agent.go") {
		t.Errorf("depth 2 descends: %q", deep)
	}
}

func TestSearchBothWays(t *testing.T) {
	dir, _ := repo(t)
	for _, s := range []Search{{Dir: dir}, {Dir: dir, RG: "-"}} {
		name := "ripgrep-if-present"
		if s.RG == "-" {
			name = "go"
		}
		t.Run(name, func(t *testing.T) {
			got := run(t, s, `{"pattern":"func Send","dir":"."}`)
			if !strings.Contains(got, "agent.go:3") {
				t.Fatalf("%q", got)
			}
			globbed := run(t, s, `{"pattern":"func ","dir":".","glob":"**/tui/*.go"}`)
			if strings.Contains(globbed, "agent.go") || !strings.Contains(globbed, "tui.go") {
				t.Fatalf("the glob narrows it: %q", globbed)
			}
			if got := run(t, s, `{"pattern":"zzznothing","dir":"."}`); !strings.Contains(got, "no match") {
				t.Fatalf("%q", got)
			}
		})
	}
}

func TestSearchNeedsAPattern(t *testing.T) {
	dir, _ := repo(t)
	fails(t, Search{Dir: dir}, `{"dir":"."}`)
}

// A pattern Go cannot compile is an error from the fallback, not a
// silent empty answer.
func TestSearchBadPattern(t *testing.T) {
	dir, _ := repo(t)
	fails(t, Search{Dir: dir, RG: "-"}, `{"pattern":"(","dir":"."}`)
}

func TestRunStreamsAndReportsStatus(t *testing.T) {
	dir := t.TempDir()
	var streamed strings.Builder
	res, err := Run{Dir: dir}.Run(context.Background(),
		json.RawMessage(`{"command":"echo one; echo two 1>&2"}`), tool.Handle{Output: &streamed})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(streamed.String(), "one") || !strings.Contains(streamed.String(), "two") {
		t.Fatalf("streamed %q", streamed.String())
	}
	if !strings.Contains(res.Text, "exited 0") {
		t.Fatalf("%q", res.Text)
	}
}

// A command that exits 1 is a result, not an error: the model can
// work with "exited 1", and cannot work with a Go error.
func TestRunNonZeroIsAResult(t *testing.T) {
	res, err := Run{Dir: t.TempDir()}.Run(context.Background(),
		json.RawMessage(`{"command":"echo nope 1>&2; exit 3"}`), tool.Handle{})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(res.Text, "exited 3") || !strings.Contains(res.Text, "nope") {
		t.Fatalf("%q", res.Text)
	}
}

func TestRunTimesOut(t *testing.T) {
	res, err := Run{Dir: t.TempDir()}.Run(context.Background(),
		json.RawMessage(`{"command":"sleep 5","timeout":1}`), tool.Handle{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "did not finish") {
		t.Fatalf("%q", res.Text)
	}
}

func TestRunSaysWhatItRanAndWhere(t *testing.T) {
	dir := t.TempDir()
	res, _ := Run{Dir: dir}.Run(context.Background(), json.RawMessage(`{"command":"true"}`), tool.Handle{})
	if !strings.Contains(res.Text, "$ true") || !strings.Contains(res.Text, dir) {
		t.Fatalf("%q", res.Text)
	}
	if !strings.Contains(res.Text, "(no output)") {
		t.Fatalf("silence is said out loud: %q", res.Text)
	}
}

// What a policy reads off each tool. This is the whole of the contract
// between a profile's rules and the tools they are about.
func TestWhatTheToolsDeclare(t *testing.T) {
	dir := t.TempDir()
	tools := map[string]tool.Tool{}
	for _, tl := range New(dir) {
		tools[tl.Name()] = tl
	}
	cases := []struct {
		tool  string
		args  string
		paths []string
		cmd   string
	}{
		{"read", `{"path":"a/b.md"}`, []string{filepath.Join(dir, "a/b.md")}, ""},
		{"write", `{"path":"/tmp/x"}`, []string{"/tmp/x"}, ""},
		{"edit", `{"path":"../out.md"}`, []string{filepath.Join(filepath.Dir(dir), "out.md")}, ""},
		{"list", `{"dir":"sub"}`, []string{filepath.Join(dir, "sub")}, ""},
		{"search", `{"pattern":"x"}`, []string{dir}, ""},
		{"run", `{"command":"git status --short"}`, nil, "git status --short"},
		{"run", `{"command":"ls","cwd":"sub"}`, []string{filepath.Join(dir, "sub")}, "ls"},
		{"write", `not json at all`, nil, ""},
	}
	for _, c := range cases {
		tl := tools[c.tool]
		got := tool.Paths(tl, json.RawMessage(c.args))
		if strings.Join(got, ",") != strings.Join(c.paths, ",") {
			t.Errorf("%s %s: paths %v, want %v", c.tool, c.args, got, c.paths)
		}
		if got := tool.Command(tl, json.RawMessage(c.args)); got != c.cmd {
			t.Errorf("%s %s: command %q, want %q", c.tool, c.args, got, c.cmd)
		}
	}
}

// Every built-in says plainly what it did, in a sentence that names
// what it did it to. A result the model has to guess at is how a
// refusal gets read as a success — so a tool that changed something
// leads with the past tense and the path.
func TestResultsSayWhatHappened(t *testing.T) {
	dir, tools := repo(t)
	cases := []struct {
		tool, args string
		want       []string
	}{
		{"read", `{"path":"README.md"}`, []string{filepath.Join(dir, "README.md"), "3 lines"}},
		{"list", `{"dir":"agent"}`, []string{filepath.Join(dir, "agent"), "entries"}},
		{"search", `{"pattern":"func Send","dir":"agent"}`,
			[]string{filepath.Join(dir, "agent"), "matches"}},
		{"write", `{"path":"new.md","content":"x\n"}`,
			[]string{"created ", filepath.Join(dir, "new.md")}},
		{"write", `{"path":"README.md","content":"x\n"}`,
			[]string{"replaced ", filepath.Join(dir, "README.md")}},
		{"edit", `{"path":"agent/agent.go","old":"func Done() {}","new":"func Done() { return }"}`,
			[]string{"edited ", filepath.Join(dir, "agent", "agent.go"), "line 5"}},
		{"delete", `{"path":"tui/tui.go"}`, []string{"removed ", filepath.Join(dir, "tui", "tui.go")}},
		{"run", `{"command":"echo hi"}`, []string{"echo hi", dir, "exited 0"}},
	}
	for _, c := range cases {
		got := run(t, tools[c.tool], c.args)
		if strings.TrimSpace(got) == "" {
			t.Errorf("%s said nothing", c.tool)
			continue
		}
		for _, want := range c.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s %s: the result should say %q:\n%s", c.tool, c.args, want, got)
			}
		}
	}
}

// A search that found nothing, a listing of an empty directory and a
// command that printed nothing are answers, not silences.
func TestEmptyResultsStillSaySo(t *testing.T) {
	dir, tools := repo(t)
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		tool, args, want string
	}{
		{"search", `{"pattern":"zzzz","dir":"agent"}`, "no match"},
		{"list", `{"dir":"empty"}`, "is empty"},
		{"run", `{"command":"true"}`, "(no output)"},
		{"read", `{"path":"README.md","from":99}`, "no line 99"},
	} {
		if got := run(t, tools[c.tool], c.args); !strings.Contains(got, c.want) {
			t.Errorf("%s: the result should say %q: %q", c.tool, c.want, got)
		}
	}
}

// Every built-in's schema is JSON Schema that parses, and the
// registry hands them to the model in the wire shape.
func TestSchemasAreValidJSON(t *testing.T) {
	r := Registry(t.TempDir())
	if got := len(r.Definitions()); got != 7 {
		t.Fatalf("%d definitions", got)
	}
	for _, d := range r.Definitions() {
		var into map[string]any
		if err := json.Unmarshal(d.Function.Parameters, &into); err != nil {
			t.Errorf("%s: %v", d.Function.Name, err)
		}
		if into["type"] != "object" {
			t.Errorf("%s: schema is not an object", d.Function.Name)
		}
		if d.Function.Description == "" {
			t.Errorf("%s: no description", d.Function.Name)
		}
	}
}

// Arguments that are not JSON are an error the model can read, not a
// panic and not a silent no-op.
func TestUnreadableArguments(t *testing.T) {
	dir := t.TempDir()
	for _, tl := range New(dir) {
		if _, err := tl.Run(context.Background(), json.RawMessage(`{{{`), tool.Handle{}); err == nil {
			t.Errorf("%s took nonsense", tl.Name())
		}
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
