package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/muesli/termenv"
)

// markdown renders assistant text. Building a glamour renderer is the
// expensive part — it parses a style and builds a syntax highlighter —
// so there is one per width, kept until the window changes size.
type markdown struct {
	style   string
	profile termenv.Profile
	byWidth map[int]*glamour.TermRenderer
}

func newMarkdown(style string, profile termenv.Profile) *markdown {
	if style == "" {
		style = "auto"
	}
	return &markdown{style: style, profile: profile, byWidth: map[int]*glamour.TermRenderer{}}
}

func (md *markdown) renderer(width int) *glamour.TermRenderer {
	if r, ok := md.byWidth[width]; ok {
		return r
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(md.style),
		glamour.WithColorProfile(md.profile),
		glamour.WithWordWrap(width),
		glamour.WithEmoji(),
	)
	if err != nil {
		return nil
	}
	md.byWidth[width] = r
	return r
}

// render lays out markdown at width. Text that is still arriving is
// closed off first (see balance) so a half-written fence does not make
// the rest of the reply jump around as it streams.
func (md *markdown) render(s string, width int, streaming bool) string {
	if width < 20 {
		width = 20
	}
	in := s
	if streaming {
		in = balance(in)
	}
	r := md.renderer(width)
	if r == nil {
		return strings.TrimRight(in, "\n")
	}
	out, err := r.Render(in)
	if err != nil {
		return strings.TrimRight(in, "\n")
	}
	// glamour pads with a blank line top and bottom; the transcript
	// puts its own space between turns.
	return strings.Trim(out, "\n")
}

// balance closes an unterminated fenced code block. Without it the
// tail of a streaming reply flips between "prose" and "code" on every
// delta, which is the flicker people notice most.
func balance(s string) string {
	fences := 0
	for line := range strings.SplitSeq(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fences++
		}
	}
	if fences%2 == 1 {
		if !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		s += "```"
	}
	return s
}
