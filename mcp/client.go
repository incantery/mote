package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"

	"github.com/incantery/mote/tool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Us is what a server is told it is talking to.
var us = &sdk.Implementation{Name: "mote", Version: "1"}

// Set is a profile's servers, connected.
//
// It is what a harness holds on to: it owns the connections, and
// closing it closes them. The tools are already in the registry by
// then — a Set is not something anything has to consult per call.
type Set struct {
	clients []*Client
}

// Connect opens every server in the list and registers what each one
// offers, as `<server>.<tool>` and with the server's own schema.
//
// The tools go in as the registry's Own: a profile's `tools:` line
// lists built-ins, and it should not have to have named an MCP tool
// that did not exist when it was written in order to keep it. What a
// profile does say about them is what the *policy* says, and there
// they are like anything else — with a profile whose default is ask,
// the first `github.create_issue` stops and asks.
//
// A server that will not answer does not stop the others. The ones
// that connected are in the Set and their tools are registered; the
// error names the ones that did not, and a harness is expected to
// print it rather than to die of it — one broken server in a list of
// four is not a reason to have no agent.
//
// The context bounds the connecting, not the connection: a server
// that is still being spoken to when it returns is fine, and closing
// the Set is what ends that.
func Connect(ctx context.Context, servers []Server, reg *tool.Registry) (*Set, error) {
	set := &Set{}
	var failed []error
	for _, s := range servers {
		c, err := Open(ctx, s, reg)
		if err != nil {
			failed = append(failed, err)
			continue
		}
		set.clients = append(set.clients, c)
	}
	return set, errors.Join(failed...)
}

// Clients is every server that answered, in the order declared.
func (s *Set) Clients() []*Client {
	if s == nil {
		return nil
	}
	return s.clients
}

// Close ends every connection: a subprocess is asked to stop and
// waited for, an HTTP session is deleted.
func (s *Set) Close() error {
	if s == nil {
		return nil
	}
	var errs []error
	for _, c := range s.clients {
		errs = append(errs, c.Close())
	}
	return errors.Join(errs...)
}

// Client is one connected server.
type Client struct {
	server  Server
	session *sdk.ClientSession
	reg     *tool.Registry

	mu    sync.Mutex
	tools []tool.Tool

	// refreshed is closed and replaced after every tool list, so a
	// test can wait for one it did not ask for. Nothing outside this
	// package needs it: a harness reads the registry, which is
	// already correct by the time anything is signalled.
	refreshed chan struct{}
}

// Open connects to one server, initializes, lists its tools and
// registers them. It is Connect for a single server, and it is what a
// harness calls when the server did not come out of a file.
func Open(ctx context.Context, s Server, reg *tool.Registry) (*Client, error) {
	if err := check([]Server{s}); err != nil {
		return nil, err
	}
	t, err := transport(s)
	if err != nil {
		return nil, fmt.Errorf("mcp %s: %w", s.Name, err)
	}
	return open(ctx, s, reg, t)
}

// open is Open with the transport already decided, which is how a
// test speaks both wires without a subprocess or a port.
func open(ctx context.Context, s Server, reg *tool.Registry, t sdk.Transport) (*Client, error) {
	c := &Client{server: s, reg: reg, refreshed: make(chan struct{})}
	client := sdk.NewClient(us, &sdk.ClientOptions{
		// The server saying its tools changed. It arrives on the
		// session's own goroutine, and answering it means asking the
		// same session a question, so the work goes elsewhere.
		ToolListChangedHandler: func(context.Context, *sdk.ToolListChangedRequest) {
			go func() {
				// Not the notification's context: that one ends when
				// the notification has been handled, and this outlives
				// it by a round trip.
				if err := c.Refresh(context.Background()); err != nil {
					c.tell(err)
				}
			}()
		},
	})
	session, err := client.Connect(ctx, t, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp %s: %w", s.Name, err)
	}
	c.session = session
	if err := c.Refresh(ctx); err != nil {
		session.Close()
		return nil, err
	}
	return c, nil
}

