package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/incantery/mote/agent"
)

type entryKind int

const (
	entryUser entryKind = iota
	entryReply
	entryNotice
	entryError
	entryTool
	entryAsk   // a question the agent stopped to ask
	entryBlock // markdown the application put there, not the agent
	entryShow  // what a command printed: a block, in its own gutter
)

// The registers a transcript is written in. Every entry belongs to
// exactly one of them and looks like nothing else:
//
//	you     the person's turn, warm and in the margin
//	reply   the model's prose, full width, ordinary weight
//	tool    a card — one sentence closed, args and result open
//	event   a notice from outside the exchange: dim, indented, grouped
//	result  what a command printed: Note dim like an event, Show a block
//	error   red, one line
//
// Which is which is the kind above; how each one reads is renderProse
// and renderTool below.

// entry is one thing in the transcript. A finished one renders once
// per width and then holds its bytes; only a tool call still running
// is re-rendered, because it has a spinner in it.
type entry struct {
	kind entryKind
	text string

	id       string
	name     string
	args     string
	summary  string // the sentence the agent wanted on the card, if any
	output   string // what the tool printed as it ran
	result   string // what it returned when it ended
	dur      time.Duration
	cost     float64
	running  bool
	started  time.Time // when the call went out, for the elapsed on a running card
	expanded bool
	offset   int // first body line shown, when expanded

	// at is when the exchange this entry opens began. Only a user
	// entry has one, because only a user entry opens an exchange —
	// it is what Options.Timestamps puts on the rule above it.
	at time.Time

	// An ask's: what the person said, and whether the turn ended
	// before they could say it.
	answer    string
	cancelled bool

	// A notice's: the colour its gutter carries, if any, and the
	// command that opens what it is about, if there is one. A notice
	// with an open is a card of a kind — tab reaches it, enter runs
	// the command.
	tone agent.Tone
	open string

	cache  string
	cacheW int
	cacheK string
}

func (e *entry) volatile() bool { return e.kind == entryTool && e.running }

// stopped is a call the turn ended without: no result, no output, no
// time. The person pressed esc, or the agent said done with a call
// still open. It is derived rather than stored so that it survives a
// round trip through the events in a session file, which have nowhere
// else to put it.
func (e *entry) stopped() bool {
	return e.kind == entryTool && !e.running && e.dur == 0 &&
		e.result == "" && e.output == ""
}

// body is everything the tool said, in order: what it printed as it
// ran, then what it returned. A tool that streams and then returns
// nothing keeps its output; one that only returns shows the result.
func (e *entry) body() string {
	switch {
	case e.output == "":
		return e.result
	case e.result == "":
		return e.output
	}
	return strings.TrimRight(e.output, "\n") + "\n" + e.result
}

// invalidate drops the cached render. Anything that changes what an
// entry looks like has to call it.
func (e *entry) invalidate() { e.cacheW = 0; e.cacheK = "" }

// renderEntry draws one entry at width w, from cache when it can.
func (m *Model) renderEntry(e *entry, w int, focused bool) string {
	if e.kind == entryAsk {
		key := "ask|" + e.answer
		if e.cancelled {
			key += "|cancelled"
		}
		if e.cacheW == w && e.cacheK == key {
			return e.cache
		}
		out := m.renderAsk(e, w)
		e.cache, e.cacheW, e.cacheK = out, w, key
		return out
	}
	if e.kind == entryTool {
		key := fmt.Sprintf("%v|%v|%d|%d", e.expanded, focused, e.offset, m.frame)
		if !e.volatile() {
			key = fmt.Sprintf("%v|%v|%d", e.expanded, focused, e.offset)
		}
		if e.cacheW == w && e.cacheK == key {
			return e.cache
		}
		out := m.renderTool(e, w, focused)
		e.cache, e.cacheW, e.cacheK = out, w, key
		return out
	}
	key := ""
	if e.kind == entryNotice && focused {
		key = "focused"
	}
	if e.cacheW == w && e.cacheK == key {
		return e.cache
	}
	out := m.renderProse(e, w, focused)
	e.cache, e.cacheW, e.cacheK = out, w, key
	return out
}

