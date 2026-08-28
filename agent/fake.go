package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Step is one scripted event and how long to wait before it.
type Step struct {
	After time.Duration
	Event Event
	// Then belongs to a KindAsk step. The Fake sends the ask, stops,
	// and does not go on until somebody answers; Then is handed the
	// answer and returns what happens next, in place of nothing. It
	// is how a script has two endings without a second script.
	Then func(choice string) []Step
}

// Fake is an agent that replays a script. It exists for two audiences
// that want opposite things: a demo, which wants the pauses and the
// typing that make a terminal feel alive, and a test, which wants the
// same bytes every time. Instant is the switch between them — with it
// set, every delay is zero and the event sequence is a pure function
// of the turn number and the text.
type Fake struct {
	// Instant drops every delay. Set it in tests.
	Instant bool
	// Script chooses the steps for a turn. Nil means DefaultScript.
	Script func(turn int, conversation, text string) []Step

	mu      sync.Mutex
	turn    int
	pending map[string]chan string
}

// Turn is how many exchanges this Fake has served. Scripts are chosen
// by it, so a test that wants a particular scene can set it.
func (f *Fake) Turn() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.turn
}

// SetTurn winds the counter to n.
func (f *Fake) SetTurn(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.turn = n
}

// Answer is the person's word on an ask the script is waiting for.
// It satisfies agent.Answerer, which is what makes the Fake a whole
// agent for a terminal that shows ask cards.
//
// An answer to an id nothing is waiting on is dropped: the turn ended
// while the key was on its way down, which is not a failure.
func (f *Fake) Answer(ctx context.Context, id, choice string) error {
	switch choice {
	case Yes, No, Always:
	default:
		return errors.New("no such answer " + choice)
	}
	f.mu.Lock()
	ch, ok := f.pending[id]
	delete(f.pending, id)
	f.mu.Unlock()
	if !ok {
		return nil
	}
	select {
	case ch <- choice:
	default:
	}
	return nil
}

// wait parks the script on an ask. A cancelled exchange is a no —
// there is nobody left to answer.
func (f *Fake) wait(ctx context.Context, id string) string {
	ch := make(chan string, 1)
	f.mu.Lock()
	if f.pending == nil {
		f.pending = map[string]chan string{}
	}
	f.pending[id] = ch
	f.mu.Unlock()
	select {
	case <-ctx.Done():
		f.mu.Lock()
		delete(f.pending, id)
		f.mu.Unlock()
		return No
	case choice := <-ch:
		return choice
	}
}

// Send replays this turn's script. It honours ctx: a cancelled
// exchange stops where it is and still says done, so the terminal
// never waits on a turn that will not end.
func (f *Fake) Send(ctx context.Context, conversation, text string) (<-chan Event, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("nothing to say")
	}
	f.mu.Lock()
	turn := f.turn
	f.turn++
	f.mu.Unlock()

	script := f.Script
	if script == nil {
		script = DefaultScript
	}
	steps := script(turn, conversation, text)
	if n := len(steps); n == 0 || steps[n-1].Event.Kind != KindDone {
		steps = append(steps, Step{Event: Done()})
	}

	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		if !f.play(ctx, ch, steps) {
			ch <- Done()
		}
	}()
	return ch, nil
}

// play sends the steps, waiting where a step says to. It reports
// whether it got to the end; a run that did not has to say done
// itself, because the terminal is waiting for one.
func (f *Fake) play(ctx context.Context, ch chan<- Event, steps []Step) bool {
	for _, s := range steps {
		if !f.Instant && s.After > 0 {
			t := time.NewTimer(s.After)
			select {
			case <-ctx.Done():
				t.Stop()
				return false
			case <-t.C:
			}
		}
		select {
		case <-ctx.Done():
			return false
		case ch <- s.Event:
		}
		if s.Event.Kind != KindAsk {
			continue
		}
		// The script stops here until the terminal says something.
		choice := f.wait(ctx, s.Event.ID)
		if ctx.Err() != nil {
			return false
		}
		if s.Then != nil && !f.play(ctx, ch, s.Then(choice)) {
			return false
		}
	}
	return true
}

