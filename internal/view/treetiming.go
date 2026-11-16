package view

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/view/style"
)

// timingRow is one finished build of a tree on the time axis. Queued is
// when it joined the queue, Started when an agent took it and Finished when
// it ended; Queued and Started are equal when Bamboo did not say how long
// the build waited. Timed is false when Bamboo gave no usable times.
type timingRow struct {
	Key, PlanKey string
	Depth        int
	Queued       time.Time
	Started      time.Time
	Finished     time.Time
	Timed        bool
}

// timing is the rows of a tree's timeline and the axis they share.
type timing struct {
	Rows       []timingRow
	Start, End time.Time // the axis; zero when no row has times
	Running    int       // builds left out because they have not finished
}

// treeTiming collects the timeline of the builds under root, parents first.
// Superseded runs with the builds they started, and repeated, skipped and never-run
// builds, are not part of the run and are left out; a build that has not finished
// has no end and is only counted.
func treeTiming(root *app.TreeNode) timing {
	var t timing
	add := func(n *app.TreeNode, depth int) {
		if n.NotRun || n.Loop {
			return
		}
		b := n.Build
		if b.State == provider.StateSkipped { // it never ran, as a build not run
			return
		}
		if !app.TreeFinished(b.State) {
			t.Running++
			return
		}
		r := timingRow{Key: b.Key, PlanKey: n.PlanKey, Depth: depth}
		r.Queued, r.Started, r.Finished = buildSpan(b.QueuedAt, b.StartedAt, b.FinishedAt, b.QueueDuration, b.Duration)
		r.Timed = !r.Queued.IsZero() && !r.Finished.IsZero() && !r.Finished.Before(r.Queued)
		t.Rows = append(t.Rows, r)
		if !r.Timed {
			return
		}
		if t.Start.IsZero() || r.Queued.Before(t.Start) {
			t.Start = r.Queued
		}
		if r.Finished.After(t.End) {
			t.End = r.Finished
		}
	}
	var walk func(n *app.TreeNode, depth int)
	walk = func(n *app.TreeNode, depth int) {
		if n.Superseded {
			return
		}
		add(n, depth)
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	walk(root, 0)
	return t
}

// buildSpan fills what a finished build's record leaves out: the start from
// the wait, the end from the duration. Zero times stay zero.
func buildSpan(queued, started, finished time.Time, wait, run time.Duration) (q, s, f time.Time) {
	q, s, f = queued, started, finished
	if q.IsZero() {
		q = s
	}
	if s.IsZero() && !q.IsZero() {
		s = q.Add(wait)
	}
	if f.IsZero() && !s.IsZero() && run > 0 {
		f = s.Add(run)
	}
	if s.Before(q) {
		s = q
	}
	return q, s, f
}

const (
	timingMinCells     = 20
	timingMaxCells     = 60
	timingDefaultCells = 40
)

// TreeTimingText prints the timeline of a finished tree run: one line per
// build with a bar on a shared axis, the wait for an agent drawn as ░ and
// the run as █, then the axis. failedOnly prunes the builds as TreeText does.
func TreeTimingText(o Out, t app.Tree, failedOnly bool) error {
	root := t.Root
	if failedOnly {
		root = pruneFailed(root)
	}
	tm := treeTiming(root)
	if _, err := fmt.Fprintln(o.W, "timing:"); err != nil {
		return err
	}
	if tm.Start.IsZero() {
		_, err := fmt.Fprintln(o.W, "  no build of this tree has times to draw"+tm.runningNote())
		return err
	}
	total := tm.End.Sub(tm.Start)
	labels := make([]string, len(tm.Rows))
	lw, tw := 0, 0
	for i, r := range tm.Rows {
		labels[i] = strings.Repeat("  ", r.Depth) + r.Key
		lw = max(lw, len([]rune(labels[i])))
		if r.Timed {
			tw = max(tw, len([]rune(timingTail(r))))
		}
	}
	cells, tails := timingDefaultCells, true
	if o.Width > 0 {
		// two spaces of indent, the label, a gap, the bar, a gap and a
		// tail such as "wait 12s · run 3m05s". On a terminal too narrow for
		// the tail at the shortest bar, the bar keeps the room.
		room := o.Width - 1 - 2 - lw - 2
		if cells = room - 2 - tw; cells < timingMinCells {
			cells, tails = max(10, min(timingMaxCells, room)), false
		}
		cells = min(timingMaxCells, cells)
	}
	for i, r := range tm.Rows {
		if !r.Timed {
			if _, err := fmt.Fprintf(o.W, "  %s  %s\n", pad(labels[i], lw), "no times from Bamboo"); err != nil {
				return err
			}
			continue
		}
		bar := timingBar(o, tm.Start, total, cells, r)
		line := "  " + pad(labels[i], lw) + "  " + bar
		if tails {
			line += "  " + timingTail(r)
		}
		if _, err := fmt.Fprintln(o.W, strings.TrimRight(line, " ")); err != nil {
			return err
		}
	}
	return timingAxis(o.W, lw, cells, total, tm)
}

func (t timing) runningNote() string {
	switch t.Running {
	case 0:
		return ""
	case 1:
		return "; 1 build has not finished"
	}
	return fmt.Sprintf("; %d builds have not finished", t.Running)
}

func pad(s string, w int) string {
	if n := w - len([]rune(s)); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// timingBar draws a row's bar in cells columns: spaces before the build was
// queued, ░ while it waited for an agent, █ while it ran. A build that ran
// at all keeps at least one █.
func timingBar(o Out, start time.Time, total time.Duration, cells int, r timingRow) string {
	cell := func(t time.Time) int {
		if total <= 0 {
			return 0
		}
		return min(cells, int(float64(t.Sub(start))/float64(total)*float64(cells)))
	}
	q, s, f := cell(r.Queued), cell(r.Started), cell(r.Finished)
	if f <= s { // a build shorter than a cell
		if s >= cells {
			s = cells - 1
		}
		f = s + 1
		if q > s {
			q = s
		}
	}
	return strings.Repeat(" ", q) + style.Dim(o.Style, strings.Repeat("░", s-q)) + strings.Repeat("█", f-s) + strings.Repeat(" ", cells-f)
}

// timingTail says a row's waiting and running time in words.
func timingTail(r timingRow) string {
	run := r.Finished.Sub(r.Started)
	if wait := r.Started.Sub(r.Queued); wait > 0 {
		return fmt.Sprintf("wait %s · run %s", Duration(wait), Duration(run))
	}
	return "run " + Duration(run)
}

// timingAxis prints the scale under the bars, from 0s to the whole run, then
// a note for what was left out and the legend.
func timingAxis(w io.Writer, lw, cells int, total time.Duration, tm timing) error {
	end := Duration(total)
	rule := "0s" + strings.Repeat("─", max(1, cells-len("0s")-len(end))) + end
	if _, err := fmt.Fprintf(w, "  %s  %s\n", strings.Repeat(" ", lw), rule); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "  ░ waited for an agent  █ ran"); err != nil {
		return err
	}
	if tm.Running > 0 {
		_, err := fmt.Fprintf(w, "  %d not drawn: still running\n", tm.Running)
		return err
	}
	return nil
}

// TimingDoc is the timeline in bam build tree --timing --json. Offsets count
// milliseconds from start, the earliest time in the tree.
type TimingDoc struct {
	Start      *time.Time       `json:"start"` // null when no build has times
	DurationMS int64            `json:"duration_ms"`
	Unfinished int              `json:"unfinished"` // builds left out because they had not finished
	Builds     []TimingBuildDoc `json:"builds"`
}

// TimingBuildDoc is one build on the axis. The offsets and durations are
// null when Bamboo gave no usable times.
type TimingBuildDoc struct {
	Key             string `json:"key"`
	PlanKey         string `json:"plan_key"`
	Depth           int    `json:"depth"`
	QueuedMS        *int64 `json:"queued_offset_ms"`
	WaitMS          *int64 `json:"wait_ms"`
	StartedMS       *int64 `json:"started_offset_ms"`
	RunMS           *int64 `json:"run_ms"`
	FinishedMS      *int64 `json:"finished_offset_ms"`
	TimesFromBamboo bool   `json:"timed"`
}

// TreeTimingJSON is TreeJSON with the timeline in a timing field.
func TreeTimingJSON(t app.Tree, failedOnly bool) TreeDoc {
	d := TreeJSON(t, failedOnly)
	root := t.Root
	if failedOnly {
		root = pruneFailed(root)
	}
	tm := treeTiming(root)
	td := TimingDoc{Unfinished: tm.Running, Builds: []TimingBuildDoc{}}
	if !tm.Start.IsZero() {
		start := tm.Start.UTC()
		td.Start = &start
		td.DurationMS = tm.End.Sub(tm.Start).Milliseconds()
	}
	for _, r := range tm.Rows {
		b := TimingBuildDoc{Key: r.Key, PlanKey: r.PlanKey, Depth: r.Depth, TimesFromBamboo: r.Timed}
		if r.Timed {
			ms := func(x time.Time) *int64 { v := x.Sub(tm.Start).Milliseconds(); return &v }
			dur := func(a, z time.Time) *int64 { v := z.Sub(a).Milliseconds(); return &v }
			b.QueuedMS, b.StartedMS, b.FinishedMS = ms(r.Queued), ms(r.Started), ms(r.Finished)
			b.WaitMS, b.RunMS = dur(r.Queued, r.Started), dur(r.Started, r.Finished)
		}
		td.Builds = append(td.Builds, b)
	}
	d.Timing = &td
	return d
}
