package profile

import (
	"context"
	"encoding/json"
	"io"

	"github.com/incantery/mote/tool"
)

// named is a tool that is only a name, for the tests that are about
// which tools a profile has rather than what they do.
type named string

func (n named) Name() string            { return string(n) }
func (n named) Description() string     { return string(n) }
func (n named) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (n named) Run(context.Context, json.RawMessage, io.Writer) (tool.Result, error) {
	return tool.Result{}, nil
}
