package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/incantery/mote/tool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tests here run against a real MCP server — the SDK's, in this
// process, over both transports the spec has. Nothing is mocked: the
// bytes on the pipe and on the socket are the protocol's, and the
// only thing that is not real is that the server is a goroutine
// rather than somebody's npm package.
//
// TestServesOverASubprocess is the exception and the point of the
// exception: it runs this test binary again as the server, over its
// stdin and stdout, so that the path a profile actually takes —
// `command = "…"` → exec → CommandTransport — is the path under test.

// serveEnv makes this binary a server instead of a test run.
const serveEnv = "MOTE_MCP_SERVE_FAKE"

func TestMain(m *testing.M) {
	if os.Getenv(serveEnv) != "" {
		if err := fakeServer().Run(context.Background(), &sdk.StdioTransport{}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeServer is three tools: one that answers in text, one that
// answers with everything that is not text, and one that says it
// failed the way the protocol says to.
func fakeServer() *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "v9"}, nil)

	s.AddTool(&sdk.Tool{
		Name:        "echo",
		Description: "Say a thing back.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
	}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var v struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(req.Params.Arguments, &v)
		return &sdk.CallToolResult{
			Content: []sdk.Content{&sdk.TextContent{Text: "you said " + v.Text}},
			Meta:    sdk.Meta{"seen": v.Text},
		}, nil
	})

	s.AddTool(&sdk.Tool{
		Name:        "look",
		Description: "Answer with everything that is not text.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{
			&sdk.TextContent{Text: "here it is"},
			&sdk.ImageContent{MIMEType: "image/png", Data: make([]byte, 4096)},
			&sdk.AudioContent{MIMEType: "audio/wav", Data: make([]byte, 1_500_000)},
			&sdk.ResourceLink{URI: "file:///tmp/report.pdf", Name: "the report"},
			&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{
				URI: "file:///tmp/note.md", MIMEType: "text/markdown", Text: "# a note"}},
			&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{
				URI: "file:///tmp/blob.bin", MIMEType: "application/octet-stream", Blob: make([]byte, 12)}},
		}}, nil
	})

	s.AddTool(&sdk.Tool{
		Name:        "fails",
		Description: "Fail, the way the protocol says to.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{
			IsError: true,
			Content: []sdk.Content{&sdk.TextContent{Text: "the disk is full"}},
		}, nil
	})
	return s
}

// wired is one connected fake, over one of the two transports, with
// the server it is talking to so a test can change what it offers.
type wired struct {
	client *Client
	reg    *tool.Registry
	server *sdk.Server
}

// each runs a test against both wires. The assertions are the same
// either way, which is the claim being made: what a profile writes
// down is which transport, and nothing above that line knows.
func each(t *testing.T, fn func(t *testing.T, w wired)) {
	t.Helper()
	for _, over := range []struct {
		name string
		open func(*testing.T) wired
	}{
		{"stdio", overPipes},
		{"http", overHTTP},
	} {
		t.Run(over.name, func(t *testing.T) { fn(t, over.open(t)) })
	}
}

// overPipes is the stdio wire without a subprocess: newline-delimited
// JSON-RPC over a pair of pipes, which is byte for byte what a
// command's stdin and stdout carry.
func overPipes(t *testing.T) wired {
	t.Helper()
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	srv := fakeServer()
	ctx := t.Context()
	ss, err := srv.Connect(ctx, &sdk.IOTransport{Reader: serverIn, Writer: serverOut}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })

	reg := tool.NewRegistry()
	c, err := open(ctx, Server{Name: "fake", Command: "(pipes)"}, reg,
		&sdk.IOTransport{Reader: clientIn, Writer: clientOut})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return wired{client: c, reg: reg, server: srv}
}

// overHTTP is the other wire, through the whole of Open: a URL in a
// profile, the transport built from it, headers on every request.
func overHTTP(t *testing.T) wired {
	t.Helper()
	srv := fakeServer()
	var seen = make(chan string, 8)
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	http := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.Header.Get("Authorization"):
		default:
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(http.Close)

	t.Setenv("MOTE_TEST_TOKEN", "hunter2")
	reg := tool.NewRegistry()
	c, err := Open(t.Context(), Server{
		Name:    "fake",
		URL:     http.URL,
		Headers: map[string]string{"Authorization": "Bearer ${MOTE_TEST_TOKEN}"},
	}, reg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	// The profile's header, with the secret out of the environment
	// rather than out of the file, on the wire.
	select {
	case got := <-seen:
		if got != "Bearer hunter2" {
			t.Fatalf("Authorization was %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no request arrived")
	}
	return wired{client: c, reg: reg, server: srv}
}

// waitFor gives a change made on the server time to reach the
// registry, which happens on the client's own goroutine after a
// notification.
func waitFor(t *testing.T, c *Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := c.waitRefresh(ctx); err != nil {
		t.Fatalf("the tool list never came back: %v", err)
	}
}

func names(reg *tool.Registry) string { return strings.Join(reg.Names(), " ") }
