package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/incantery/mote/session"
)

// sessions lists the conversations on disk, most recently said to
// first — the thing you read before `mote demo -c <id>`.
func sessions(args []string) error {
	fs := flag.NewFlagSet("mote sessions", flag.ContinueOnError)
	dir := fs.String("dir", "", "where the conversations live")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, err := sessionDir(*dir)
	if err != nil {
		return err
	}
	list, err := session.List(d)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Printf("no conversations in %s\n", d)
		return nil
	}
	return writeSessions(os.Stdout, d, list, time.Now())
}

func writeSessions(w io.Writer, dir string, list []session.Info, now time.Time) error {
	fmt.Fprintln(w, dir)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "id\tturns\tcost\tstarted\tlast")
	for _, it := range list {
		cost := ""
		if it.Cost > 0 {
			cost = fmt.Sprintf("$%.4f", it.Cost)
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n",
			it.ID, it.Turns, cost, it.Started.Local().Format("2006-01-02 15:04"), ago(it.Last, now))
	}
	return tw.Flush()
}

// ago is how long since something happened, in the largest unit that
// still says something true.
func ago(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// summarize is the one-line description of a conversation the demo's
// greeting uses.
func summarize(it session.Info) string {
	turns := "turns"
	if it.Turns == 1 {
		turns = "turn"
	}
	parts := []string{fmt.Sprintf("%d %s", it.Turns, turns)}
	if it.Cost > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", it.Cost))
	}
	return strings.Join(parts, " · ")
}
