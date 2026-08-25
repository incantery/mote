package main

import (
	"flag"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/incantery/mote/agent"
	"github.com/incantery/mote/tui"
)

// demo runs the terminal over agent.Fake, with a rail that moves and a
// couple of commands, so that the whole milestone is one command away.
func demo(args []string) error {
	fs := flag.NewFlagSet("mote demo", flag.ContinueOnError)
	light := fs.Bool("light", false, "style the markdown for a light terminal")
	style := fs.String("style", "", "glamour style: auto, dark, light, ascii, notty")
	if err := fs.Parse(args); err != nil {
		return err
	}

	f := &fleet{items: seed()}
	notices := make(chan agent.Event, 8)
	stop := make(chan struct{})
	defer close(stop)
	go f.run(notices, stop)

	pal := tui.DefaultPalette()
	switch {
	case *style != "":
		pal.Markdown = *style
	case *light:
		pal.Markdown = "light"
	}

	return tui.Run(&agent.Fake{}, tui.Options{
		Name:         "mote",
		Model:        "fake-1",
		Conversation: "demo-" + time.Now().Format("150405"),
		Palette:      &pal,
		Greeting: "# mote demo\n\n" +
			"This is the terminal, over a scripted agent. Say anything and it " +
			"answers; the turns cycle through **markdown**, a **tool round**, " +
			"and an **error**, or say a line with `tool` or `error` in it to " +
			"pick one.\n\n" +
			"`ctrl+o` opens the last tool card · `tab` walks the cards · " +
			"`ctrl+t` hides the rail · `/help` for the rest.",
		Side:      f.snapshot,
		SideTitle: "fleet",
		Notices:   notices,
		Commands: []tui.Command{
			{Name: "tasks", Help: "the fleet, as lines in the transcript"},
			{Name: "report", Help: "/report <id> — what a task wrote"},
			{Name: "start", Help: "/start <brief> — put another task on the rail"},
			{Name: "new", Help: "a fresh conversation id"},
			{Name: "quit", Help: "leave"},
		},
		Handle: func(name, args string) tea.Cmd {
			switch name {
			case "tasks":
				var b strings.Builder
				for _, it := range f.snapshot() {
					fmt.Fprintf(&b, "%s %-8s %s\n", it.ID, it.State, it.Title)
				}
				return tui.Note("%s", strings.TrimRight(b.String(), "\n"))
			case "report":
				id, _, _ := strings.Cut(args, " ")
				for _, it := range f.snapshot() {
					if it.ID == id {
						return tui.Show(report(it))
					}
				}
				return tui.Fail("no task %q — /tasks", id)
			case "start":
				if strings.TrimSpace(args) == "" {
					return tui.Fail("/start <brief>")
				}
				id := f.add(args)
				return tea.Batch(tui.Note("started %s", id), tui.Refresh())
			case "new":
				id := "demo-" + time.Now().Format("150405")
				return tea.Batch(tui.SetConversation(id), tui.Note("new conversation %s", id))
			case "quit":
				return tea.Quit
			}
			return tui.Fail("unknown command /%s — /help", name)
		},
	})
}

// fleet is a made-up set of tasks that changes on its own, so the rail
// and the notices have something to say.
type fleet struct {
	mu    sync.Mutex
	items []tui.SideItem
	n     int
}

func seed() []tui.SideItem {
	return []tui.SideItem{
		{ID: "184a1100", Title: "build mote's first milestone", Subtitle: "mote", State: tui.Working, Current: true},
		{ID: "c41f9a02", Title: "tool registry with policy", Subtitle: "mote", State: tui.Idle},
		{ID: "7b20e5d9", Title: "session on disk, resumable", Subtitle: "mote", State: tui.Blocked},
		{ID: "0f3c8811", Title: "anthropic-native provider", Subtitle: "mote", State: tui.Done},
	}
}

func (f *fleet) snapshot() []tui.SideItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tui.SideItem(nil), f.items...)
}

func (f *fleet) add(brief string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	id := fmt.Sprintf("a%07d", 1000000+f.n*7919)
	f.items = append(f.items, tui.SideItem{ID: id, Title: brief, Subtitle: "mote", State: tui.Working})
	return id
}

// run walks a task through its states every few seconds and says so.
func (f *fleet) run(out chan<- agent.Event, stop <-chan struct{}) {
	defer close(out)
	order := []tui.State{tui.Working, tui.Blocked, tui.Done, tui.Idle, tui.Failed}
	t := time.NewTicker(6 * time.Second)
	defer t.Stop()
	i := 0
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			f.mu.Lock()
			if len(f.items) == 0 {
				f.mu.Unlock()
				continue
			}
			it := &f.items[(i+1)%len(f.items)]
			it.State = order[i%len(order)]
			id, st, title := it.ID, it.State, it.Title
			f.mu.Unlock()
			i++
			select {
			case out <- agent.Notice(fmt.Sprintf("%s is %s — %s", id, st, title)):
			case <-stop:
				return
			}
		}
	}
}

func report(it tui.SideItem) string {
	return "## report — " + it.ID + "\n\n" +
		"**" + it.Title + "** (" + string(it.State) + ")\n\n" +
		"- the interface is one method, `Send`\n" +
		"- the terminal knows nothing about providers\n" +
		"- `mote demo` shows it without a key\n\n" +
		"```\n$ go test ./...\nok  \tgithub.com/incantery/mote/agent\nok  \tgithub.com/incantery/mote/tui\n```\n"
}