func (m *Model) renderProse(e *entry, w int, focused bool) string {
	switch e.kind {
	case entryUser:
		return hang(m.st.user, "› ", "  ", e.text, w)
	case entryNotice:
		return m.renderNotice(e, w, focused)
	case entryError:
		return hang(m.st.errline, "✗ ", "  ", e.text, w)
	case entryShow:
		return m.showBlock(e.text, w)
	case entryReply, entryBlock:
		return m.md.render(e.text, w, false)
	}
	return ""
}

// noticeInset is how much narrower than the reply a notice is drawn.
// The gutter is two of it; the rest is so that the block reads as an
// aside even when every line of it is short.
const noticeInset = 8

// renderNotice is a notice down a gutter: narrower than the reply and
// indented under it, because a notice is the world talking over the
// exchange, not part of it. The gutter is a bar the whole height of
// the block, and the bar is where the notice's one colour goes — dim
// for a thing that happened, the Needs colour for a thing that is
// asking, the Error colour for a thing that failed. The words stay
// dim whatever the tone: the reference tints the gutter, and only the
// gutter, so that red stays a shape on the margin rather than a
// paragraph of it.
//
// A focused one — reached with tab because it has a command to open
// — wears the accent instead, the same bar a focused tool card does:
// one colour for "this is the thing the keyboard is on", everywhere.
func (m *Model) renderNotice(e *entry, w int, focused bool) string {
	bar := m.st.event
	switch {
	case focused:
		bar = m.st.accent
	case e.tone == agent.ToneNeeds:
		bar = m.st.needs
	case e.tone == agent.ToneFailed:
		bar = m.st.errline
	}
	return hangBar(bar, m.st.event, "  ▏ ", "  ▏ ", e.text, max(w-noticeInset, 20))
}

// showBlock is markdown a command printed, down its own gutter. The
// reply is speech and takes the whole width; this is a thing handed
// over — a report, a policy — and a person should be able to tell at
// a glance which of the two they are reading.
func (m *Model) showBlock(md string, w int) string {
	body := m.md.render(md, max(w-2, 20), false)
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		lines[i] = m.st.result.Render("▏ ") + l
	}
	return strings.Join(lines, "\n")
}

// --- tool cards ---------------------------------------------------------

// resultLines is how much of a tool's output an expanded card shows
// at once. Results are routinely thousands of lines; the card is a
// window on one, not a copy of it.
const resultLines = 12

