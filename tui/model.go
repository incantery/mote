package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/incantery/mote/agent"
	"github.com/incantery/mote/session"
)

// frameRate is how often anything that moves — spinners — redraws.
const frameRate = 100 * time.Millisecond

// Model is the terminal. It is a tea.Model; New returns it so that an
// application can embed it in a larger program if it wants to, and Run
// is there for the common case where it is the whole program.
type Model struct {
	agent agent.Agent
	opts  Options
	r     *lipgloss.Renderer
	st    styles
	md    *markdown

	conversation string
	width        int
	height       int
	ready        bool

	entries    []*entry
	partial    string // the reply as it arrives
	statusText string
	inflight   bool
	cancel     context.CancelFunc

	// The turn being recorded. turnStart is where in entries the
	// agent's side of it begins, and -1 when no turn is in flight,
	// which is also what keeps a turn from being written twice: done
	// and the end of the stream both call finish.
	sess      *session.Session
	turnStart int
	turnAt    time.Time
	turnSaid  string
	turnCost  float64
	turnIn    int
	turnOut   int

	// The conversation's, restored turns included.
	total    float64
	totalIn  int
	totalOut int

	// The finished transcript is rendered once and kept. stable holds
	// the joined render of entries[:stableN] at width stableW;
	// everything after it is redrawn, which is only ever the tail.
	stable  string
	stableN int
	stableW int

	vp     viewport.Model
	follow bool

	in *input

	side     []SideItem
	sideOpen bool

	focus int // index of the focused tool card, -1 for none
	frame int // spinner

	events    chan tea.Msg
	animating bool
}

type (
	eventMsg struct{ ev agent.Event }
	turnMsg  struct{ err error }
	tickMsg  struct{}
	sideTick struct{}
)

// New builds the terminal over an agent.
//
// It decides the markdown style here, once, from the renderer and the
// environment — see resolveStyle. Nothing after this asks the terminal
// anything, which is why New has to be called before whatever is going
// to own stdin does: Run does exactly that.
func New(a agent.Agent, opts Options) *Model {
	opts.fill()
	st := newStyles(opts.Renderer, *opts.Palette)
	profile := opts.Renderer.ColorProfile()
	style := resolveStyle(opts.Palette.Markdown, profile)
	// Glamour is not the only one who asks: every lipgloss
	// AdaptiveColor asks too, the first time it is drawn, and bubbles'
	// textarea is made of them. Answering here — once, from the same
	// decision the markdown style came from — is what keeps the
	// question off the wire for good; lipgloss remembers an answer it
	// was given and never asks again.
	dark := darkBackground(style)
	opts.Renderer.SetHasDarkBackground(dark)
	lipgloss.SetHasDarkBackground(dark)
	m := &Model{
		agent:        a,
		opts:         opts,
		r:            opts.Renderer,
		st:           st,
		md:           newMarkdown(style, profile),
		conversation: opts.Conversation,
		vp:           viewport.New(0, 0),
		follow:       true,
		in:           newInput(st, opts.Placeholder),
		focus:        -1,
		turnStart:    -1,
		sess:         opts.Session,
		events:       make(chan tea.Msg, 128),
	}
	m.vp.MouseWheelEnabled = true
	m.in.cmds = m.commands()
	if opts.Greeting != "" {
		m.entries = append(m.entries, &entry{kind: entryBlock, text: opts.Greeting})
	}
	if m.sess != nil {
		m.restore()
	}
	if opts.Side != nil {
		m.side = opts.Side()
		m.sideOpen = true
	}
	return m
}

// Conversation is the id exchanges are being sent under.
func (m *Model) Conversation() string { return m.conversation }

// setConversation is the one place the id moves, so it is the one
// place the application has to be told about it.
func (m *Model) setConversation(id string) {
	if id == m.conversation {
		return
	}
	m.conversation = id
	if m.opts.OnConversation != nil {
		m.opts.OnConversation(id)
	}
}

// restore rebuilds the transcript from the file. It folds the stored
// events through the same apply the live ones go through, so what a
// reopened conversation looks like is not a second rendering that has
// to be kept in step with the first — it is the first, replayed. What
// was streamed arrives whole, and every card comes back closed.
func (m *Model) restore() {
	for _, t := range m.sess.Turns() {
		if t.Said != "" {
			m.add(&entry{kind: entryUser, text: t.Said})
		}
		for _, ev := range t.Events {
			m.apply(ev)
		}
		m.commit()
		m.total += t.Cost
		m.totalIn += t.InputTokens
		m.totalOut += t.OutputTokens
	}
	m.in.load(m.sess.History())
}

