package tool

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// The three answers to an ask. They are the strings agent.Answerer
// takes, spelled again here so that the tool package does not have to
// know a terminal exists.
const (
	Yes    = "yes"
	No     = "no"
	Always = "always"
)

// Gate is a Policy plus the answers a person has already given.
//
// It is the piece between "the policy says ask" and "the tool ran":
// it holds the question open until somebody answers it, and it
// remembers an "always" so the same question is not asked twice in
// one session. A Gate's Answer method has the signature
// agent.Answerer wants, so a harness can hand its Gate straight to
// the terminal.
//
// Nothing here decides *how* the question is put — that is the
// harness's, which emits the ask and shows it however it shows
// things. A Gate only knows a call is waiting and what came back.
type Gate struct {
	// Policy decides. Nil denies, which is the safe nil.
	Policy *Policy

	mu      sync.Mutex
	grants  []Grant
	pending map[string]*ask
}

type ask struct {
	done     chan struct{}
	choice   string
	answered bool
}

// Grant is what an "always" remembered: a tool, and how far the
// answer reaches.
//
// The reach is the harness's decision, and this is the one mote
// makes: for a call about files, the directory the file was in and
// everything under it; for a command, the program that was run,
// whatever its arguments; for anything else, the tool by itself. It
// is deliberately narrower than "this tool, forever" and wider than
// "this exact call" — the question a person is answering is "yes, you
// may work in there", not "yes, that one file".
type Grant struct {
	Tool string
	Dir  string
	Word string
}

// String is the grant as a person would read it back.
func (g Grant) String() string {
	switch {
	case g.Word != "":
		return g.Tool + " " + g.Word
	case g.Dir != "":
		return g.Tool + " under " + g.Dir
	}
	return g.Tool
}

// Decide answers a call, honouring anything a person has already said
// always to. It is the only method a harness needs for a call it does
// not have to ask about.
func (g *Gate) Decide(c Call) Verdict {
	p := g.Policy
	if p == nil {
		p = &Policy{}
	}
	v := p.Decide(c)
	if v.Decision != Ask {
		return v
	}
	c.Paths = p.Clean(c.Paths)
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, grant := range g.grants {
		if grant.covers(c) {
			return Verdict{
				Decision: Allow,
				Reason:   "you said always: " + grant.String(),
				Rule:     "always",
				Path:     v.Path,
			}
		}
	}
	return v
}

// Grant is what an "always" for this call would cover, so a harness
// can say so on the card before the person answers it.
func (g *Gate) Grant(c Call) Grant {
	p := g.Policy
	if p == nil {
		p = &Policy{}
	}
	return grantFor(Call{Tool: c.Tool, Paths: p.Clean(c.Paths), Command: c.Command})
}

// Wait blocks until the call is answered, and reports whether it may
// run. A cancelled context is a no — the exchange ended and nobody is
// going to answer.
//
// The answer may arrive before Wait does: a person cannot press a key
// before the card is on screen, but nothing in the types promises the
// order, and an ask that hangs because of a race is the worst bug
// this could have. An answer for a call nobody is waiting on is kept
// until somebody waits.
func (g *Gate) Wait(ctx context.Context, c Call) (bool, error) {
	if c.ID == "" {
		return false, errors.New("an ask needs a call id")
	}
	g.mu.Lock()
	a := g.open(c.ID)
	if a.answered {
		choice := a.choice
		delete(g.pending, c.ID)
		g.remember(choice, c)
		g.mu.Unlock()
		return choice != No, nil
	}
	g.mu.Unlock()

	select {
	case <-ctx.Done():
		g.mu.Lock()
		delete(g.pending, c.ID)
		g.mu.Unlock()
		return false, ctx.Err()
	case <-a.done:
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	choice := a.choice
	delete(g.pending, c.ID)
	g.remember(choice, c)
	return choice != No, nil
}

// Answer is the person's word on an open ask. It is idempotent: the
// second answer to one question is dropped, so a card that is clicked
// and then typed at does not answer twice.
//
// The signature is agent.Answerer's on purpose.
func (g *Gate) Answer(ctx context.Context, id, choice string) error {
	switch choice {
	case Yes, No, Always:
	default:
		return fmt.Errorf("no such answer %q — yes, no or always", choice)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	a := g.open(id)
	if a.answered {
		return nil
	}
	a.choice, a.answered = choice, true
	close(a.done)
	return nil
}

// open finds or starts the record for one ask. Called with the lock.
func (g *Gate) open(id string) *ask {
	if g.pending == nil {
		g.pending = map[string]*ask{}
	}
	a, ok := g.pending[id]
	if !ok {
		a = &ask{done: make(chan struct{})}
		g.pending[id] = a
	}
	return a
}

// remember records an "always". Called with the lock.
func (g *Gate) remember(choice string, c Call) {
	if choice != Always {
		return
	}
	p := g.Policy
	if p == nil {
		p = &Policy{}
	}
	grant := grantFor(Call{Tool: c.Tool, Paths: p.Clean(c.Paths), Command: c.Command})
	for _, have := range g.grants {
		if have == grant {
			return
		}
	}
	g.grants = append(g.grants, grant)
}

// Grants is what has been said always to, in the order it was said.
func (g *Gate) Grants() []Grant {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Grant(nil), g.grants...)
}

func grantFor(c Call) Grant {
	if word := firstWord(c.Command); word != "" {
		return Grant{Tool: c.Tool, Word: word}
	}
	if len(c.Paths) > 0 {
		return Grant{Tool: c.Tool, Dir: dirOf(c.Paths[0])}
	}
	return Grant{Tool: c.Tool}
}

func (g Grant) covers(c Call) bool {
	if g.Tool != c.Tool {
		return false
	}
	switch {
	case g.Word != "":
		return firstWord(c.Command) == g.Word
	case g.Dir != "":
		if len(c.Paths) == 0 {
			return false
		}
		for _, p := range c.Paths {
			if !under(g.Dir, p) {
				return false
			}
		}
		return true
	}
	return c.Command == "" && len(c.Paths) == 0
}

func firstWord(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// dirOf is the directory an "always" about this path covers. A path
// that is itself a directory is not distinguishable from a file
// without asking the disk, and a Gate does not ask the disk — so the
// grant is about the parent either way, which errs narrow for a
// directory and exactly right for a file.
func dirOf(path string) string { return filepath.Dir(path) }

func under(dir, path string) bool {
	if dir == path {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(path, strings.TrimSuffix(dir, sep)+sep)
}
