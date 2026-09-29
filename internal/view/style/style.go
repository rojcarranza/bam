// Package style is the single definition of how a state looks. The text
// renderers use it now; the terminal UI maps the same tokens later.
package style

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// Mode says whether color and hyperlinks may be emitted.
type Mode struct {
	Color bool
	Links bool
}

const reset = "\x1b[0m"

var glyphs = map[provider.State]string{
	provider.StateSuccess:  "✓",
	provider.StateFailed:   "✗",
	provider.StateRunning:  "▸",
	provider.StateQueued:   "◷",
	provider.StateStopped:  "⊘",
	provider.StateNotBuilt: "–",
	provider.StateSkipped:  "⤼",
	provider.StateUnknown:  "?",
}

// Standard ANSI codes: the terminal theme decides the actual shade.
var colors = map[provider.State]string{
	provider.StateSuccess:  "32", // green
	provider.StateFailed:   "31", // red
	provider.StateRunning:  "36", // cyan
	provider.StateQueued:   "33", // yellow
	provider.StateStopped:  "90", // grey
	provider.StateNotBuilt: "2",  // dim
	provider.StateSkipped:  "2",  // dim
	provider.StateUnknown:  "35", // magenta
}

var escRe = regexp.MustCompile("\x1b\\[[0-9;]*m|\x1b\\]8;;[^\x1b]*\x1b\\\\")

// Glyph returns the symbol of a state. Unknown states get "?".
func Glyph(s provider.State) string {
	if g, ok := glyphs[s]; ok {
		return g
	}
	return glyphs[provider.StateUnknown]
}

// Label returns the state as words.
func Label(s provider.State) string { return strings.ReplaceAll(string(s), "_", " ") }

// Paint wraps text in an ANSI color when color is on.
func Paint(m Mode, code, text string) string {
	if !m.Color || code == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + reset
}

// State returns glyph and label, colored when color is on.
func State(m Mode, s provider.State) string { return Paint(m, colors[s], Glyph(s)+" "+Label(s)) }

// StateGlyph returns the glyph alone, colored when color is on.
func StateGlyph(m Mode, s provider.State) string { return Paint(m, colors[s], Glyph(s)) }

func Bold(m Mode, t string) string { return Paint(m, "1", t) }
func Dim(m Mode, t string) string  { return Paint(m, "2", t) }

// Link returns an OSC 8 hyperlink when links are on.
func Link(m Mode, url, text string) string {
	if !m.Links || url == "" {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// Strip removes color and hyperlink escapes.
func Strip(s string) string { return escRe.ReplaceAllString(s, "") }

// Width is the number of visible characters.
func Width(s string) int { return utf8.RuneCountInString(Strip(s)) }

// Colored paints arbitrary text in a state's color.
func Colored(m Mode, s provider.State, text string) string { return Paint(m, colors[s], text) }

// Title is the label with a capital first letter: "Not built".
func Title(s provider.State) string {
	l := Label(s)
	if l == "" {
		return l
	}
	return strings.ToUpper(l[:1]) + l[1:]
}

// ColorEnabled applies --color, then NO_COLOR, then TTY detection.
func ColorEnabled(flag, noColor string, tty bool) (bool, error) {
	switch flag {
	case "always":
		return true, nil
	case "never":
		return false, nil
	case "", "auto":
	default:
		return false, errs.Usagef("--color must be auto, always or never, not %q", flag)
	}
	if noColor != "" {
		return false, nil
	}
	return tty, nil
}

// Pending is the dim glyph of a run that has not triggered yet: it has no
// state of its own.
func Pending(m Mode) string { return Dim(m, "·") }