// record is the turn that just ended, built from the entries it left
// behind rather than from the events that made them. The transcript
// is what we want back, so the transcript is what is written down.
func (m *Model) record() session.Turn {
	t := session.Turn{
		At: m.turnAt, Ended: time.Now(), Said: m.turnSaid,
		Cost: m.turnCost, InputTokens: m.turnIn, OutputTokens: m.turnOut,
	}
	for _, e := range m.entries[min(m.turnStart, len(m.entries)):] {
		switch e.kind {
		case entryReply:
			t.Events = append(t.Events, agent.Delta(e.text))
		case entryNotice:
			t.Events = append(t.Events, agent.Notice(e.text))
		case entryError:
			t.Events = append(t.Events, agent.Fail(e.text))
		case entryTool:
			t.Events = append(t.Events, agent.Call(e.id, e.name, e.args))
			if e.output != "" {
				t.Events = append(t.Events, agent.Output(e.id, e.output))
			}
			if !e.running {
				t.Events = append(t.Events, agent.Result(e.id, e.result, e.dur, e.cost))
			}
		}
	}
	return t
}

// commands is what completion offers: the application's, plus /help if
// it did not define one.
func (m *Model) commands() []Command {
	cmds := append([]Command(nil), m.opts.Commands...)
	for _, c := range cmds {
		if c.Name == "help" {
			return cmds
		}
	}
	return append(cmds, Command{Name: "help", Help: "the keys, and these commands"})
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink, m.waitEvent()}
	if m.opts.Notices != nil {
		cmds = append(cmds, m.pumpNotices())
	}
	if m.opts.Side != nil {
		cmds = append(cmds, tea.Tick(m.opts.SideRefresh, func(time.Time) tea.Msg { return sideTick{} }))
	}
	return tea.Batch(cmds...)
}

// waitEvent hands the next agent event to Update. Everything that
// arrives from off the UI goroutine goes through this one channel, so
// there is one place where ordering is decided.
func (m *Model) waitEvent() tea.Cmd {
	ch := m.events
	return func() tea.Msg { return <-ch }
}

func (m *Model) pumpNotices() tea.Cmd {
	src, out := m.opts.Notices, m.events
	return func() tea.Msg {
		for ev := range src {
			out <- eventMsg{ev}
		}
		return nil
	}
}

func (m *Model) tick() tea.Cmd {
	if m.animating {
		return nil
	}
	m.animating = true
	return tea.Tick(frameRate, func(time.Time) tea.Msg { return tickMsg{} })
}

// moving says whether anything on screen still needs redrawing.
func (m *Model) moving() bool {
	if m.inflight {
		return true
	}
	for _, e := range m.entries {
		if e.volatile() {
			return true
		}
	}
	return false
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.layout()
		return m, nil

	case tickMsg:
		m.animating = false
		m.frame++
		var cmd tea.Cmd
		if m.moving() {
			cmd = m.tick()
		}
		m.refresh()
		return m, cmd

	case sideTick:
		m.pollSide()
		return m, tea.Tick(m.opts.SideRefresh, func(time.Time) tea.Msg { return sideTick{} })

	case eventMsg:
		m.apply(msg.ev)
		m.refresh()
		return m, tea.Batch(m.waitEvent(), m.tick())

	case turnMsg:
		// It came down the same channel the events did, so everything
		// the agent said has already been folded in.
		m.finish(msg.err)
		m.refresh()
		return m, tea.Batch(m.waitEvent(), m.pollSideCmd())

	case noteMsg:
		m.add(&entry{kind: entryNotice, text: msg.text})
		m.refresh()
		return m, nil

	case failMsg:
		m.add(&entry{kind: entryError, text: msg.text})
		m.refresh()
		return m, nil

	case blockMsg:
		m.add(&entry{kind: entryBlock, text: msg.md})
		m.refresh()
		return m, nil

	case convMsg:
		m.setConversation(msg.id)
		return m, nil

	case sessionMsg:
		m.sess = msg.s
		return m, nil

	case sideMsg:
		m.side = msg.items
		return m, nil

	case refreshMsg:
		m.pollSide()
		return m, nil

	case tea.MouseMsg:
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		m.follow = m.vp.AtBottom()
		return m, cmd

	case tea.KeyMsg:
		return m.key(msg)
	}

	cmd := m.in.update(msg)
	m.layout()
	return m, cmd
}