func (m *Model) renderTool(e *entry, w int, focused bool) string {
	gutter, gst := "  ", m.st.dim
	if focused {
		gutter, gst = "┃ ", m.st.accent
	}
	inner := w - 2
	if inner < 20 {
		inner = 20
	}

	arrow := "▸"
	if e.expanded {
		arrow = "▾"
	}
	mark, mst := "✓", m.st.dim
	switch {
	case e.running:
		mark, mst = spinnerFrame(m.frame), m.st.status
	case e.stopped(), strings.HasPrefix(e.result, "error:"):
		mark, mst = "✗", m.st.errline
	}

	// The tail: what it is taking while it runs, what it took when it
	// is over. It is the part that never gives up its room — a card
	// whose sentence has been cut still says how long it went on for.
	right := ""
	switch {
	case e.running:
		right = "…"
		if d := e.elapsed(); d >= time.Second {
			right += " " + formatDuration(d.Round(100*time.Millisecond))
		}
		if e.output != "" {
			right += " · " + formatVolume(e.output)
		}
	case e.stopped():
		right = "stopped"
	default:
		right = formatDuration(e.dur)
		if e.cost > 0 {
			right += " · " + formatCost(e.cost)
		}
	}

	// One line that reads like a sentence: what happened, to what,
	// and how long it took. "✓ fleet · started a ship task in vera →
	// 05a40191 · 473ms" — not a dump of the arguments it was called
	// with, which is what the card holds open for anybody who wants
	// it.
	head := m.st.dim.Render(arrow) + " " + mst.Render(mark) + " " + m.st.tool.Render(e.name)
	used := 1 + 1 + 1 + 1 + lipgloss.Width(e.name)
	if right != "" {
		used += 3 + lipgloss.Width(right)
	}
	if says := e.says(max(8, inner-used-3)); says != "" {
		// The sentence is the card, so it is written in the colour
		// prose is written in. Everything around it — the arrow, the
		// mark, what it took — is dim, because none of it is what a
		// person is reading the line for.
		head += m.st.dim.Render(" · ") + m.st.text.Render(says)
	}
	if right != "" {
		head += m.st.dim.Render(" · " + right)
	}

	lines := []string{head}
	if e.expanded {
		lines = append(lines, m.st.dim.Render("  args"))
		for _, l := range strings.Split(prettyJSON(e.args), "\n") {
			lines = append(lines, m.st.toolArgs.Render("    "+ansi.Truncate(l, inner-4, "…")))
		}
		switch text := e.body(); {
		case e.running && text == "":
			lines = append(lines, m.st.status.Render("  running…"))
		case e.stopped():
			lines = append(lines, m.st.errline.Render("  stopped — the turn ended first"))
		default:
			what := "result"
			if e.output != "" {
				what = "output"
			}
			body := strings.Split(strings.TrimRight(text, "\n"), "\n")
			// A tool still printing is followed, not paged: the end
			// of what it has said is the part worth looking at.
			from := clamp(e.offset, 0, max(0, len(body)-1))
			if e.running {
				from = max(0, len(body)-resultLines)
			}
			to := min(from+resultLines, len(body))
			window := fmt.Sprintf("  %s · lines %d–%d of %d", what, from+1, to, len(body))
			if len(body) <= resultLines {
				window = fmt.Sprintf("  %s · %d lines", what, len(body))
			}
			lines = append(lines, m.st.dim.Render(window))
			for _, l := range body[from:to] {
				lines = append(lines, m.st.text.Render("    "+ansi.Truncate(l, inner-4, "…")))
			}
		}
	}
	for i, l := range lines {
		lines[i] = gst.Render(gutter) + l
	}
	return strings.Join(lines, "\n")
}

// says is the one line a card reads as, at most width columns of it:
// the summary the agent gave, and failing that the arguments it was
// called with, cut down until they fit.
func (e *entry) says(width int) string {
	if s := oneLine(e.summary); s != "" {
		return ansi.Truncate(s, width, "…")
	}
	return summarizeCall(e.args, width)
}

// elapsed is how long a running call has been running. Zero for one
// restored from a file, which was never running here.
func (e *entry) elapsed() time.Duration {
	if e.started.IsZero() {
		return 0
	}
	return time.Since(e.started)
}

// resultHeight is how many lines an expanded card's body has, so the
// keys that scroll it know where the end is.
func (e *entry) resultHeight() int {
	return len(strings.Split(strings.TrimRight(e.body(), "\n"), "\n"))
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func spinnerFrame(i int) string { return spinner[((i%len(spinner))+len(spinner))%len(spinner)] }

// summarizeCall is the fallback for a call nobody summarized: the
// values, in the order they were written, without their keys. A tool
// call is a verb and its object, and "README.md" is the object —
// "path=README.md limit=200" is the wire format, which is what the
// card holds open for. Nested values have no short form worth
// reading, so a call made entirely of them keeps its keys instead.
func summarizeCall(args string, width int) string {
	vals := argValues(args)
	if len(vals) == 0 {
		return summarizeArgs(args, width)
	}
	return ansi.Truncate(strings.Join(vals, ", "), width, "…")
}

// argValues is the scalar values of a JSON object, in order.
func argValues(args string) []string {
	args = strings.TrimSpace(args)
	if args == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(args))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil
	}
	var out []string
	for dec.More() {
		if _, err := dec.Token(); err != nil {
			break
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			break
		}
		s := strings.TrimSpace(string(raw))
		if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
			continue // a shape, not a word: it belongs in the open card
		}
		if v := shortValue(raw); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// summarizeArgs turns a tool's JSON arguments into one line, in the
