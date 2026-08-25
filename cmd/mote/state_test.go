package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/incantery/mote/agent"
	"github.com/incantery/mote/session"
)

func TestSessionDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/state")
	got, err := sessionDir("")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/state", "mote", "sessions"); got != want {
		t.Fatalf("sessionDir = %q, want %q", got, want)
	}
	if got, _ := sessionDir("/elsewhere"); got != "/elsewhere" {
		t.Fatalf("the flag did not win: %q", got)
	}

	// A relative XDG_STATE_HOME is a mistake worth naming, not a
	// directory to create somewhere surprising.
	t.Setenv("XDG_STATE_HOME", "relative")
	if _, err := sessionDir(""); err == nil {
		t.Fatal("want an error for a relative XDG_STATE_HOME")
	}

	// Without it, the XDG default under the home directory.
	t.Setenv("XDG_STATE_HOME", "")
	got, err = sessionDir("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, filepath.Join(".local", "state", "mote", "sessions")) {
		t.Fatalf("sessionDir = %q", got)
	}
}

func TestWriteSessions(t *testing.T) {
	dir := t.TempDir()
	s, err := session.Open(dir, "demo-1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 20, 0, 0, 0, time.UTC)
	s.Append(session.Turn{At: now.Add(-90 * time.Minute), Said: "hello", Cost: 0.0145,
		Events: []agent.Event{agent.Delta("hi")}})
	s.Close()

	list, err := session.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := writeSessions(&b, dir, list, now); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{dir, "demo-1", "$0.0145", "1h ago"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing is missing %q:\n%s", want, out)
		}
	}
}

func TestAgo(t *testing.T) {
	now := time.Date(2026, 8, 25, 20, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{
		-time.Second:     "just now",
		30 * time.Second: "30s ago",
		5 * time.Minute:  "5m ago",
		3 * time.Hour:    "3h ago",
		50 * time.Hour:   "2d ago",
	} {
		if got := ago(now.Add(-d), now); got != want {
			t.Errorf("ago(-%v) = %q, want %q", d, got, want)
		}
	}
	if got := ago(time.Time{}, now); got != "" {
		t.Errorf("ago(zero) = %q", got)
	}
}

func TestGreetingNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	s, err := session.Open(dir, "demo-1")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g := greeting(s)
	if !strings.Contains(g, s.Path()) || !strings.Contains(g, "mote demo -c demo-1") {
		t.Fatalf("a fresh greeting must say where it is kept:\n%s", g)
	}
	if strings.Contains(g, "Reopened") {
		t.Fatal("nothing to reopen yet")
	}

	s.Append(session.Turn{At: time.Now(), Said: "hello"})
	if g := greeting(s); !strings.Contains(g, "Reopened **demo-1** — 1 turn above") {
		t.Fatalf("a reopened greeting must say so:\n%s", g)
	}
}
