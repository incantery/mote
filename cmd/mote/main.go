// Command mote is the harness on its own.
//
// Today it has one thing worth running: `mote demo`, which puts the
// terminal over a scripted agent so that the whole of it is visible in
// ten seconds without a provider, a key, or a network. `mote sessions`
// lists the conversations that demo left behind, and `mote demo -c
// <id>` reopens one.
package main

import (
	"fmt"
	"os"
	"runtime/debug"
)

// version is stamped by the build when there is a tag; otherwise it
// comes from the module's own build info, so a `go install` still says
// something true.
var version = ""

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch args[0] {
	case "demo":
		if err := demo(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "mote: "+err.Error())
			os.Exit(1)
		}
	case "sessions":
		if err := sessions(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "mote: "+err.Error())
			os.Exit(1)
		}
	case "version", "-version", "--version":
		fmt.Println("mote " + moteVersion())
	case "help", "-h", "-help", "--help":
		usage(os.Stdout)
	default:
		fmt.Fprintln(os.Stderr, "mote: no such command: "+args[0])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprint(w, `mote — a small agent harness

usage:
  mote demo [-c <id>] [-dir <path>] [-light]
        the terminal, over a scripted agent. -c reopens a conversation.
  mote sessions [-dir <path>]
        the conversations on disk, most recently said to first.
  mote version

Conversations live under $XDG_STATE_HOME/mote/sessions, or
~/.local/state/mote/sessions.
`)
}

func moteVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		rev, dirty := "", ""
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if len(s.Value) > 7 {
					rev = s.Value[:7]
				} else {
					rev = s.Value
				}
			case "vcs.modified":
				if s.Value == "true" {
					dirty = "+dirty"
				}
			}
		}
		if rev != "" {
			return rev + dirty
		}
		if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			return bi.Main.Version
		}
	}
	return "(devel)"
}