// DefaultScript picks a scene. A word in the text wins — "error" or
// "fail" for the failure, "test", "build" or "stream" for the tool
// that talks while it runs, "policy" or "permission" for the one that
// stops and asks, "notice" or "fleet" for the burst from outside,
// "tool" or "run" for the tool round — and otherwise
// the turns cycle, so that ten seconds of typing anything at all
// shows the whole vocabulary.
func DefaultScript(turn int, conversation, text string) []Step {
	switch low := strings.ToLower(text); {
	case strings.Contains(low, "error"), strings.Contains(low, "fail"):
		return ErrorScene(text)
	case strings.Contains(low, "stream"), strings.Contains(low, "build"), strings.Contains(low, "test"):
		return StreamScene(text)
	case strings.Contains(low, "policy"), strings.Contains(low, "permission"):
		return AskScene(text)
	case strings.Contains(low, "notice"), strings.Contains(low, "fleet"):
		return NoticeScene(text)
	case strings.Contains(low, "tool"), strings.Contains(low, "run"):
		return ToolScene(text)
	}
	switch turn % 6 {
	case 1:
		return ToolScene(text)
	case 2:
		return ErrorScene(text)
	case 3:
		return StreamScene(text)
	case 4:
		return AskScene(text)
	case 5:
		return NoticeScene(text)
	}
	return MarkdownScene(text)
}

// MarkdownScene streams a reply that exercises the renderer: headings,
// prose, a list, a fenced block, and a table.
func MarkdownScene(text string) []Step {
	steps := []Step{
		{After: 220 * time.Millisecond, Event: Status("thinking")},
		{After: 400 * time.Millisecond, Event: Status("drafting a reply")},
	}
	return append(steps, stream(markdownReply, 320*time.Millisecond, 45*time.Millisecond)...)
}

// ToolScene is a tool round: a call, a status line while it runs, a
// result after a delay — and a notice from elsewhere arriving in the
// middle of it, which is the case the transcript has to get right.
func ToolScene(text string) []Step {
	steps := []Step{
		{After: 180 * time.Millisecond, Event: Status("looking at the repository")},
		{After: 350 * time.Millisecond, Event: Call("call_1", "read_file",
			`{"path":"README.md","limit":200}`).
			WithSummary("read the first 200 lines of README.md")},
		{After: 250 * time.Millisecond, Event: Status("reading README.md")},
		{After: 900 * time.Millisecond, Event: Result("call_1", readFileResult,
			1420*time.Millisecond, 0.0021)},
		{After: 200 * time.Millisecond, Event: Notice("task 184a1100 finished — /report 184a1100")},
		{After: 300 * time.Millisecond, Event: Call("call_2", "grep",
			`{"pattern":"func Send","glob":"**/*.go"}`).
			WithSummary("searched every .go file for func Send")},
		{After: 700 * time.Millisecond, Event: Result("call_2", grepResult, 310*time.Millisecond, 0.0004)},
		{After: 250 * time.Millisecond, Event: Status("writing it up")},
	}
	return append(steps, stream(toolReply, 300*time.Millisecond, 40*time.Millisecond)...)
}

// StreamScene is a tool that talks while it works: one call, its
// output arriving line by line, then the result that ends it. The
// result is empty on purpose — everything the command had to say it
// already said, and the card keeps what it streamed.
func StreamScene(text string) []Step {
	steps := []Step{
		{After: 180 * time.Millisecond, Event: Status("running the tests")},
		{After: 300 * time.Millisecond, Event: Call("call_1", "shell",
			`{"cmd":"go test ./... -race","dir":"/src/mote"}`).
			WithSummary("ran the tests under -race")},
	}
	for _, l := range strings.SplitAfter(testOutput, "\n") {
		if l == "" {
			continue
		}
		steps = append(steps, Step{After: 260 * time.Millisecond, Event: Output("call_1", l)})
	}
	steps = append(steps,
		Step{After: 400 * time.Millisecond, Event: Result("call_1", "", 9840*time.Millisecond, 0.0007)},
		Step{After: 250 * time.Millisecond, Event: Status("writing it up")},
	)
	steps = append(steps, stream(streamReply, 300*time.Millisecond, 40*time.Millisecond)...)
	return append(steps, Step{After: 200 * time.Millisecond, Event: Spent(0.0138, 18422, 611)})
}

