package tui

import (
	"os"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	gansi "charm.land/glamour/v2/ansi"
	gstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// autoStyle is the name that means "you decide". Glamour v2 dropped
// its own auto style along with everything that asked the terminal a
// question; the word is still the one people write in a Palette, so
// the terminal keeps it and answers it itself.
const autoStyle = "auto"

// markdown renders assistant text. Building a glamour renderer is the
// expensive part — it parses a style and builds a syntax highlighter —
// so there is one per width, kept until the window changes size.
//
// Glamour v2 is pure: it always writes full-fidelity colour and never
// looks at the terminal. What the terminal can take is bubbletea's
// business, on the way out.
type markdown struct {
	style   string
	byWidth map[int]*glamour.TermRenderer
}

// newMarkdown takes a style that has already been resolved: "auto"
// never reaches here.
func newMarkdown(style string) *markdown {
	return &markdown{style: style, byWidth: map[int]*glamour.TermRenderer{}}
}

// resolveStyle turns a glamour style name into a concrete one, from
// what is known about the terminal at the moment it is asked.
//
// The order is who said it loudest. A name in the Palette is the
// person's own word — `mote demo -style pink`, or `-light` — and
// nothing overrides it. GLAMOUR_STYLE is the same word said in the
// environment. Otherwise the terminal decides: nothing that can carry
// colour at all gets notty, and the rest turns on the background.
//
// dark is a guess to begin with and an answer later. Nobody blocks on
// getting it: the first frame is drawn with the safe default and the
// answer, if the terminal gives one, arrives as a message and the
// frame is drawn again. See Model.Init.
func resolveStyle(name string, noColor, dark bool) string {
	if name != "" && name != autoStyle {
		return name
	}
	if s := os.Getenv("GLAMOUR_STYLE"); s != "" && s != autoStyle {
		return s
	}
	if noColor {
		return gstyles.NoTTYStyle
	}
	if !dark {
		return gstyles.LightStyle
	}
	return gstyles.DarkStyle
}

// darkDefault is what to assume before the terminal has answered — and
// forever, on a terminal that never does. COLORFGBG is the one answer
// that costs nothing: a terminal that publishes its own background has
// already said so, in the environment, without being asked. Dark
// otherwise, because the rest of the palette is ANSI 1–6 and 8 and
// reads on either.
func darkDefault() bool { return !lightBackground(os.Getenv("COLORFGBG")) }

// noColor says the terminal cannot paint at all — output redirected to
// a file, or TERM=dumb. It is what glamour's notty style is for.
func noColor(p colorprofile.Profile) bool { return p == colorprofile.NoTTY }

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
	// The same question the terminal's own answer is put to, asked of
	// the colour it named in the environment instead.
	return !tea.BackgroundColorMsg{Color: lipgloss.ANSIColor(n)}.IsDark()
}

// styleConfig is a glamour style with the two things it does to the
// width taken back.
//
// A glamour style indents the whole document by two columns and keeps
// two more in reserve on the right, and the dark and light styles pad
// every piece of inline code with a space on each side. Four columns
// and change is not much until a sentence is four columns too long,
// and then a greeting that fits its window breaks in the middle of
// itself, around the `/help` that pushed it over. The transcript has
// its own gutters — "› " for what was said, "· " for a notice, "┃ "
// for the focused card — so glamour's are a second margin inside them,
// and the text is what is worth the columns.
func styleConfig(name string) (gansi.StyleConfig, bool) {
	cfg, ok := gstyles.DefaultStyles[name]
	if !ok {
		return gansi.StyleConfig{}, false
	}
	out := *cfg // a copy: DefaultStyles hands out the originals
	flush := uint(0)
	out.Document.Margin = &flush
	out.Code.Prefix, out.Code.Suffix = "", ""
	return out, true
}

func (md *markdown) renderer(width int) *glamour.TermRenderer {
	if r, ok := md.byWidth[width]; ok {
		return r
	}
	cfg, ok := styleConfig(md.style)
	if !ok {
		return nil // a style nobody has: the text, as it was written
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(cfg),
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