// transport is the SDK transport this server declared.
func transport(s Server) (sdk.Transport, error) {
	if s.URL != "" {
		return &sdk.StreamableClientTransport{
			Endpoint:   expand(s.URL),
			HTTPClient: &http.Client{Transport: headers(s.Headers)},
		}, nil
	}
	cmd := exec.Command(expand(s.Command), expanded(s.Args)...)
	if len(s.Env) > 0 {
		// Added to what this process has rather than replacing it: a
		// server that cannot see PATH or HOME is a server that does
		// not start, and a profile saying `env = { TOKEN = … }` did
		// not mean to take those away.
		cmd.Env = os.Environ()
		for k, v := range s.Env {
			cmd.Env = append(cmd.Env, k+"="+expand(v))
		}
	}
	return &sdk.CommandTransport{Command: cmd}, nil
}

func expanded(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		out = append(out, expand(a))
	}
	return out
}

// headers puts a profile's headers on every request. It is a
// RoundTripper rather than a field on the transport because that is
// where the SDK leaves the door open, and because the standalone SSE
// stream is a request nobody else would have got them onto.
type headers map[string]string

func (h headers) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(h) > 0 {
		req = req.Clone(req.Context())
		for k, v := range h {
			req.Header.Set(k, expand(v))
		}
	}
	return http.DefaultTransport.RoundTrip(req)
}

// Name is what this server's tools are called after.
func (c *Client) Name() string { return c.server.Name }

// Server is the declaration this client was opened from.
func (c *Client) Server() Server { return c.server }

// Says is what the server called itself when it was initialized —
// its own name and version, which is not the profile's name for it.
func (c *Client) Says() string {
	res := c.session.InitializeResult()
	if res == nil || res.ServerInfo == nil {
		return ""
	}
	if res.ServerInfo.Version == "" {
		return res.ServerInfo.Name
	}
	return res.ServerInfo.Name + " " + res.ServerInfo.Version
}

// Tools is what it offers, as of the last list, in the server's own
// order.
func (c *Client) Tools() []tool.Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]tool.Tool(nil), c.tools...)
}

// Refresh asks the server what it has and makes the registry agree:
// what is new is added, what changed is replaced in place, and what
// the server no longer offers is taken away. It runs on its own after
// a `notifications/tools/list_changed`, and a harness may call it.
func (c *Client) Refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() {
		close(c.refreshed)
		c.refreshed = make(chan struct{})
	}()

	var found []tool.Tool
	for t, err := range c.session.Tools(ctx, nil) {
		if err != nil {
			return fmt.Errorf("mcp %s: listing tools: %w", c.server.Name, err)
		}
		found = append(found, c.adopt(t))
	}
	if c.reg == nil {
		c.tools = found
		return nil
	}

	keep := make(map[string]bool, len(found))
	for _, t := range found {
		keep[t.Name()] = true
	}
	var gone []string
	for _, was := range c.tools {
		if !keep[was.Name()] {
			gone = append(gone, was.Name())
		}
	}
	c.reg.Remove(gone...)

	var errs []error
	for _, t := range found {
		if _, had := c.reg.Get(t.Name()); had {
			errs = append(errs, c.reg.Replace(t))
			continue
		}
		if err := c.reg.Own(t); err != nil {
			// Something else answers to that name. Say so and keep
			// the rest: one tool nobody can reach is better than a
			// server nobody can reach.
			errs = append(errs, fmt.Errorf("mcp %s: %w", c.server.Name, err))
		}
	}
	c.tools = found
	return errors.Join(errs...)
}

// Close ends the session. The tools stay registered — a harness that
// wants them gone says so; a call to one of them will fail with the
// server's own error, which is more use to a model than a tool that
// vanished mid-conversation.
func (c *Client) Close() error {
	if c == nil || c.session == nil {
		return nil
	}
	return c.session.Close()
}

// tell is where a failure with nobody to return it to goes: a refresh
// that a notification asked for, on a goroutine of its own.
func (c *Client) tell(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "mcp: "+err.Error())
}

// waitRefresh blocks until the next tool list finishes, for a test
// that changed something on the server and wants to know when the
// registry has caught up.
func (c *Client) waitRefresh(ctx context.Context) error {
	c.mu.Lock()
	done := c.refreshed
	c.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
