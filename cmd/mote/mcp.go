package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/incantery/mote/mcp"
	"github.com/incantery/mote/tool"
)

// mcpCommand is `mote mcp ls <profile>`: connect to what a profile
// declares and say what came back.
//
// It is the verb you run before believing a profile: the tools are
// named the way the model will see them and the way a policy rule
// about them has to be written, so the answer is what to put in
// policy.toml.
func mcpCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("mote mcp ls <profile> — the only verb is ls")
	}
	switch args[0] {
	case "ls":
		return mcpLs(os.Stdout, args[1:])
	}
	return fmt.Errorf("no such mcp verb %q — ls", args[0])
}

func mcpLs(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("mote mcp ls", flag.ContinueOnError)
	fs.SetOutput(w)
	wait := fs.Duration("timeout", 30*time.Second, "how long to wait for a server to answer")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := fs.Arg(0)
	if dir == "" {
		return errors.New("which profile? — mote mcp ls <profile>")
	}
	servers, err := mcp.Load(dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%s\n", filepath.Join(dir, mcp.File))
	if len(servers) == 0 {
		fmt.Fprintln(w, "no servers declared")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), *wait)
	defer cancel()
	reg := tool.NewRegistry()
	set, failed := mcp.Connect(ctx, servers, reg)
	defer set.Close()

	// Whatever answered, in the order the file declared it — and then
	// the ones that did not, which is the half you came to read.
	answered := map[string]*mcp.Client{}
	for _, c := range set.Clients() {
		answered[c.Name()] = c
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, s := range servers {
		c, ok := answered[s.Name]
		if !ok {
			fmt.Fprintf(tw, "\n%s\t%s\t%s\t— did not answer\n", s.Name, s.Transport(), s.Where())
			continue
		}
		fmt.Fprintf(tw, "\n%s\t%s\t%s\t%s\n", s.Name, s.Transport(), s.Where(), c.Says())
		for _, t := range c.Tools() {
			fmt.Fprintf(tw, "  %s\t\t\t%s\n", t.Name(), oneLine(t.Description()))
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if failed != nil {
		fmt.Fprintf(w, "\n%s\n", failed)
	}
	fmt.Fprintf(w, "\nThe policy decides these by the names above: `[tools]` in "+
		"policy.toml, or a rule about one. Anything it does not mention is the "+
		"profile's default.\n")
	return nil
}

// oneLine is a description as a line of a table: a server that wrote
// a paragraph gets its first sentence.
func oneLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	if len(s) > 90 {
		s = strings.TrimSpace(s[:90]) + "…"
	}
	return s
}