func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit

	case "ctrl+d":
		if m.in.empty() {
			return m, tea.Quit
		}

	case "esc":
		// One key, three jobs, in the order you would want them:
		// put away the completion, let go of the card, stop the turn.
		switch {
		case m.in.completing():
			m.in.sugg = nil
		case m.focus >= 0:
			m.setFocus(-1)
		case m.inflight:
			m.stop()
		}
		m.refresh()
		return m, nil

	case "enter":
		// Enter finishes a half-typed command; a whole one it runs.
		if !m.in.exact() && m.in.accept() {
			m.layout()
			return m, nil
		}
		return m.submit()

	case "tab":
		if m.in.accept() {
			m.layout()
			return m, nil
		}
		m.focusCard(1)
		m.refresh()
		return m, nil

	case "shift+tab":
		m.focusCard(-1)
		m.refresh()
		return m, nil

	case "ctrl+o":
		m.toggleCard()
		m.refresh()
		return m, nil

	case "ctrl+t", "f2":
		m.sideOpen = !m.sideOpen
		m.layout()
		return m, nil

	case "ctrl+l":
		m.follow = true
		m.vp.GotoBottom()
		return m, nil

	case "up", "down":
		if m.in.completing() {
			m.in.move(map[string]int{"up": -1, "down": 1}[msg.String()])
			return m, nil
		}
		if m.in.browsing() {
			if m.in.browse(map[string]int{"up": -1, "down": 1}[msg.String()]) {
				m.layout()
				return m, nil
			}
		}

	case "pgup", "pgdown":
		// A focused, open card takes these for its own result; the
		// transcript gets them otherwise. Nothing else is taken from
		// the input: ctrl+u, ctrl+f and the rest still edit the line.
		if e := m.focused(); e != nil && e.expanded && !e.running {
			step := resultLines
			if msg.String() == "pgup" {
				step = -step
			}
			e.offset = clamp(e.offset+step, 0, max(0, e.resultHeight()-resultLines))
			e.invalidate()
			m.refresh()
			return m, nil
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		m.follow = m.vp.AtBottom()
		return m, cmd
	}

	cmd := m.in.update(msg)
	m.layout()
	return m, cmd
}

// submit sends what is in the box, or runs it as a command.
func (m *Model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.in.value())
	if text == "" {
		return m, nil
	}
	m.in.remember(text)
	if m.sess != nil {
		if err := m.sess.Remember(text); err != nil {
			m.add(&entry{kind: entryError, text: "session: " + err.Error()})
		}
	}
	m.in.reset()
	m.layout()

	if strings.HasPrefix(text, "/") {
		name, args, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
		return m, m.run(name, strings.TrimSpace(args))
	}
	if m.inflight {
		m.add(&entry{kind: entryError, text: "still answering — esc stops it"})
		m.refresh()
		return m, nil
	}
	m.add(&entry{kind: entryUser, text: text})
	cmd := m.send(text)
	m.refresh()
	return m, cmd
}

// run dispatches a slash command. /help is the terminal's own unless
// the application claimed it.
func (m *Model) run(name, args string) tea.Cmd {
	if name == "help" && !m.appHandles("help") {
		m.add(&entry{kind: entryBlock, text: m.helpText()})
		m.refresh()
		return nil
	}
	if m.opts.Handle == nil {
		m.add(&entry{kind: entryError, text: "unknown command /" + name})
		m.refresh()
		return nil
	}
	return m.opts.Handle(name, args)
}

func (m *Model) appHandles(name string) bool {
	for _, c := range m.opts.Commands {
		if c.Name == name {
			return true
		}
	}
	return false
}

