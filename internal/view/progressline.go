package view

import (
	"fmt"
	"io"
)

// ProgressLine is one line on a terminal, rewritten in place: what a slow
// read is doing. A nil line shows nothing.
type ProgressLine struct {
	w     io.Writer
	width int
	shown string
}

func NewProgressLine(w io.Writer, width int) *ProgressLine {
	return &ProgressLine{w: w, width: width}
}

// Show replaces the line with text, cut one column short of the width so
// the terminal never wraps it. An unchanged text is not written again.
func (l *ProgressLine) Show(text string) {
	if l == nil {
		return
	}
	if l.width > 0 {
		text = cut(text, l.width-1)
	}
	if text == l.shown {
		return
	}
	fmt.Fprint(l.w, "\r\x1b[K"+text)
	l.shown = text
}

// Clear wipes the line, if one shows, before anything else is printed.
func (l *ProgressLine) Clear() {
	if l == nil || l.shown == "" {
		return
	}
	fmt.Fprint(l.w, "\r\x1b[K")
	l.shown = ""
}
