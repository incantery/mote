package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/incantery/mote/tool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// MaxResult is how many bytes of a server's answer reach the model,
// the way tool/builtin caps its own. A server that returns four
// megabytes of a log file has not helped anybody, least of all the
// model paying for it by the token, and the clip says where it bit.
var MaxResult = 48 << 10

// remote is one of a server's tools, as mote's registry sees it.
//
// There is nothing to it but the call: the name is the profile's for
// the server plus the server's for the tool, the description and the
// schema are the server's own words passed through unread, and Run is
// `tools/call`. It implements neither Pather nor Commander — a server
// that says its argument is a path is saying so in a schema nobody
// here can interpret — so a policy decides it by name, which is the
// safe direction: a path rule that cannot see a path does not allow.
type remote struct {
	client *Client
	// tool is what the server calls it, which is what goes back on
	// the wire; Name() is what mote calls it.
	tool   string
	desc   string
	schema json.RawMessage
}

// adopt is one of the server's tools, ready to register. Called with
// the client's lock.
func (c *Client) adopt(t *sdk.Tool) tool.Tool {
	return &remote{
		client: c,
		tool:   t.Name,
		desc:   describe(t),
		schema: schemaOf(t),
	}
}

func (r *remote) Name() string { return r.client.server.Name + Separator + r.tool }

func (r *remote) Description() string { return r.desc }

func (r *remote) Schema() json.RawMessage { return r.schema }

// Run is `tools/call`, and what comes back read out loud.
func (r *remote) Run(ctx context.Context, args json.RawMessage, h tool.Handle) (tool.Result, error) {
	params := &sdk.CallToolParams{Name: r.tool}
	if len(strings.TrimSpace(string(args))) > 0 {
		// json.RawMessage marshals as itself, so whatever the model
		// wrote is what the server is sent — unvalidated here, and
		// validated there, which is where the schema came from.
		params.Arguments = args
	}
	res, err := r.client.session.CallTool(ctx, params)
	if err != nil {
		// The call did not happen: no such tool, a session that is
		// gone, a transport that failed. That is an error from Run.
		return tool.Result{}, fmt.Errorf("%s: %w", r.Name(), err)
	}

	text := content(res)
	if res.IsError {
		// The call happened and the tool says it failed. That is a
		// Result, not an error — the model can work with it — and the
		// "error: " is the harness's convention for a card that did
		// not do what it was asked, which is what this is.
		if strings.TrimSpace(text) == "" {
			text = "the server said this failed and did not say why"
		}
		text = "error: " + text
	}
	return tool.Result{
		Text: clip(text, MaxResult),
		// The protocol's own `_meta` is what a server wanted the
		// client to keep and the model not to read, which is exactly
		// what Meta is for.
		Meta: map[string]any(res.Meta),
	}, nil
}

// describe is what the model is told this tool is. A server that
// wrote a title as well as a name says both, because a name like
// `create_issue` and a title like "Create an issue" are not the same
// sentence and the model gets the name anyway.
func describe(t *sdk.Tool) string {
	desc := strings.TrimSpace(t.Description)
	title := ""
	if t.Annotations != nil {
		title = strings.TrimSpace(t.Annotations.Title)
	}
	switch {
	case desc != "":
		return desc
	case title != "":
		return title
	}
	return t.Name
}

// schemaOf is the server's input schema as JSON. It is passed through
// rather than parsed: it is going into a request body, and a schema
// this package did not understand is still a schema the model might.
func schemaOf(t *sdk.Tool) json.RawMessage {
	if t.InputSchema == nil {
		return nil
	}
	buf, err := json.Marshal(t.InputSchema)
	if err != nil || string(buf) == "null" {
		return nil
	}
	return buf
}

// content is a CallToolResult as something a model can read.
//
// Text is text. Everything else — an image, a sound, a file the
// server would rather hand over by reference — becomes a line saying
// what it was and how big, because the alternative is either dropping
// it silently or sending a megabyte of base64 into a context window
// to be ignored. A model told "[image image/png, 41.0 kB]" knows
// something happened and can ask about it; a model told nothing
// believes the call returned nothing.
func content(res *sdk.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		switch v := c.(type) {
		case *sdk.TextContent:
			parts = append(parts, v.Text)
		case *sdk.ImageContent:
			parts = append(parts, brief("image", v.MIMEType, len(v.Data)))
		case *sdk.AudioContent:
			parts = append(parts, brief("audio", v.MIMEType, len(v.Data)))
		case *sdk.ResourceLink:
			line := "[resource " + v.URI
			if v.Name != "" {
				line += " — " + v.Name
			}
			parts = append(parts, line+"]")
		case *sdk.EmbeddedResource:
			parts = append(parts, embedded(v))
		default:
			parts = append(parts, fmt.Sprintf("[%T]", c))
		}
	}
	// A server that answered with structured output and no content is
	// answering in JSON, and JSON is something a model reads.
	if len(parts) == 0 && res.StructuredContent != nil {
		if buf, err := json.Marshal(res.StructuredContent); err == nil {
			parts = append(parts, string(buf))
		}
	}
	return strings.Join(parts, "\n")
}

// embedded is a resource the server put in the answer itself: its
// text if it is text, and what it is if it is not.
func embedded(v *sdk.EmbeddedResource) string {
	if v.Resource == nil {
		return "[resource]"
	}
	if v.Resource.Text != "" {
		return v.Resource.URI + ":\n" + v.Resource.Text
	}
	return "[resource " + v.Resource.URI + ", " +
		strings.TrimPrefix(brief("", v.Resource.MIMEType, len(v.Resource.Blob)), " ") + "]"
}

func brief(what, mime string, n int) string {
	if mime == "" {
		mime = "unknown type"
	}
	if what == "" {
		return mime + ", " + size(n)
	}
	return "[" + what + " " + mime + ", " + size(n) + "]"
}

func size(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d B", n)
	case n < 1000*1000:
		return fmt.Sprintf("%.1f kB", float64(n)/1000)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/1000/1000)
}

// clip is the last cap, on whatever the server said.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndexByte(s[:n], '\n')
	if cut <= 0 {
		cut = n
	}
	return fmt.Sprintf("%s\n… truncated: %s of %s shown", s[:cut], size(cut), size(len(s)))
}