// AskScene is a tool the policy will not run without a word: one call
// that is allowed outright, then one the profile says to ask about.
// The scene has two endings, and which one it has is the person's.
func AskScene(text string) []Step {
	return []Step{
		{After: 180 * time.Millisecond, Event: Status("checking the policy")},
		{After: 300 * time.Millisecond, Event: Call("call_1", "read",
			`{"path":"GAPS.md","from":1,"to":40}`)},
		{After: 600 * time.Millisecond, Event: Result("call_1", readFileResult,
			240*time.Millisecond, 0)},
		{
			After: 350 * time.Millisecond,
			Event: Asking("call_2", "write",
				`{"path":"/tmp/scratch/notes.md","content":"# what the policy decided\n"}`,
				"outside ~/vera and not a project — the profile says ask"),
			Then: answered,
		},
	}
}

// answered is what happens after the ask, either way. A no is not an
// error: the person answered, and the model is told what they said.
func answered(choice string) []Step {
	if choice == No {
		return append([]Step{
			{After: 150 * time.Millisecond, Event: Result("call_2", "declined", 0, 0)},
		}, stream(declinedReply, 250*time.Millisecond, 40*time.Millisecond)...)
	}
	steps := []Step{
		{After: 150 * time.Millisecond, Event: Result("call_2",
			"created /tmp/scratch/notes.md — 34 B, 1 line", 3*time.Millisecond, 0)},
	}
	reply := allowedReply
	if choice == Always {
		reply = alwaysReply
	}
	return append(steps, stream(reply, 250*time.Millisecond, 40*time.Millisecond)...)
}

// NoticeScene is the world talking over an exchange: a short answer
// with a burst of notices landing in the middle of it, each about a
// different task. It is the case the transcript has to keep separate
// — three things that happened elsewhere are not three things the
// model said, and they belong together rather than one per paragraph.
func NoticeScene(text string) []Step {
	steps := []Step{
		{After: 180 * time.Millisecond, Event: Status("catching up")},
	}
	steps = append(steps, stream(noticeReply, 300*time.Millisecond, 40*time.Millisecond)...)
	steps = append(steps,
		Step{After: 400 * time.Millisecond, Event: About("05a40191",
			"05a40191 is working — ship the price table")},
		Step{After: 120 * time.Millisecond, Event: About("c41f9a02",
			"c41f9a02 finished — /report c41f9a02")},
		Step{After: 120 * time.Millisecond, Event: About("7b20e5d9",
			"7b20e5d9 needs you — a question on the tool registry")},
		Step{After: 200 * time.Millisecond, Event: Status("writing it up")},
	)
	steps = append(steps, stream(noticeTail, 300*time.Millisecond, 40*time.Millisecond)...)
	return append(steps, Step{After: 150 * time.Millisecond, Event: Spent(0.0041, 8210, 190)})
}

// ErrorScene starts a reply and then fails partway, which is the shape
// a real provider timeout has: some text, then nothing good.
func ErrorScene(text string) []Step {
	steps := []Step{
		{After: 200 * time.Millisecond, Event: Status("thinking")},
	}
	steps = append(steps, stream(errorReply, 300*time.Millisecond, 45*time.Millisecond)...)
	return append(steps,
		Step{After: 500 * time.Millisecond, Event: Fail("upstream: 429 rate limited — retry in 12s")},
	)
}

// stream cuts markdown into deltas the way a model emits them: a few
// words at a time, on word boundaries so the wrap does not jitter.
// The cut is a pure function of the text, so two runs agree.
func stream(md string, first, gap time.Duration) []Step {
	var steps []Step
	for i, c := range chunks(md, 14) {
		d := gap
		if i == 0 {
			d = first
		}
		steps = append(steps, Step{After: d, Event: Delta(c)})
	}
	return steps
}

// chunks splits s into pieces of about n runes, preferring to end on
// whitespace and always ending a piece after a newline.
func chunks(s string, n int) []string {
	var out []string
	runes := []rune(s)
	for i := 0; i < len(runes); {
		end := min(i+n, len(runes))
		// Extend to the next space so words stay whole; stop at a
		// newline first, so block structure arrives block by block.
		for end < len(runes) && !isBreak(runes[end-1]) {
			end++
		}
		for j := i; j < end; j++ {
			if runes[j] == '\n' {
				end = j + 1
				break
			}
		}
		out = append(out, string(runes[i:end]))
		i = end
	}
	return out
}

func isBreak(r rune) bool { return r == ' ' || r == '\n' || r == '\t' }

