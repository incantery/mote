package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/incantery/mote/tool"
)

// Run is a command, through sh -c, with its output streamed as it
// arrives. The streaming is the point: a build that takes two minutes
// is two minutes of a person watching a card fill up, not two minutes
// of a spinner.
type Run struct {
	Dir string
	// Shell is what runs the command. Empty means /bin/sh -c.
	Shell []string
}

type runArgs struct {
	Command string `json:"command"`
	Cwd     string `json:"cwd"`
	Timeout int    `json:"timeout"`
}

// DefaultTimeout and MaxTimeout bound how long a command may take. A
// command with no bound is a harness that hangs.
var (
	DefaultTimeout = 60 * time.Second
	MaxTimeout     = 10 * time.Minute
)

func (Run) Name() string { return "run" }

func (Run) Description() string {
	return "Run a shell command and return what it printed, with its exit status. " +
		"`cwd` is where; `timeout` is in seconds. Output is streamed as it arrives and " +
		"clipped in the answer if it is long."
}

func (Run) Schema() json.RawMessage {
	return schema(`{
  "type": "object",
  "properties": {
    "command": {"type": "string", "description": "The command line, as you would type it."},
    "cwd": {"type": "string", "description": "Where to run it. Default: the working directory."},
    "timeout": {"type": "integer", "description": "Seconds before it is killed. Default 60."}
  },
  "required": ["command"]
}`)
}

// Command is what a policy matches a prefix against.
func (Run) Command(args json.RawMessage) string {
	var v runArgs
	_ = json.Unmarshal(args, &v)
	return strings.TrimSpace(v.Command)
}

// Paths is where the command will run, when it says. A profile that
// wants "only in these directories" writes a path rule about run, and
// this is what it matches.
func (r Run) Paths(args json.RawMessage) []string {
	var v runArgs
	if json.Unmarshal(args, &v) != nil || strings.TrimSpace(v.Cwd) == "" {
		return nil
	}
	abs, err := resolve(r.Dir, v.Cwd)
	if err != nil {
		return nil
	}
	return []string{abs}
}

func (r Run) Run(ctx context.Context, args json.RawMessage, out io.Writer) (tool.Result, error) {
	var v runArgs
	if err := decode(r.Name(), args, &v); err != nil {
		return tool.Result{}, err
	}
	command := strings.TrimSpace(v.Command)
	if command == "" {
		return tool.Result{}, errors.New("run: no command")
	}
	dir := r.Dir
	if strings.TrimSpace(v.Cwd) != "" {
		abs, err := resolve(r.Dir, v.Cwd)
		if err != nil {
			return tool.Result{}, err
		}
		dir = abs
	}
	if dir == "" {
		// The header says where it ran, and "" is not a place.
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}

	timeout := DefaultTimeout
	if v.Timeout > 0 {
		timeout = min(time.Duration(v.Timeout)*time.Second, MaxTimeout)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shell := r.Shell
	if len(shell) == 0 {
		shell = []string{"/bin/sh", "-c"}
	}
	cmd := exec.CommandContext(ctx, shell[0], append(append([]string(nil), shell[1:]...), command)...)
	cmd.Dir = dir

	// One sink for both streams, because that is the order the person
	// would have seen at a terminal, and because a tool card is one
	// pane. It is written from the command's own goroutines, so it
	// holds a lock.
	sink := &tee{out: out}
	cmd.Stdout, cmd.Stderr = sink, sink

	started := time.Now()
	err := cmd.Run()
	took := time.Since(started)

	status := "exited 0"
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		status = fmt.Sprintf("killed after %s — it did not finish", timeout)
	case err != nil:
		var ee *exec.ExitError
		if asExit(err, &ee) {
			status = fmt.Sprintf("exited %d", ee.ExitCode())
		} else {
			return tool.Result{}, fmt.Errorf("run: %w", err)
		}
	}

	body := sink.String()
	head := fmt.Sprintf("$ %s\n(in %s — %s, %s)\n", command, dir, status, took.Round(time.Millisecond))
	if strings.TrimSpace(body) == "" {
		return tool.Result{Text: head + "(no output)"}, nil
	}
	text, _ := clipLines(splitLines(body), MaxLines, "lines")
	return tool.Result{Text: clipBytes(head+text, MaxResult)}, nil
}

// tee sends the command's output to the person as it arrives and
// keeps a bounded copy for the model. Bounded: a command that prints
// a gigabyte should not be held in memory to be clipped later.
type tee struct {
	out io.Writer

	mu   sync.Mutex
	buf  bytes.Buffer
	over int
}

func (t *tee) Write(p []byte) (int, error) {
	t.mu.Lock()
	if room := MaxResult*2 - t.buf.Len(); room > 0 {
		t.buf.Write(p[:min(room, len(p))])
		t.over += max(0, len(p)-room)
	} else {
		t.over += len(p)
	}
	t.mu.Unlock()
	if t.out != nil {
		if _, err := t.out.Write(p); err != nil {
			return len(p), nil // the watcher went away; the command has not
		}
	}
	return len(p), nil
}

func (t *tee) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.buf.String()
	if t.over > 0 {
		s += fmt.Sprintf("\n… and %s more that was streamed but not kept", size(t.over))
	}
	return s
}

// asExit is errors.As, spelled so that both callers read the same.
func asExit(err error, into **exec.ExitError) bool { return errors.As(err, into) }
