package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, File), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoad(t *testing.T) {
	t.Setenv("MOTE_TEST_NOTES", "/home/somebody/notes")
	dir := write(t, `
[[servers]]
name    = "files"
command = "mcp-server-filesystem"
args    = ["${MOTE_TEST_NOTES}"]
env     = { READ_ONLY = "1" }

[[servers]]
name    = "docs"
url     = "https://mcp.example.com/mcp"
headers = { Authorization = "Bearer ${DOCS_TOKEN}" }
`)
	servers, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("%d servers", len(servers))
	}
	if servers[0].Transport() != "stdio" || servers[1].Transport() != "http" {
		t.Fatalf("transports %q %q", servers[0].Transport(), servers[1].Transport())
	}
	// The file is read as it was written; a ${VAR} is expanded when
	// the server is opened, not when the file is parsed, so what a
	// person sees in `mote mcp ls` is what they wrote.
	if servers[0].Where() != "mcp-server-filesystem ${MOTE_TEST_NOTES}" {
		t.Fatalf("where %q", servers[0].Where())
	}
	if got := expand(servers[0].Args[0]); got != "/home/somebody/notes" {
		t.Fatalf("expanded to %q", got)
	}
	if servers[0].Env["READ_ONLY"] != "1" {
		t.Fatalf("env %v", servers[0].Env)
	}
}

// A profile with no mcp.toml has no servers, which is how most
// profiles say so.
func TestLoadWithoutTheFile(t *testing.T) {
	servers, err := Load(t.TempDir())
	if err != nil || servers != nil {
		t.Fatalf("%v %v", servers, err)
	}
}

// The mistakes worth finding when the file is read, rather than when
// a model reaches for a tool that is not there.
func TestLoadRefusesNonsense(t *testing.T) {
	for _, c := range []struct{ name, body, says string }{
		{"no name", "[[servers]]\ncommand = \"x\"\n", "has no name"},
		{"a dot in the name", "[[servers]]\nname = \"a.b\"\ncommand = \"x\"\n", "may not contain"},
		{"a space in the name", "[[servers]]\nname = \"a b\"\ncommand = \"x\"\n", "may not contain"},
		{"twice", "[[servers]]\nname=\"a\"\ncommand=\"x\"\n[[servers]]\nname=\"a\"\nurl=\"http://y\"\n", "declared twice"},
		{"neither", "[[servers]]\nname = \"a\"\n", "neither a command nor a url"},
		{"both", "[[servers]]\nname=\"a\"\ncommand=\"x\"\nurl=\"http://y\"\n", "one or the other"},
		{"args on a url", "[[servers]]\nname=\"a\"\nurl=\"http://y\"\nargs=[\"z\"]\n", "args belong to a command"},
		{"env on a url", "[[servers]]\nname=\"a\"\nurl=\"http://y\"\nenv={A=\"1\"}\n", "env belongs to a command"},
		{"headers on a command", "[[servers]]\nname=\"a\"\ncommand=\"x\"\nheaders={A=\"1\"}\n", "headers belong to a url"},
		{"not toml", "[[servers]\n", "expected"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(write(t, c.body))
			if err == nil {
				t.Fatal("no error")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Fatalf("says %q, not %q", err, c.says)
			}
		})
	}
}

// Opening a server the file could not have described is the same
// error as loading one, and it happens before anything is started.
func TestOpenChecksTheDeclaration(t *testing.T) {
	if _, err := Open(t.Context(), Server{Name: "a"}, nil); err == nil {
		t.Fatal("a server with nowhere to connect is an error")
	}
}
