package tui

import (
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/glamour"
	gstyles "github.com/charmbracelet/glamour/styles"
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

// newMarkdown takes a style that has already been resolved: nothing
// below this line is allowed to ask the terminal anything.
func newMarkdown(style string, profile termenv.Profile) *markdown {
	return &markdown{style: style, profile: profile, byWidth: map[int]*glamour.TermRenderer{}}
}

// resolveStyle turns a glamour style name into one that can be drawn
// with while the program owns the terminal. "auto" is not one.
//
// Deciding "auto" means asking the terminal what colour it is: twelve
// bytes out — an OSC 11 query and a cursor-position request after it —
// and the answer arrives on the same device, byte by byte. Glamour
// asks when it builds a renderer, which is the first time a reply has
// to be drawn, and by then Bubble Tea is reading stdin. A terminal
// that answers has its answer typed into the input box; a terminal
// that does not answer costs five seconds of blank screen, once per
// width, because that is termenv's timeout.
//
// So nothing here asks. What is known without asking is enough:
// GLAMOUR_STYLE if the person set it, notty if nothing is going to a
// terminal at all, COLORFGBG if the terminal published its own
// background, and dark otherwise — the rest of the palette is ANSI
// 1–6 and 8, which reads on either background. Somebody who wants the
// other one says so outright: Palette.Markdown, or `mote demo -light`.
func resolveStyle(name string, profile termenv.Profile) string {
	if name != "" && name != gstyles.AutoStyle {
		return name
	}
	if s := os.Getenv("GLAMOUR_STYLE"); s != "" && s != gstyles.AutoStyle {
		return s
	}
	if profile == termenv.Ascii {
		return gstyles.NoTTYStyle
	}
	if lightBackground(os.Getenv("COLORFGBG")) {
		return gstyles.LightStyle
	}
	return gstyles.DarkStyle
}

// darkBackground is the same question one step further on: not which
// style to render markdown with, but what the terminal is, which is
// what lipgloss's AdaptiveColor asks the terminal if nobody answers it
// first — and bubbles' textarea is full of AdaptiveColor. Dark unless
// something said otherwise, for the same reason as above.
func darkBackground(style string) bool {
	return style != gstyles.LightStyle && !lightBackground(os.Getenv("COLORFGBG"))
}

// lightBackground reads COLORFGBG, which is how a terminal says what
// colour it is without being asked: "15;0" is a light foreground on a
// dark background, and the last field is always the background. rxvt
// writes three fields, so it is the last one that counts.
func lightBackground(colorFGBG string) bool {
	fields := strings.Split(colorFGBG, ";")
	if len(fields) < 2 {
		return false
	}
	n, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil || n < 0 || n > 15 {
		return false
	}
	_, _, l := termenv.ConvertToRGB(termenv.ANSIColor(n)).Hsl()
	return l >= 0.5
}

func (md *markdown) renderer(width int) *glamour.TermRenderer {
	if r, ok := md.byWidth[width]; ok {
		return r
	}
	cfg, ok := gstyles.DefaultStyles[md.style]
	if !ok {
		return nil // a style nobody has: the text, as it was written
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(*cfg),
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