// order the agent wrote them — a decoder walk rather than a map, so
// the summary reads like the call did.
func summarizeArgs(args string, width int) string {
	args = strings.TrimSpace(args)
	if args == "" || width <= 0 {
		return ""
	}
	dec := json.NewDecoder(strings.NewReader(args))
	tok, err := dec.Token()
	if err != nil {
		return ansi.Truncate(oneLine(args), width, "…")
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return ansi.Truncate(oneLine(args), width, "…")
	}
	var parts []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			break
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			break
		}
		parts = append(parts, fmt.Sprintf("%v=%s", k, shortValue(raw)))
	}
	if len(parts) == 0 {
		return ""
	}
	return ansi.Truncate(strings.Join(parts, " "), width, "…")
}

func shortValue(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(s, "{"):
		return "{…}"
	case strings.HasPrefix(s, "["):
		return "[…]"
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		s = str
	}
	return ansi.Truncate(oneLine(s), 28, "…")
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func prettyJSON(s string) string {
	var buf bytes.Buffer
	if json.Indent(&buf, []byte(s), "", "  ") == nil {
		return buf.String()
	}
	return s
}

// --- small formatting ---------------------------------------------------

func formatDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return ""
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.2fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// formatVolume is how much a running tool has printed. Lines are what
// a person counts; bytes are what tells them it is still moving when
// the lines are long.
func formatVolume(s string) string {
	lines := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		lines++
	}
	unit := "lines"
	if lines == 1 {
		unit = "line"
	}
	return fmt.Sprintf("%d %s · %s", lines, unit, formatBytes(len(s)))
}

func formatBytes(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d B", n)
	case n < 1000*1000:
		return fmt.Sprintf("%.1f kB", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/1000/1000)
	}
}

// formatTokens is a token count small enough for a status line.
func formatTokens(n int) string {
	switch {
	case n < 10000:
		return fmt.Sprintf("%d", n)
	case n < 1000*1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1000/1000)
	}
}

func formatCost(c float64) string {
	if c >= 1 {
		return fmt.Sprintf("$%.2f", c)
	}
	return fmt.Sprintf("$%.4f", c)
}

// hang wraps text under a prefix, with continuation lines indented to
// match. lipgloss wraps; it does not hang.
func hang(st lipgloss.Style, prefix, cont, text string, w int) string {
	return hangBar(st, st, prefix, cont, text, w)
}

// hangBar is hang with the prefix in a style of its own, which is how
// a gutter gets a colour the words do not have.
func hangBar(pst, st lipgloss.Style, prefix, cont, text string, w int) string {
	inner := w - lipgloss.Width(prefix)
	if inner < 8 {
		inner = 8
	}
	wrapped := ansi.Wrap(text, inner, breakpoints(text))
	var b strings.Builder
	for i, l := range strings.Split(wrapped, "\n") {
		if i > 0 {
			b.WriteString("\n" + pst.Render(cont))
		} else {
			b.WriteString(pst.Render(prefix))
		}
		b.WriteString(st.Render(l))
	}
	return b.String()
}

// breakpoints is where a line may wrap. A space, a hyphen and a slash
// are all fine in prose, and a slash is where a long path wants to
// break — but a slash that begins a word is a slash command, the one
// token on the line that must survive whole, and a line that ends in
// "/" with "report a3f2" under it is the one thing nobody can read.
// So a text with a word-initial slash in it wraps on spaces and
// hyphens only; its paths, if it has any, wrap where they must.
func breakpoints(text string) string {
	if strings.HasPrefix(text, "/") || strings.Contains(text, " /") || strings.Contains(text, "\n/") {
		return " -"
	}
	return " -/"
}

func clamp(v, lo, hi int) int { return min(max(v, lo), hi) }
