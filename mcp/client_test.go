package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/incantery/mote/tool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// What a server offers becomes tools in the registry, named after the
// profile's name for the server, with the server's own schema.
func TestServerToolsBecomeToolsInTheRegistry(t *testing.T) {
	each(t, func(t *testing.T, w wired) {
		if got := names(w.reg); got != "fake.echo fake.fails fake.look" {
			t.Fatalf("registered %q", got)
		}
		// They are the harness's, not the profile's, so a profile's
		// tools: line — written before this server existed — cannot
		// drop them.
		only, err := w.reg.Only()
		if err != nil {
			t.Fatal(err)
		}
		if len(only.List()) != 3 {
			t.Fatalf("a profile that named no tools has %v", only.Names())
		}

		echo, ok := w.reg.Get("fake.echo")
		if !ok {
			t.Fatal("fake.echo is missing")
		}
		if echo.Description() != "Say a thing back." {
			t.Fatalf("description %q", echo.Description())
		}
		// The schema is the server's, and it reaches the model as it
		// arrived: this is what a tool call will be validated against
		// at the other end.
		var schema map[string]any
		if err := json.Unmarshal(echo.Schema(), &schema); err != nil {
			t.Fatalf("schema %q: %v", echo.Schema(), err)
		}
		if schema["type"] != "object" {
			t.Fatalf("schema %v", schema)
		}
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["text"]; !ok {
			t.Fatalf("the server's schema did not come through: %v", schema)
		}
		// And it is in what the model is told.
		var found bool
		for _, d := range w.reg.Definitions() {
			if d.Function.Name == "fake.echo" && len(d.Function.Parameters) > 0 {
				found = true
			}
		}
		if !found {
			t.Fatal("fake.echo is not in the definitions")
		}

		if w.client.Says() != "fake v9" {
			t.Fatalf("the server calls itself %q", w.client.Says())
		}
	})
}

// tools/call is Run, and what the server said is the Result.
func TestCallingATool(t *testing.T) {
	each(t, func(t *testing.T, w wired) {
		echo, _ := w.reg.Get("fake.echo")
		res, err := echo.Run(t.Context(), json.RawMessage(`{"text":"hello"}`), tool.Handle{})
		if err != nil {
			t.Fatal(err)
		}
		if res.Text != "you said hello" {
			t.Fatalf("text %q", res.Text)
		}
		// The protocol's own _meta is what the server wanted kept and
		// the model not told, which is what Result.Meta is.
		if res.Meta["seen"] != "hello" {
			t.Fatalf("meta %v", res.Meta)
		}
	})
}

// A call the tool says failed is a Result, not an error: the model
// can work with "the disk is full", and it is marked the way every
// other failed card is.
func TestAToolThatSaysItFailed(t *testing.T) {
	each(t, func(t *testing.T, w wired) {
		fails, _ := w.reg.Get("fake.fails")
		res, err := fails.Run(t.Context(), nil, tool.Handle{})
		if err != nil {
			t.Fatalf("a tool that failed is not a Run that failed: %v", err)
		}
		if res.Text != "error: the disk is full" {
			t.Fatalf("text %q", res.Text)
		}
	})
}

// Everything that is not text is said in a line, because the
// alternative is a megabyte of base64 in a context window or a model
// told nothing happened.
func TestContentThatIsNotText(t *testing.T) {
	each(t, func(t *testing.T, w wired) {
		look, _ := w.reg.Get("fake.look")
		res, err := look.Run(t.Context(), nil, tool.Handle{})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"here it is",
			"[image image/png, 4.1 kB]",
			"[audio audio/wav, 1.5 MB]",
			"[resource file:///tmp/report.pdf — the report]",
			"file:///tmp/note.md:\n# a note",
			"[resource file:///tmp/blob.bin, application/octet-stream, 12 B]",
		} {
			if !strings.Contains(res.Text, want) {
				t.Errorf("missing %q in:\n%s", want, res.Text)
			}
		}
	})
}

// A tool that goes away while a conversation is running is an error
// from Run — the call did not happen — rather than a Result saying it
// did nothing.
func TestCallingSomethingTheServerNoLongerHas(t *testing.T) {
	each(t, func(t *testing.T, w wired) {
		echo, _ := w.reg.Get("fake.echo")
		w.server.RemoveTools("echo")
		waitFor(t, w.client)
		if _, err := echo.Run(t.Context(), json.RawMessage(`{"text":"hi"}`), tool.Handle{}); err == nil {
			t.Fatal("calling a tool that is gone should fail")
		}
	})
}

