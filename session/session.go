// Package session is a conversation on disk.
//
// A terminal that forgets everything when the process ends is a demo.
// This is the record that outlives it: what the person said, what the
// agent answered, every tool it reached for and what came back — kept
// so that reopening a conversation redraws the transcript exactly as
// it was — and the input history to go with it.
//
// Vera's journal is the seed: one file per conversation, one line per
// exchange, append-only, written when the exchange ends whether it
// succeeded or not. What a line holds is the difference. A journal
// entry is for reading later — "why did it do that?" — and so keeps
// the system prompt and the timings. A session entry is for redrawing,
// and so keeps the agent's events in the order the terminal folded
// them in.
//
// # The file
//
// One file per conversation, dir/<conversation>.jsonl, the id
// sanitised to a file name. One JSON object per line, appended, never
// rewritten. A line that does not parse is skipped, so a half-written
// last line cannot hide the rest of the file. Every line has a type:
//
//	{"type":"open","at":"2026-08-25T19:40:00Z","v":1,"conversation":"demo-1"}
//
// written once, when the file is created — a header a person can read
// with head -1, and the version to change the format against.
//
//	{"type":"input","at":"…","text":"/report 184a1100"}
//
// one line the person sent, commands and all: this is the input
// history. It is written when the line is sent, not when the exchange
// ends, so a conversation that ends badly still remembers what was
// typed into it.
//
//	{"type":"turn","at":"…","ended":"…","said":"tell me about tools",
//	 "events":[…],"cost":0.0042,"input_tokens":1200,"output_tokens":310}
//
// one exchange: what was said, and everything until the reply was
// done. Events are agent.Events in order, with two coalescings — the
// deltas of a reply become one delta holding the finished text, and
// the pieces of a tool's streamed output become one tool_output. Tool
// calls, results, notices and errors are kept as they were, because a
// transcript shows them as they were. There is no done event: a turn
// record exists because the turn ended, and what done carried is in
// cost and the token counts.
//
// What is deliberately not here: notices that arrived between
// exchanges, and blocks an application printed with tui.Show. Those
// are the chrome around a conversation rather than the conversation
// itself, and a reopened transcript should not replay yesterday's
// fleet chatter.
package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/incantery/mote/agent"
)

// Version is the format's. It goes in the open record of every file
// this package creates.
const Version = 1

// historyLimit is how much of a long conversation's input history is
// loaded. The file keeps every line; the box in front of a person does
// not need ten thousand of them.
const historyLimit = 500

// Turn is one exchange: what the person said, and everything the agent
// did until the reply was done.
type Turn struct {
	// At is when the person sent it; Ended when the turn finished.
	At    time.Time `json:"at"`
	Ended time.Time `json:"ended,omitempty"`
	// Said is the person's text. A turn always has one.
	Said string `json:"said"`
	// Events are the agent's, in order, coalesced as the package
	// comment describes.
	Events []agent.Event `json:"events,omitempty"`
	// Cost is the whole turn in USD: the tool costs in Events plus
	// whatever the done event said the model itself spent. Zero means
	// nobody knew.
	Cost float64 `json:"cost,omitempty"`
	// InputTokens and OutputTokens are the turn's, if the agent
	// reported them on done.
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

// Took is how long the turn ran, or zero if it never ended.
func (t Turn) Took() time.Duration {
	if t.Ended.IsZero() || t.At.IsZero() {
		return 0
	}
	return t.Ended.Sub(t.At)
}

// Answered is the reply text, the deltas joined. Everything else in
// the turn — the cards, the notices — is in Events.
func (t Turn) Answered() string {
	var b strings.Builder
	for _, ev := range t.Events {
		if ev.Kind == agent.KindDelta {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

// line is one record in the file. It is a union rather than three
// types because the file is meant to be read with head and jq as much
// as with this package.
type line struct {
	Type         string        `json:"type"`
	At           time.Time     `json:"at"`
	Version      int           `json:"v,omitempty"`
	Conversation string        `json:"conversation,omitempty"`
	Text         string        `json:"text,omitempty"`
	Said         string        `json:"said,omitempty"`
	Ended        time.Time     `json:"ended,omitempty"`
	Events       []agent.Event `json:"events,omitempty"`
	Cost         float64       `json:"cost,omitempty"`
	InputTokens  int           `json:"input_tokens,omitempty"`
	OutputTokens int           `json:"output_tokens,omitempty"`
}

// Session is one conversation's file, open for appending. It is safe
// for concurrent use; the terminal appends from the UI goroutine and
// reads from wherever it likes.
type Session struct {
	id   string
	path string

	mu      sync.Mutex
	f       *os.File
	turns   []Turn
	history []string
}

// Open reads a conversation's file if it is there and opens it for
// appending. A conversation that has never been said to is created,
// header and all, on the first append rather than now — opening a
// session must not litter the directory with empty files.
func Open(dir, conversation string) (*Session, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Session{id: conversation, path: Path(dir, conversation)}
	f, err := read(s.path)
	if err != nil {
		return nil, err
	}
	s.turns, s.history = f.turns, f.history
	return s, nil
}

// ID is the conversation this session records.
func (s *Session) ID() string { return s.id }

// Path is the file it writes to.
func (s *Session) Path() string { return s.path }

// Turns is every exchange in the file, oldest first.
func (s *Session) Turns() []Turn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Turn(nil), s.turns...)
}

// History is every line the person sent, oldest last — what the up
// arrow walks. The last historyLimit of them, for a very long file.
func (s *Session) History() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.history...)
}

