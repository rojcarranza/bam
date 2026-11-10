// Package view renders app and provider values as terminal text or JSON.
package view

import (
	"fmt"
	"io"
	"time"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/view/style"
)

// Out is where and how to render.
type Out struct {
	W     io.Writer
	TTY   bool
	Width int
	Style style.Mode
	Now   func() time.Time
}

// Time is relative on a TTY and RFC3339 on a pipe. Zero is "–".
func (o Out) Time(t time.Time) string {
	if t.IsZero() {
		return "–"
	}
	if !o.TTY {
		return t.UTC().Format(time.RFC3339)
	}
	return Ago(o.Now().Sub(t))
}

// Key wraps a key in a hyperlink when links are on.
func (o Out) Key(key, url string) string { return style.Link(o.Style, url, key) }

// State is glyph plus label in the state's color.
func (o Out) State(s provider.State) string { return style.State(o.Style, s) }

// Ago formats an age.
func Ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// Duration formats a duration compactly: 12s, 2m03s, 1h04m. Zero is "–".
func Duration(d time.Duration) string {
	switch {
	case d <= 0:
		return "–"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}
