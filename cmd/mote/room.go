package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/incantery/mote/tool"
)

// room is the demo's own tool: the one the *harness* brought, rather
// than one the profile picked off a list.
//
// It stands in for what a real supervisor has — Vera's `fleet`, which
// starts a task in a repository, reports on one, and stops one. It is
// here because four things this milestone added only show themselves
// on a tool like that, and none of them show on `read`:
//
//   - the registry owns it, so the profile's `tools:` line — which
//     has never heard of `room` — cannot take it away,
//   - it says a line in the harness's voice before it has a result,
//     which is what the Handle's Say is for,
//   - it hands back a task id and what the call cost in Result.Meta,
//     which the harness records and the model never sees,
//   - its verb is the whole of the question, so a rule keys on the
//     argument (`when = { action = "stop" }`) and an "always" covers
//     the verb it states as its Scope, not the tool.
//
// Nothing is really opened. The point is the shape of the call.
type room struct{}

type roomArgs struct {
	Action string `json:"action"`
	What   string `json:"what"`
}

func (room) Name() string { return "room" }

func (room) Description() string {
	return "Hand a piece of work to somebody else. `action` is open (start work in a room " +
		"of its own), report (say how it is going) or stop (abandon it). `what` is the work, " +
		"or the room."
}

func (room) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["open", "report", "stop"],
               "description": "What to do about a room."},
    "what": {"type": "string", "description": "The work to hand over, or the room to ask about."}
  },
  "required": ["action"]
}`)
}

// Scope is the verb: two calls that open a room are the same
// question, and opening one is not the same question as stopping one.
// Without this the Gate would have no command line to read and would
// fall back to the tool itself, so an "always" about opening a room
// would quietly cover stopping one.
func (room) Scope(args json.RawMessage) string {
	var v roomArgs
	_ = json.Unmarshal(args, &v)
	return strings.TrimSpace(v.Action)
}

func (room) Run(ctx context.Context, args json.RawMessage, h tool.Handle) (tool.Result, error) {
	var v roomArgs
	if err := json.Unmarshal(args, &v); err != nil {
		return tool.Result{}, fmt.Errorf("room: arguments are not readable: %w", err)
	}
	what := strings.TrimSpace(v.What)
	if what == "" {
		what = "the work"
	}
	switch strings.TrimSpace(v.Action) {
	case "open":
		// The harness's voice, before there is anything to show: a
		// person watching a card should not have to guess what the
		// twenty seconds are for.
		h.Say("Opening a room for " + what + "…")
		fmt.Fprintf(h, "cloning the repository\nwriting the brief\n")
		id := "r-" + time.Now().UTC().Format("150405")
		return tool.Result{
			Text: fmt.Sprintf("opened %s for %s, asked from %s (%s)",
				id, what, h.Value(tool.Device), h.Value(tool.Cwd)),
			// What the harness writes down beside the call. The model
			// is told the sentence above; the journal gets these.
			Meta: map[string]any{
				tool.MetaTask:    id,
				tool.MetaSession: "s-" + id,
				tool.MetaCost:    0.014,
			},
		}, nil
	case "report":
		h.Say("Asking " + what + " how it is going…")
		return tool.Result{Text: what + " is working, and last said so a minute ago"}, nil
	case "stop":
		h.Say("Stopping " + what + "…")
		return tool.Result{
			Text: "stopped " + what + " — whatever it had not finished is gone",
			Meta: map[string]any{tool.MetaTask: what},
		}, nil
	}
	return tool.Result{Text: "no such action " + v.Action + " — open, report or stop"}, nil
}