// Append records one finished exchange.
func (s *Session) Append(t Turn) error {
	if t.At.IsZero() {
		t.At = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turns = append(s.turns, t)
	return s.write(line{
		Type: "turn", At: t.At, Ended: t.Ended, Said: t.Said, Events: t.Events,
		Cost: t.Cost, InputTokens: t.InputTokens, OutputTokens: t.OutputTokens,
	})
}

// Remember records one line the person sent. A repeat of the last one
// is dropped, which is what the history does in front of them.
func (s *Session) Remember(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(s.history); n > 0 && s.history[n-1] == text {
		return nil
	}
	s.history = append(s.history, text)
	if len(s.history) > historyLimit {
		s.history = s.history[len(s.history)-historyLimit:]
	}
	return s.write(line{Type: "input", At: time.Now(), Text: text})
}

// Close releases the file. A closed session can still be read.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	f := s.f
	s.f = nil
	return f.Close()
}

// write appends one record. The file is opened on first use so that a
// session nobody says anything to leaves nothing behind.
func (s *Session) write(l line) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	if s.f == nil {
		fresh := false
		if _, err := os.Stat(s.path); os.IsNotExist(err) {
			fresh = true
		}
		f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		s.f = f
		if fresh {
			h, err := json.Marshal(line{
				Type: "open", At: time.Now(), Version: Version, Conversation: s.id,
			})
			if err == nil {
				if _, err := s.f.Write(append(h, '\n')); err != nil {
					return err
				}
			}
		}
	}
	_, err = s.f.Write(append(b, '\n'))
	return err
}

// Path is where a conversation's file lives. A conversation with no id
// goes in one shared file, the way a stateless caller's does.
func Path(dir, conversation string) string {
	return filepath.Join(dir, fileName(conversation))
}

func fileName(conversation string) string {
	name := strings.TrimSpace(conversation)
	if name == "" {
		name = "stateless"
	}
	// An id is a file name; keep it one.
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, name)
	return name + ".jsonl"
}

// file is a parsed session file.
type file struct {
	turns   []Turn
	history []string
	// started is the first record's time and last the newest turn or
	// input's, so a listing reads them from the record and not from
	// the file's modification time, which a copy would move.
	started, last time.Time
}

// read parses a file. A missing one is an empty conversation, not an
// error: Open on a new id has to work.
func read(path string) (file, error) {
	var out file
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	defer f.Close()

	var turns []Turn
	var history []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if !l.At.IsZero() {
			if out.started.IsZero() {
				out.started = l.At
			}
			// last is when the person last said something, so the
			// header does not count towards it.
			if l.Type != "open" && l.At.After(out.last) {
				out.last = l.At
			}
		}
		switch l.Type {
		case "turn":
			turns = append(turns, Turn{
				At: l.At, Ended: l.Ended, Said: l.Said, Events: l.Events,
				Cost: l.Cost, InputTokens: l.InputTokens, OutputTokens: l.OutputTokens,
			})
		case "input":
			if n := len(history); n == 0 || history[n-1] != l.Text {
				history = append(history, l.Text)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	if len(history) > historyLimit {
		history = history[len(history)-historyLimit:]
	}
	if out.last.IsZero() {
		out.last = out.started
	}
	out.turns, out.history = turns, history
	return out, nil
}

// Info is one conversation, as a listing shows it. Started is when
// the file was opened and Last when something was last said to it.
type Info struct {
	ID      string    `json:"id"`
	Path    string    `json:"path"`
	Started time.Time `json:"started"`
	Last    time.Time `json:"last"`
	Turns   int       `json:"turns"`
	// Cost is the whole conversation's, summed over its turns.
	Cost float64 `json:"cost,omitempty"`
}

// List is every conversation under dir, most recently said to first.
// It reads each file to count the turns — a session file is one
// conversation, not the log of a fleet, and stays small enough for
// that. A directory that is not there is no conversations, not an
// error.
func List(dir string) ([]Info, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Info
	for _, d := range entries {
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, d.Name())
		f, err := read(path)
		if err != nil {
			continue
		}
		info := Info{
			ID:      strings.TrimSuffix(d.Name(), ".jsonl"),
			Path:    path,
			Started: f.started,
			Last:    f.last,
			Turns:   len(f.turns),
		}
		for _, t := range f.turns {
			info.Cost += t.Cost
		}
		if info.Started.IsZero() {
			// Nothing dated in it: fall back to the file itself.
			if fi, err := d.Info(); err == nil {
				info.Started, info.Last = fi.ModTime(), fi.ModTime()
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out, nil
}