// A server that says its tool list changed gets the registry to
// agree: what is new is added, what went is taken away, and what
// changed keeps its place.
func TestToolListChangedRefreshesTheRegistry(t *testing.T) {
	each(t, func(t *testing.T, w wired) {
		w.server.AddTool(&sdk.Tool{
			Name:        "later",
			Description: "Turned up after the conversation started.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "here"}}}, nil
		})
		waitFor(t, w.client)
		if got := names(w.reg); got != "fake.echo fake.fails fake.later fake.look" {
			t.Fatalf("after a tool arrived: %q", got)
		}
		later, ok := w.reg.Get("fake.later")
		if !ok {
			t.Fatal("fake.later is missing")
		}
		res, err := later.Run(t.Context(), nil, tool.Handle{})
		if err != nil || res.Text != "here" {
			t.Fatalf("%q %v", res.Text, err)
		}
		if !w.reg.Owns("fake.later") {
			t.Error("a tool that arrived late is still the harness's")
		}

		// And the other way.
		w.server.RemoveTools("later", "look")
		waitFor(t, w.client)
		if got := names(w.reg); got != "fake.echo fake.fails" {
			t.Fatalf("after two went: %q", got)
		}
		// A description that changed is the same tool, in its place.
		w.server.AddTool(&sdk.Tool{
			Name:        "echo",
			Description: "Say a thing back, differently.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "again"}}}, nil
		})
		waitFor(t, w.client)
		echo, _ := w.reg.Get("fake.echo")
		if echo.Description() != "Say a thing back, differently." {
			t.Fatalf("description %q", echo.Description())
		}
		if got := len(w.client.Tools()); got != 2 {
			t.Fatalf("%d tools on the client", got)
		}
	})
}

// The policy decides an MCP tool the way it decides anything: by
// name, with nothing about it that a profile has to have anticipated.
// A profile whose default is ask asks the first time.
func TestPolicyDecidesThemLikeAnythingElse(t *testing.T) {
	each(t, func(t *testing.T, w wired) {
		echo, _ := w.reg.Get("fake.echo")
		call := tool.NewCall("c1", echo, json.RawMessage(`{"text":"hi"}`))

		asks := &tool.Policy{Default: tool.Ask}
		if v := asks.Decide(call); v.Decision != tool.Ask {
			t.Fatalf("a profile that asks says %v", v.Decision)
		}
		// And a profile that says otherwise says otherwise, by the
		// name it reads in `mote mcp ls`.
		said := &tool.Policy{
			Default: tool.Ask,
			Tools:   map[string]tool.Decision{"fake.echo": tool.Allow},
			Rules: []tool.Rule{{
				Tools: []string{"fake.echo"}, When: map[string]string{"text": "no"},
				Then: tool.Deny, Reason: "not that one",
			}},
		}
		if v := said.Decide(call); v.Decision != tool.Allow {
			t.Fatalf("a profile that allowed it says %v", v.Decision)
		}
		no := tool.NewCall("c2", echo, json.RawMessage(`{"text":"no"}`))
		if v := said.Decide(no); v.Decision != tool.Deny || v.Reason != "not that one" {
			t.Fatalf("the argument rule says %v (%s)", v.Decision, v.Reason)
		}
	})
}

// The stdio transport as a profile writes it: a command, run, talked
// to over its stdin and stdout. The command here is this test binary,
// which serves the same fake when it is told to.
func TestServesOverASubprocess(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	reg := tool.NewRegistry()
	c, err := Open(ctx, Server{
		Name:    "sub",
		Command: testBinary(t),
		Env:     map[string]string{serveEnv: "1"},
	}, reg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if got := names(reg); got != "sub.echo sub.fails sub.look" {
		t.Fatalf("registered %q", got)
	}
	echo, _ := reg.Get("sub.echo")
	res, err := echo.Run(ctx, json.RawMessage(`{"text":"over a pipe"}`), tool.Handle{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "you said over a pipe" {
		t.Fatalf("text %q", res.Text)
	}
}

// A list with one server that will not start still gets the others,
// and the error names the one that did not.
func TestOneBrokenServerDoesNotStopTheRest(t *testing.T) {
	reg := tool.NewRegistry()
	set, err := Connect(t.Context(), []Server{
		{Name: "gone", Command: "/nonexistent/mcp-server"},
		{Name: "sub", Command: testBinary(t), Env: map[string]string{serveEnv: "1"}},
	}, reg)
	defer set.Close()
	if err == nil {
		t.Fatal("the broken one should be reported")
	}
	if !strings.Contains(err.Error(), "gone") {
		t.Fatalf("the error does not name it: %v", err)
	}
	if len(set.Clients()) != 1 || set.Clients()[0].Name() != "sub" {
		t.Fatalf("clients %v", set.Clients())
	}
	if got := names(reg); got != "sub.echo sub.fails sub.look" {
		t.Fatalf("registered %q", got)
	}
}

// testBinary is this test's own executable, which serves the fake
// when serveEnv is set.
func testBinary(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("no test binary to re-run: %v", err)
	}
	return exe
}
