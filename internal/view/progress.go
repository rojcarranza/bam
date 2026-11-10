package view

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/view/style"
)

// minCells is the narrowest bar worth drawing; below it the cells say less
// than the percentage beside them.
const minCells = 6

// Bar renders a progress bar, a percentage and the time left. It returns ""
// when there is no estimate, so callers may append it unconditionally.
func Bar(o Out, p provider.Progress) string {
	cells := 20
	if o.Width > 0 {
		cells = min(24, max(10, o.Width/4))
	}
	return renderBar(o.Style, p, cells)
}

// BarWithin renders the widest bar that fits in budget columns, or "" when
// even the narrowest one would not. Panels with a header to share use this;
// full-width lines use Bar.
func BarWithin(m style.Mode, p provider.Progress, budget int) string {
	if !p.Valid || p.Average <= 0 {
		return ""
	}
	if cells := budget - barOverhead(p); cells >= minCells {
		return renderBar(m, p, min(cells, 24))
	}
	// Too narrow for cells: the percentage and the time left say the same
	// thing in fewer columns, so they are what gets dropped last.
	short := fmt.Sprintf("%3.0f%%  %s", p.Percent*100, barTail(p))
	if len(short) > budget {
		return ""
	}
	return short
}

// barOverhead is everything a bar prints besides its cells.
func barOverhead(p provider.Progress) int {
	return len("[]") + len(" 100%  ") + len(barTail(p))
}

func barTail(p provider.Progress) string {
	if p.Elapsed >= p.Average {
		return "over by " + Duration(p.Elapsed-p.Average)
	}
	return "~" + Duration(p.Remaining) + " left"
}

func renderBar(m style.Mode, p provider.Progress, cells int) string {
	if !p.Valid || p.Average <= 0 {
		return ""
	}
	filled := int(math.Round(p.Percent * float64(cells)))
	filled = min(max(filled, 0), cells)
	full, empty := "#", "-"
	if m.Color {
		full, empty = "█", "░"
	}
	return fmt.Sprintf("[%s%s] %3.0f%%  %s",
		strings.Repeat(full, filled), strings.Repeat(empty, cells-filled), p.Percent*100, barTail(p))
}

// advance moves an estimate forward by d, so a live display keeps counting
// between polls. The server's own numbers replace it on the next poll.
func advance(p provider.Progress, d time.Duration) provider.Progress {
	if !p.Valid || p.Average <= 0 || d <= 0 {
		return p
	}
	p.Elapsed += d
	p.Remaining = max(0, p.Remaining-d)
	p.Percent = min(max(p.Percent+float64(d)/float64(p.Average), 0), 1)
	return p
}