const markdownReply = `## What a harness is

A harness is a loop over a model with tools, a record of what
happened, and a terminal to watch it in. Everything else — the
prompt, the tools, what those tools may touch — is a *profile*.

The pieces, in the order they matter:

1. ` + "`agent`" + ` — the loop, and the seam this terminal speaks to.
2. ` + "`tool`" + ` — a registry with policy: auto, ask, or deny.
3. ` + "`session`" + ` — the record on disk, resumable and dumpable.
4. ` + "`tui`" + ` — what you are looking at.

The seam is one method:

` + "```go" + `
type Agent interface {
	Send(ctx context.Context, conversation, text string) (<-chan Event, error)
}
` + "```" + `

Which is enough for both kinds of agent:

| agent     | where it runs | transport |
| --------- | ------------- | --------- |
| mote demo | in process    | a channel |
| vera      | verad         | HTTP      |
| a coding agent | a room   | HTTP      |

> A profile is a directory a person can read.
`

const toolReply = `Read it. ` + "`README.md`" + ` says the first milestone is the
terminal, behind an interface, with Vera's chat as the first client —
and ` + "`Send`" + ` is the only method either side has to agree on.

- **agent** holds the seam and a scripted ` + "`Fake`" + `.
- **tui** holds the terminal, and knows nothing about providers.

Next: the ` + "`tool`" + ` registry, so a profile can say *ask* by path.
`

const noticeReply = `Three of them moved while you were away — here they
are, as they land:
`

const noticeTail = `The scout is **done** and nobody has read it: ` + "`/report c41f9a02`" + `.
The other one is waiting on a word from you.
`

const errorReply = `Let me look at the provider's rate limits before I
answer that — the last three calls all came back slower than
`

const testOutput = `go: downloading charm.land/bubbletea/v2 v2.0.9
=== RUN   TestFakeIsDeterministic
--- PASS: TestFakeIsDeterministic (0.00s)
=== RUN   TestFakeDeltasReassemble
--- PASS: TestFakeDeltasReassemble (0.00s)
=== RUN   TestFakeScenes
=== RUN   TestFakeScenes/markdown
=== RUN   TestFakeScenes/tools
=== RUN   TestFakeScenes/stream
--- PASS: TestFakeScenes (0.01s)
ok  	github.com/incantery/mote/agent	1.204s
ok  	github.com/incantery/mote/session	0.311s
ok  	github.com/incantery/mote/tui	8.325s`

const streamReply = `Green, under ` + "`-race`" + `. The slow one is ` + "`tui`" + ` — the golden
files render markdown at three widths, and glamour builds a syntax
highlighter for each.

Nothing to fix.
`

const declinedReply = `Left it alone. Nothing was written.

If you want it kept somewhere I may write without asking, ` + "`~/vera`" + `
is that place — the profile says so, and the policy enforces it.
`

const allowedReply = `Written. That one was outside ` + "`~/vera`" + ` and not in a
project, so the profile said **ask** rather than deciding for you.

The next one asks too. ` + "`a`" + ` instead of ` + "`y`" + ` would have stopped it asking
about that directory for the rest of the session.
`

const alwaysReply = `Written — and I will not ask again about
` + "`/tmp/scratch`" + ` this session.

The grant is the directory and the tool, not the file: ` + "`write`" + ` under
` + "`/tmp/scratch`" + ` and everything below it. A different tool, or a
different directory, is a fresh question.
`

const readFileResult = `# mote

A small agent harness, in Go. The thing Vera is built on; usable
without her.

## What it is

pi's shape, ours: a harness is a loop over a model with tools, a
record of what happened, and a terminal to watch it in. Everything
that makes one agent different from another — its prompt, which
tools it has, what those tools are allowed to touch — is a *profile*,
not code in the loop.

## Pieces

- ` + "`agent`" + ` — the loop: messages, tool rounds, streaming, providers.
- ` + "`tool`" + ` — a registry with policy.
- ` + "`session`" + ` — the record on disk.
- ` + "`tui`" + ` — the terminal.
- ` + "`cmd/mote`" + ` — the harness alone, with a profile directory.

## First milestone

The TUI, behind an interface, with vera's chat as the first client.
(200 lines elided)`

const grepResult = `agent/agent.go:78:	Send(ctx context.Context, conversation, text string) (<-chan Event, error)
agent/fake.go:52:func (f *Fake) Send(ctx context.Context, conversation, text string) (<-chan Event, error) {
tui/model.go:214:	ch, err := m.agent.Send(ctx, m.conversation, text)`
