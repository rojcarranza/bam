package view

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
)

// GroupTitle is how bam top heads a pin group: the group's name with a capital
// first letter and - and _ shown as spaces, so integration reads
// Integration and release-train reads Release train.
func GroupTitle(name string) string {
	s := strings.NewReplacer("-", " ", "_", " ").Replace(name)
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[:1])) + string(r[1:])
}

// GroupState is the right end of a group's heading: how many pipelines it
// pins, then how many fail (the newest finished build failed) and how many
// run, leaving out a zero count: 3 pipelines · 1 failing · 1 running.
func GroupState(g app.PinGroupView) string {
	var failing, running int
	for _, p := range g.Pins {
		if len(p.Results) > 0 && p.Results[0].State == provider.StateFailed {
			failing++
		}
		if p.Current != nil && p.Current.State == provider.StateRunning {
			running++
		}
	}
	parts := []string{fmt.Sprintf("%d pipelines", len(g.Pins))}
	if len(g.Pins) == 1 {
		parts[0] = "1 pipeline"
	}
	if failing > 0 {
		parts = append(parts, fmt.Sprintf("%d failing", failing))
	}
	if running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", running))
	}
	return strings.Join(parts, " · ")
}

// RuledLine draws title, a rule and state across width columns, the state at
// the right end. Each part goes through its own render function so a caller
// can color it. When the width cannot hold the state, the state is left out;
// when it cannot hold the title either, the title is cut.
func RuledLine(title, state string, width int, renderTitle, renderRule, renderState func(string) string) string {
	tw, sw := utf8.RuneCountInString(title), utf8.RuneCountInString(state)
	if state != "" && tw+sw+4 > width {
		state, sw = "", 0
	}
	if tw > width {
		tw = max(width, 1)
		title = string([]rune(title)[:tw-1]) + "…"
	}
	out := renderTitle(title)
	if state == "" {
		if fill := width - tw - 1; fill > 0 {
			out += " " + renderRule(strings.Repeat("─", fill))
		}
		return out
	}
	fill := width - tw - sw - 2
	return out + " " + renderRule(strings.Repeat("─", fill)) + " " + renderState(state)
}

// PassRecord is the line under a pinned pipeline: how many of the results
// read passed, the current run of failures or passes with its first day, and
// the last pass. It is computed from results (newest first) alone. No results
// gives "". Results where nothing passed say so; when the newest passed, the
// last pass is the run itself and is not repeated.
func PassRecord(results []provider.Build, now time.Time) string {
	if len(results) == 0 {
		return ""
	}
	passed := 0
	for _, b := range results {
		if b.State == provider.StateSuccess {
			passed++
		}
	}
	parts := []string{fmt.Sprintf("%d of %d passed", passed, len(results))}
	newest := results[0].State
	if newest == provider.StateSuccess || newest == provider.StateFailed {
		n := 0
		for n < len(results) && results[n].State == newest {
			n++
		}
		word := "failing"
		if newest == provider.StateSuccess {
			word = "passing"
		}
		run := fmt.Sprintf("%s for %d", word, n)
		if n == 1 {
			run += " build"
		} else {
			run += " builds"
		}
		if t := results[n-1].FinishedAt; !t.IsZero() {
			run += " since " + t.Local().Format("2 Jan")
		}
		parts = append(parts, run)
	}
	if newest != provider.StateSuccess {
		last := -1
		for i, b := range results {
			if b.State == provider.StateSuccess {
				last = i
				break
			}
		}
		switch {
		case last < 0:
			parts = append(parts, fmt.Sprintf("no pass in these %d", len(results)))
		default:
			s := fmt.Sprintf("last pass #%d", results[last].Number)
			if t := results[last].FinishedAt; !t.IsZero() {
				s += " " + Ago(max(now.Sub(t), 0))
			}
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}
