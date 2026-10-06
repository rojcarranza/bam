package view

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

// PlanStatsView is bam plan stats.
func PlanStatsView(o Out, s app.PlanStats) error {
	if s.Passed+s.Failed+otherCount(s) == 0 {
		fmt.Fprintf(o.W, "no finished builds of %s in the last %d\n", s.PlanKey, s.Builds)
		return nil
	}
	head := s.PlanKey
	if s.Branch != "" {
		master := s.MasterKey
		if master == "" {
			master = s.PlanKey
		}
		head = fmt.Sprintf("%s  branch %s (%s)", master, s.Branch, s.PlanKey)
	}
	fmt.Fprintf(o.W, "%s  last %d builds (#%d–#%d, %s)\n", style.Bold(o.Style, head), s.Builds, s.First, s.Last, span(s.To.Sub(s.From)))

	ok, bad := style.StateGlyph(o.Style, provider.StateSuccess), style.StateGlyph(o.Style, provider.StateFailed)
	counts := fmt.Sprintf("%d %s  %d %s", s.Passed, ok, s.Failed, bad)
	for _, st := range sortedStates(s.Other) {
		counts += fmt.Sprintf("  %d %s", s.Other[st], strings.ToLower(style.Title(st)))
	}
	rate := "–"
	if r, has := s.SuccessRate(); has {
		rate = fmt.Sprintf("%d%%", int(math.Round(r*100)))
	}
	line := fmt.Sprintf("%-9s %s  (%s)", "Success", rate, counts)
	if s.Streak != nil {
		line += fmt.Sprintf("   streak %s %d", style.StateGlyph(o.Style, s.Streak.State), s.Streak.Count)
	}
	fmt.Fprintln(o.W, line)

	if d := s.Durations; d != nil {
		fmt.Fprintf(o.W, "%-9s median %s  p90 %s  max %s (#%d)   of %d successful\n", "Duration",
			Duration(d.Median), Duration(d.P90), Duration(d.Max), d.MaxBuild, d.Of)
	} else {
		fmt.Fprintf(o.W, "%-9s no successful builds\n", "Duration")
	}

	if len(s.FailingJobs) == 0 {
		fmt.Fprintf(o.W, "%-12s none\n", "Failing jobs")
		return nil
	}
	fmt.Fprintln(o.W, "Failing jobs")
	t := Table{Indent: "  ", Headers: []string{"", "", ""}}
	for _, j := range s.FailingJobs {
		t.Rows = append(t.Rows, []string{j.Name, fmt.Sprintf("%d of %d failures", j.Failures, s.Failed), fmt.Sprintf("last #%d", j.LastBuild)})
	}
	return RenderRows(o, t)
}

func otherCount(s app.PlanStats) int {
	n := 0
	for _, c := range s.Other {
		n += c
	}
	return n
}

func sortedStates(m map[provider.State]int) []provider.State {
	out := make([]provider.State, 0, len(m))
	for st, n := range m {
		if n > 0 {
			out = append(out, st)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// span is how long the builds covered: days from one day on, else a
// duration.
func span(d time.Duration) string {
	if d >= 24*time.Hour {
		days := int(d / (24 * time.Hour))
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	return Duration(d)
}

type PlanStatsDoc struct {
	PlanKey     string            `json:"plan_key"`
	Branch      string            `json:"branch"`
	Builds      int               `json:"builds"`
	First       int               `json:"first"`
	Last        int               `json:"last"`
	From        *string           `json:"from"`
	To          *string           `json:"to"`
	Passed      int               `json:"passed"`
	Failed      int               `json:"failed"`
	Other       map[string]int    `json:"other"`
	SuccessRate *float64          `json:"success_rate"`
	Duration    *DurationStatsDoc `json:"duration"`
	Streak      *StreakDoc        `json:"streak"`
	FailingJobs []JobFailuresDoc  `json:"failing_jobs"`
	Warnings    []string          `json:"warnings"`
}

type DurationStatsDoc struct {
	MedianMS int64 `json:"median_ms"`
	P90MS    int64 `json:"p90_ms"`
	MaxMS    int64 `json:"max_ms"`
	MaxBuild int   `json:"max_build"`
	Of       int   `json:"of"`
}

type StreakDoc struct {
	State string `json:"state"`
	Count int    `json:"count"`
}

type JobFailuresDoc struct {
	Name      string `json:"name"`
	Failures  int    `json:"failures"`
	LastBuild int    `json:"last_build"`
}

func PlanStatsJSON(s app.PlanStats) PlanStatsDoc {
	d := PlanStatsDoc{PlanKey: s.PlanKey, Branch: s.Branch, Builds: s.Builds, First: s.First, Last: s.Last,
		Passed: s.Passed, Failed: s.Failed, Other: map[string]int{},
		FailingJobs: []JobFailuresDoc{}, Warnings: nonNil(s.Warnings)}
	if !s.From.IsZero() {
		d.From = timePtr(s.From)
	}
	if !s.To.IsZero() {
		d.To = timePtr(s.To)
	}
	for st, n := range s.Other {
		d.Other[string(st)] = n
	}
	if r, ok := s.SuccessRate(); ok {
		d.SuccessRate = &r
	}
	if x := s.Durations; x != nil {
		d.Duration = &DurationStatsDoc{MedianMS: x.Median.Milliseconds(), P90MS: x.P90.Milliseconds(),
			MaxMS: x.Max.Milliseconds(), MaxBuild: x.MaxBuild, Of: x.Of}
	}
	if s.Streak != nil {
		d.Streak = &StreakDoc{State: string(s.Streak.State), Count: s.Streak.Count}
	}
	for _, j := range s.FailingJobs {
		d.FailingJobs = append(d.FailingJobs, JobFailuresDoc{Name: j.Name, Failures: j.Failures, LastBuild: j.LastBuild})
	}
	return d
}