// send starts an exchange. Everything about it — the call itself, the
// stream, the end — happens off the UI goroutine, because an agent
// over HTTP can take as long as it likes to answer the first byte.
func (m *Model) send(text string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.inflight = true
	m.statusText = ""
	m.partial = ""
	m.follow = true

	// The question is already in the transcript; the agent's side of
	// this turn starts here.
	m.turnStart = len(m.entries)
	m.turnAt, m.turnSaid = time.Now(), text
	m.turnCost, m.turnIn, m.turnOut = 0, 0, 0

	// The end of the turn goes down the same channel as the events,
	// not back as this command's result: a tea.Cmd's return value can
	// overtake what is still queued, and a turn that ends early loses
	// the last of the reply.
	a, conv, out := m.agent, m.conversation, m.events
	stream := func() tea.Msg {
		ch, err := a.Send(ctx, conv, text)
		if err != nil {
			out <- turnMsg{err}
			return nil
		}
		for ev := range ch {
			out <- eventMsg{ev}
		}
		out <- turnMsg{nil}
		return nil
	}
	return tea.Batch(stream, m.tick())
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

// apply folds one event into the transcript.
func (m *Model) apply(ev agent.Event) {
	switch ev.Kind {
	case agent.KindStatus:
		m.statusText = ev.Text

	case agent.KindDelta:
		m.partial += ev.Text
		m.statusText = ""

	case agent.KindToolCall:
		// Text before a tool call is finished text; close it off so
		// the card lands after it and not on top of it.
		m.commit()
		m.add(&entry{kind: entryTool, id: ev.ID, name: ev.Name, args: ev.Args, running: true})
		m.statusText = ""

	case agent.KindToolOutput:
		if e := m.openCard(ev.ID); e != nil {
			e.output += ev.Text
			e.invalidate()
		}

	case agent.KindToolResult:
		if e := m.openCard(ev.ID); e != nil {
			e.result, e.dur, e.cost, e.running = ev.Result, ev.Duration, ev.Cost, false
			e.invalidate()
		}
		m.spend(ev.Cost, 0, 0)

	case agent.KindNotice:
		// A notice belongs to the world, not to the reply, so it goes
		// above the answer still being written rather than splitting it.
		m.add(&entry{kind: entryNotice, text: ev.Text})

	case agent.KindError:
		m.commit()
		m.add(&entry{kind: entryError, text: ev.Text})

	case agent.KindDone:
		// Cost on done is the model's own spend for the whole turn;
		// the tools have already reported theirs.
		m.spend(ev.Cost, ev.InputTokens, ev.OutputTokens)
		m.finish(nil)
	}
}

// openCard finds the running card a tool event belongs to. An event
// for a call nobody made is dropped, not crashed on.
func (m *Model) openCard(id string) *entry {
	for i := len(m.entries) - 1; i >= 0; i-- {
		e := m.entries[i]
		if e.kind == entryTool && e.id == id && e.running {
			if i < m.stableN {
				m.resetStable()
			}
			return e
		}
	}
	return nil
}

// spend adds to the turn's running total. It only counts while a turn
// is in flight, so replaying a file does not charge for it twice.
func (m *Model) spend(cost float64, in, out int) {
	if m.turnStart < 0 {
		return
	}
	m.turnCost += cost
	m.turnIn += in
	m.turnOut += out
}

func (m *Model) finish(err error) {
	m.commit()
	if err != nil {
		m.add(&entry{kind: entryError, text: err.Error()})
	}
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.inflight = false
	m.statusText = ""
	// A call the turn ended without is not running any more, whatever
	// it was doing: close it, or its spinner turns forever and comes
	// back turning when the conversation is reopened.
	for _, e := range m.entries {
		if e.kind == entryTool && e.running {
			e.running = false
			e.invalidate()
			m.resetStable()
		}
	}
	if m.turnStart < 0 {
		return // done and the end of the stream both land here
	}
	turn := m.record()
	m.turnStart = -1
	m.total += turn.Cost
	m.totalIn += turn.InputTokens
	m.totalOut += turn.OutputTokens
	if m.sess != nil {
		if err := m.sess.Append(turn); err != nil {
			m.add(&entry{kind: entryError, text: "session: " + err.Error()})
		}
	}
}

// commit turns the reply in flight into a finished entry.
func (m *Model) commit() {
	if strings.TrimSpace(m.partial) == "" {
		m.partial = ""
		return
	}
	m.add(&entry{kind: entryReply, text: m.partial})
	m.partial = ""
}

func (m *Model) add(e *entry) { m.entries = append(m.entries, e) }

// --- tool card focus ----------------------------------------------------

func (m *Model) focused() *entry {
	if m.focus < 0 || m.focus >= len(m.entries) {
		return nil
	}
	return m.entries[m.focus]
}

// focusCard walks the tool cards. From nowhere, forward lands on the
// last one — the one you almost always mean.
func (m *Model) focusCard(dir int) {
	idx := m.cards()
	if len(idx) == 0 {
		return
	}
	at := -1
	for i, e := range idx {
		if e == m.focus {
			at = i
		}
	}
	switch {
	case at < 0 && dir > 0:
		m.setFocus(idx[len(idx)-1])
	case at < 0:
		m.setFocus(idx[0])
	default:
		next := at + dir
		if next < 0 || next >= len(idx) {
			m.setFocus(-1)
			return
		}
		m.setFocus(idx[next])
	}
}

func (m *Model) cards() []int {
	var out []int
	for i, e := range m.entries {
		if e.kind == entryTool {
			out = append(out, i)
		}
	}
	return out
}

func (m *Model) setFocus(i int) {
	if m.focus == i {
		return
	}
	old := m.focus
	m.focus = i
	for _, j := range []int{old, i} {
		if j >= 0 && j < len(m.entries) {
			m.entries[j].invalidate()
			if j < m.stableN {
				m.resetStable()
			}
		}
	}
}

// toggleCard opens the focused card, or the last one when nothing is
// focused — the shortcut for "show me what that just did".
func (m *Model) toggleCard() {
	i := m.focus
	if i < 0 {
		idx := m.cards()
		if len(idx) == 0 {
			return
		}
		i = idx[len(idx)-1]
	}
	e := m.entries[i]
	e.expanded = !e.expanded
	e.offset = 0
	e.invalidate()
	if i < m.stableN {
		m.resetStable()
	}
	m.follow = true
}

// --- layout and drawing -------------------------------------------------

func (m *Model) sideVisible() bool {
	return m.opts.Side != nil && m.sideOpen && m.width >= m.opts.SideMinWidth
}

func (m *Model) sideWidth() int {
	if !m.sideVisible() {
		return 0
	}
	return clamp(m.opts.SideWidth, 16, m.width/3)
}

func (m *Model) pollSide() {
	if m.opts.Side != nil {
		m.side = m.opts.Side()
	}
}

func (m *Model) pollSideCmd() tea.Cmd {
	if m.opts.Side == nil {
		return nil
	}
	return Refresh()
}

// layout sizes the viewport around everything that is not it.
func (m *Model) layout() {
	if !m.ready {
		return
	}
	chrome := 1 /*rule*/ + m.in.height() + 1 /*status*/ + len(m.renderSuggestions(m.width))
	h := max(m.height-chrome, 3)
	w := max(m.width-m.sideWidth(), 20)
	m.in.ta.SetWidth(max(m.width-2, 20))
	if m.vp.Width == w && m.vp.Height == h {
		return // typing does not move anything; do not redraw the transcript
	}
	if m.vp.Width != w {
		m.resetStable()
	}
	m.vp.Width, m.vp.Height = w, h
	m.refresh()
}

func (m *Model) resetStable() { m.stable, m.stableN, m.stableW = "", 0, 0 }

// refresh rebuilds what the viewport shows and keeps it at the tail
// unless the person has scrolled away from it.
func (m *Model) refresh() {
	if !m.ready {
		return
	}
	m.vp.SetContent(m.transcript())
	if m.follow {
		m.vp.GotoBottom()
	}
}

// transcript is the whole conversation as one string. Finished entries
// come out of the cache; only the tail — a tool still running, the
// reply still arriving — is drawn again.
func (m *Model) transcript() string {
	w := m.vp.Width
	if m.stableW != w {
		m.resetStable()
		m.stableW = w
	}
	limit := len(m.entries)
	if m.focus >= 0 {
		limit = min(limit, m.focus)
	}
	var grown strings.Builder
	for m.stableN < limit && !m.entries[m.stableN].volatile() {
		grown.WriteString(m.renderEntry(m.entries[m.stableN], w, false))
		grown.WriteString("\n\n")
		m.stableN++
	}
	m.stable += grown.String()

	var b strings.Builder
	b.WriteString(m.stable)
	for i := m.stableN; i < len(m.entries); i++ {
		b.WriteString(m.renderEntry(m.entries[i], w, i == m.focus))
		b.WriteString("\n\n")
	}
	if m.partial != "" {
		b.WriteString(m.md.render(m.partial, w, true))
		b.WriteString("\n\n")
	}
	switch {
	case m.statusText != "":
		b.WriteString(m.st.status.Render(spinnerFrame(m.frame) + " " + m.statusText))
	case m.inflight && m.partial == "":
		b.WriteString(m.st.status.Render(spinnerFrame(m.frame) + " thinking"))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) View() string {
	if !m.ready {
		return ""
	}
	body := m.vp.View()
	if m.sideVisible() {
		body = lipgloss.JoinHorizontal(lipgloss.Top, body, m.renderSide(m.sideWidth(), m.vp.Height))
	}
	parts := []string{body}
	parts = append(parts, m.renderSuggestions(m.width)...)
	parts = append(parts, m.st.rule.Render(strings.Repeat("─", max(m.width, 1))))
	parts = append(parts, m.in.ta.View())
	parts = append(parts, m.statusLine())
	return strings.Join(parts, "\n")
}

// statusLine is who is answering, on what, in which conversation, and
// whether they are busy — then whatever hints still fit.
func (m *Model) statusLine() string {
	left := m.opts.Name
	if m.opts.Model != "" {
		left += " · " + m.opts.Model
	}
	if m.conversation != "" {
		left += " · " + m.conversation
	}
	if m.inflight {
		left += " · " + spinnerFrame(m.frame) + " working"
	}
	if t := m.totals(); t != "" {
		left += " · " + t
	}
	right := m.hints()
	pad := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if pad < 1 {
		return ansi.Truncate(m.st.statusbar.Render(left), max(m.width, 1), "…")
	}
	return m.st.statusbar.Render(left) + strings.Repeat(" ", pad) + m.st.hint.Render(right) + " "
}

// totals is what has been spent: this turn while it runs, and the
// whole conversation once it is over. A turn's number moves, which is
// the one worth watching; a conversation's is what you look at
// afterwards. Both are only there when somebody knew a number.
func (m *Model) totals() string {
	cost, in, out := m.total, m.totalIn, m.totalOut
	if m.inflight {
		cost, in, out = m.turnCost, m.turnIn, m.turnOut
	}
	var parts []string
	if cost > 0 {
		parts = append(parts, formatCost(cost))
	}
	if n := in + out; n > 0 {
		parts = append(parts, formatTokens(n)+" tok")
	}
	return strings.Join(parts, " · ")
}

func (m *Model) hints() string {
	if e := m.focused(); e != nil {
		if e.expanded {
			return "ctrl+o close · pgup/pgdn result · esc unfocus"
		}
		return "ctrl+o open · tab next card · esc unfocus"
	}
	if m.inflight {
		return "esc stop"
	}
	long := "enter send · alt+enter newline · tab card · ctrl+o expand · ctrl+t side · /help"
	if m.width >= 100 {
		return long
	}
	return "enter send · tab card · /help"
}

func (m *Model) helpText() string {
	var b strings.Builder
	b.WriteString("### keys\n\n")
	b.WriteString("| key | does |\n| --- | --- |\n")
	for _, r := range [][2]string{
		{"enter", "send"},
		{"alt+enter, shift+enter, ctrl+j", "newline"},
		{"up / down", "history, when the box is empty"},
		{"tab / shift+tab", "focus a tool card (tab first accepts a completion)"},
		{"ctrl+o", "expand the focused card, or the last one"},
		{"pgup / pgdn", "scroll — the focused card's result if one is open"},
		{"ctrl+l", "back to the tail"},
		{"ctrl+t, f2", "the side pane"},
		{"esc", "close completion, let go of a card, stop the turn"},
		{"ctrl+c, ctrl+d", "quit"},
	} {
		b.WriteString("| " + r[0] + " | " + r[1] + " |\n")
	}
	if cmds := m.commands(); len(cmds) > 0 {
		b.WriteString("\n### commands\n\n")
		for _, c := range cmds {
			b.WriteString("- `/" + c.Name + "` — " + c.Help + "\n")
		}
	}
	return b.String()
}
