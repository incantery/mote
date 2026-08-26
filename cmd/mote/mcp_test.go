package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// `mote mcp ls` against a real server: the names it prints are the
// names the model will see and the names a policy rule has to be
// written against, which is what the verb is for.
func TestMcpLs(t *testing.T) {
	srv := sdk.NewServer(&sdk.Implementation{Name: "notes", Version: "v2"}, nil)
	srv.AddTool(&sdk.Tool{
		Name:        "search",
		Description: "Search the notes.\nSeveral lines, because a server may write one.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{}, nil
	})
	http := httptest.NewServer(sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return srv }, nil))
	defer http.Close()

	dir := t.TempDir()
	body := "[[servers]]\nname = \"notes\"\nurl = \"" + http.URL + "\"\n\n" +
		"[[servers]]\nname = \"nowhere\"\ncommand = \"/nonexistent/mcp-server\"\n"
	if err := os.WriteFile(filepath.Join(dir, "mcp.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := mcpLs(&out, []string{dir}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"mcp.toml",
		"notes", "http", "notes v2",
		"notes.search", "Search the notes. Several lines",
		// The one that did not answer is the half you came to read.
		"nowhere", "did not answer",
		"policy.toml",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// A profile with no mcp.toml says so rather than failing: most
// profiles have no servers.
func TestMcpLsWithoutTheFile(t *testing.T) {
	var out strings.Builder
	if err := mcpLs(&out, []string{t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no servers declared") {
		t.Fatalf("%s", out.String())
	}
}

func TestMcpLsNeedsAProfile(t *testing.T) {
	var out strings.Builder
	if err := mcpLs(&out, nil); err == nil {
		t.Fatal("no error")
	}
	if err := mcpCommand([]string{"nope"}); err == nil {
		t.Fatal("no error for an unknown verb")
	}
}
