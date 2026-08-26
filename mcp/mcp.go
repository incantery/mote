// Package mcp is other people's tools: the servers a profile declares
// in mcp.toml, connected, with everything they offer registered as a
// tool.Tool like any other.
//
// A profile is a directory a person can read, and this is one more
// file in it:
//
//	[[servers]]
//	name    = "files"
//	command = "mcp-server-filesystem"
//	args    = ["~/notes"]
//
//	[[servers]]
//	name    = "docs"
//	url     = "https://mcp.example.com/mcp"
//	headers = { Authorization = "Bearer ${DOCS_TOKEN}" }
//
// Load reads it, Connect opens them, and each server's tools land in
// the registry as `<server>.<tool>` with the server's own JSON Schema.
// From there nothing is special about them: the model is told about
// them by Registry.Definitions, the policy decides them by the same
// rules as everything else — which, with a profile whose default is
// ask, means somebody is asked the first time — and a `tools/call`
// happens in Run.
//
// The protocol itself is the official Go SDK
// (github.com/modelcontextprotocol/go-sdk), which is at v1 and speaks
// both transports the spec has: newline-delimited JSON-RPC over a
// subprocess's stdin and stdout, and streamable HTTP with SSE. What
// is here is the part the SDK does not have an opinion about: what a
// profile writes down, what a tool is called once it arrives, and
// what a `CallToolResult` full of images and resources says to a
// model that can only read.
package mcp

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Separator goes between the server and the tool in a registered
// name: `files.read`.
//
// It is a variable because both wires disagree with the spec about
// it. A function name in an OpenAI request body, and a tool name in
// an Anthropic one, must match `[a-zA-Z0-9_-]{1,64}` — a dot is not
// in it. A harness that has met a real model sets this to `__` (or
// anything else in that set) once, at startup, before Connect; the
// default is the dot because it is what a person reads best and what
// a profile's rules are written against.
var Separator = "."

// Server is one server as a profile declares it. It says which
// transport by which fields it fills: a command is a subprocess to
// talk to over its stdin and stdout, a url is streamable HTTP.
type Server struct {
	// Name is what its tools are called after: `files.read`. It is
	// the profile's word, not the server's — two checkouts of the
	// same server are two names.
	Name string `toml:"name"`

	// Command, Args and Env are the stdio transport: a program to
	// run, and talk to over its stdin and stdout.
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`

	// URL and Headers are streamable HTTP: an endpoint to POST to,
	// with the server's replies arriving as JSON or as SSE.
	URL     string            `toml:"url"`
	Headers map[string]string `toml:"headers"`
}

// Transport is which of the two this server is, as a word for an
// error message and for `mote mcp ls`.
func (s Server) Transport() string {
	if s.URL != "" {
		return "http"
	}
	return "stdio"
}

// Where is the server in one line: the command it runs, or the
// endpoint it posts to.
func (s Server) Where() string {
	if s.URL != "" {
		return s.URL
	}
	return strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " "))
}

// declared is the file's shape.
type declared struct {
	Servers []Server `toml:"servers"`
}

// File is the name Load looks for in a profile directory.
const File = "mcp.toml"

// Load reads a profile's mcp.toml. A profile with no such file has no
// servers, which is not an error: most profiles do not have any, and
// a missing file is how they say so.
func Load(dir string) ([]Server, error) {
	servers, err := LoadFS(os.DirFS(dir), ".")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	return servers, nil
}

// LoadFS is Load from any filesystem, so a profile compiled into a
// binary is read by the same code as one on disk.
func LoadFS(fsys fs.FS, dir string) ([]Server, error) {
	name := File
	if dir != "" && dir != "." {
		name = dir + "/" + File
	}
	body, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	var d declared
	if _, err := toml.Decode(string(body), &d); err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	if err := check(d.Servers); err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	return d.Servers, nil
}

// check finds the mistakes worth finding when the file is read rather
// than when a model reaches for a tool that is not there.
func check(servers []Server) error {
	seen := map[string]bool{}
	for i, s := range servers {
		at := fmt.Sprintf("server %d", i+1)
		if s.Name != "" {
			at = fmt.Sprintf("server %q", s.Name)
		}
		switch {
		case strings.TrimSpace(s.Name) == "":
			return fmt.Errorf("%s has no name", at)
		case strings.ContainsAny(s.Name, " \t."+Separator):
			return fmt.Errorf("%s: a name may not contain %q or a space — "+
				"it is what its tools are called after", at, Separator)
		case seen[s.Name]:
			return fmt.Errorf("%s is declared twice", at)
		case s.Command == "" && s.URL == "":
			return fmt.Errorf("%s has neither a command nor a url", at)
		case s.Command != "" && s.URL != "":
			return fmt.Errorf("%s has both a command and a url — it is one or the other", at)
		case s.URL != "" && len(s.Args) > 0:
			return fmt.Errorf("%s has a url and args; args belong to a command", at)
		case s.URL != "" && len(s.Env) > 0:
			return fmt.Errorf("%s has a url and env; env belongs to a command", at)
		case s.Command != "" && len(s.Headers) > 0:
			return fmt.Errorf("%s has a command and headers; headers belong to a url", at)
		}
		seen[s.Name] = true
	}
	return nil
}

// expand replaces $VAR and ${VAR} in a value with this process's
// environment. It is here so that a token can live in the
// environment, where a secret belongs, and the profile — a file a
// person reads and a repository might keep — can say which one:
//
//	headers = { Authorization = "Bearer ${DOCS_TOKEN}" }
//
// A variable that is not set becomes empty, which is what a shell
// does and what an unauthenticated request deserves.
func expand(s string) string { return os.ExpandEnv(s) }
