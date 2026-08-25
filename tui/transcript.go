package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type entryKind int

const (
	entryUser entryKind = iota
	entryReply
	entryNotice
	entryError
	entryTool
	entryBlock // markdown the application put there, not the agent
)

// entry is one thing in the transcript. A finished one renders once
// per width and then holds its bytes; only a tool call still running
// is re-rendered, because it has a spinner in it.
type entry struct {
	kind entryKind
	text string

	id       string
	name     string
	args     string
	result   string
	dur      time.Duration
	cost     float64
	running  bool
	expanded bool
	offset   int // first result line shown, when expanded

	cache  string
	cacheW int
	cacheK string
}

func (e *entry) volatile() bool { return e.kind == entryTool && e.running }

// invalidate drops the cached render. Anything that changes what an
// entry looks like has to call it.
func (e *entry) invalidate() { e.cacheW = 0; e.cacheK = "" }

// renderEntry draws one entry at width w, from cache when it can.
func (m *Model) renderEntry(e *entry, w int, focused bool) string {
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
	if e.cacheW == w && e.cacheK == "" {
		return e.cache
	}
	out := m.renderProse(e, w)
	e.cache, e.cacheW, e.cacheK = out, w, ""
	return out
}

func (m *Model) renderProse(e *entry, w int) string {
	switch e.kind {
	case entryUser:
		return hang(m.st.user, "› ", "  ", e.text, w)
	case entryNotice:
		return hang(m.st.dim, "· ", "  ", e.text, w)
	case entryError:
		return hang(m.st.errline, "✗ ", "  ", e.text, w)
	case entryReply, entryBlock:
		return m.md.render(e.text, w, false)
	}
	return ""
}

// --- tool cards ---------------------------------------------------------

// resultLines is how much of a tool result an expanded card shows at
// once. Results are routinely thousands of lines; the card is a
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
	case strings.HasPrefix(e.result, "error:"):
		mark, mst = "✗", m.st.errline
	}

	right := ""
	if !e.running {
		right = formatDuration(e.dur)
		if e.cost > 0 {
			right += "  " + formatCost(e.cost)
		}
	}

	head := m.st.dim.Render(arrow) + " " + mst.Render(mark) + " " + m.st.tool.Render(e.name)
	used := 1 + 1 + 1 + 1 + lipgloss.Width(e.name)
	summary := summarizeArgs(e.args, max(8, inner-used-lipgloss.Width(right)-3))
	if summary != "" {
		head += "  " + m.st.toolArgs.Render(summary)
		used += 2 + lipgloss.Width(summary)
	}
	if right != "" {
		pad := inner - used - lipgloss.Width(right)
		if pad < 1 {
			pad = 1
		}
		head += strings.Repeat(" ", pad) + m.st.dim.Render(right)
	}

	lines := []string{head}
	if e.expanded {
		lines = append(lines, m.st.dim.Render("  args"))
		for _, l := range strings.Split(prettyJSON(e.args), "\n") {
			lines = append(lines, m.st.toolArgs.Render("    "+ansi.Truncate(l, inner-4, "…")))
		}
		if e.running {
			lines = append(lines, m.st.status.Render("  running…"))
		} else {
			body := strings.Split(strings.TrimRight(e.result, "\n"), "\n")
			from := clamp(e.offset, 0, max(0, len(body)-1))
			to := min(from+resultLines, len(body))
			head := fmt.Sprintf("  result · lines %d–%d of %d", from+1, to, len(body))
			if len(body) <= resultLines {
				head = fmt.Sprintf("  result · %d lines", len(body))
			}
			lines = append(lines, m.st.dim.Render(head))
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

// resultHeight is how many lines an expanded card's result has, so the
// keys that scroll it know where the end is.
func (e *entry) resultHeight() int {
	return len(strings.Split(strings.TrimRight(e.result, "\n"), "\n"))
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func spinnerFrame(i int) string { return spinner[((i%len(spinner))+len(spinner))%len(spinner)] }

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

func formatCost(c float64) string {
	if c >= 1 {
		return fmt.Sprintf("$%.2f", c)
	}
	return fmt.Sprintf("$%.4f", c)
}

// hang wraps text under a prefix, with continuation lines indented to
// match. lipgloss wraps; it does not hang.
func hang(st lipgloss.Style, prefix, cont, text string, w int) string {
	inner := w - lipgloss.Width(prefix)
	if inner < 8 {
		inner = 8
	}
	wrapped := lipgloss.NewStyle().Width(inner).Render(text)
	var b strings.Builder
	for i, l := range strings.Split(wrapped, "\n") {
		if i > 0 {
			b.WriteString("\n" + cont)
		} else {
			b.WriteString(prefix)
		}
		b.WriteString(l)
	}
	return st.Render(b.String())
}

func clamp(v, lo, hi int) int { return min(max(v, lo), hi) }
