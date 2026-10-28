package view

import (
	"fmt"
	"strings"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

// RunPlan prints the plan and the variables a run will use. Unchanged plan
// values are counted, not listed.
func RunPlan(o Out, ref app.PlanRef, vs app.VarSet) {
	plan := ref.MasterKey
	if ref.Branch != "" {
		plan += fmt.Sprintf("  (branch %s, %s)", ref.Branch, ref.PlanKey)
	}
	fmt.Fprintf(o.W, "%-8s %s\n", "Plan", plan)
	if ref.Revision != "" {
		fmt.Fprintf(o.W, "%-8s %s\n", "Revision", ref.RevisionLabel())
	}
	var parts []string
	defaults := 0
	for _, v := range vs.Vars {
		if v.Source == "plan" {
			defaults++
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s (%s)", v.Name, v.Display(), v.Source))
	}
	if defaults == 1 {
		parts = append(parts, "+1 plan default")
	} else if defaults > 1 {
		parts = append(parts, fmt.Sprintf("+%d plan defaults", defaults))
	}
	line := strings.Join(parts, "  ")
	if line == "" {
		line = "none"
	}
	fmt.Fprintf(o.W, "%-8s %s\n", "Vars", line)
}

// Queued prints the line after a trigger succeeds. Without a watch it says
// how to follow the build; with a revision, that the revision is checked
// only when the build starts.
func Queued(o Out, b provider.Build, watching bool, revision string) {
	fmt.Fprintf(o.W, "%s   %s   %s\n", style.Colored(o.Style, provider.StateSuccess, "✓ Queued"), o.Key(b.Key, b.URL), b.URL)
	switch {
	case watching:
	case revision != "":
		fmt.Fprintf(o.W, "  revision %s is checked when the build starts: bam watch %s\n", revision, b.Key)
	default:
		fmt.Fprintf(o.W, "  watch: bam watch %s\n", b.Key)
	}
}

// RevisionNotBuilt follows the result of a watched build that Bamboo could
// not build at the chosen revision.
func RevisionNotBuilt(o Out, revision string) {
	fmt.Fprintf(o.W, "Bamboo could not build revision %s\n", revision)
}

// Result prints the final line of a watched build and, unless it passed, what to do next.
func Result(o Out, b provider.Build) {
	head := style.Colored(o.Style, b.State, style.Glyph(b.State)+" "+style.Title(b.State))
	line := fmt.Sprintf("%s   %s  %s", head, o.Key(b.Key, b.URL), Duration(b.Duration))
	failed := b.FailedJobs()
	if len(failed) > 0 {
		for _, s := range b.Stages {
			for _, j := range s.Jobs {
				if j.Key == failed[0].Key {
					line += fmt.Sprintf("   %s › %s", s.Name, j.Name)
				}
			}
		}
	}
	fmt.Fprintln(o.W, line)
	if b.State == provider.StateSuccess {
		return
	}
	indent := "  next: "
	if len(failed) > 0 {
		fmt.Fprintf(o.W, "%sbam logs %s --failed\n", indent, b.Key)
		indent = "        "
	}
	fmt.Fprintf(o.W, "%sbam open %s\n", indent, b.Key)
}

// WatchRenderer shows watch events. Tick lets a live display update its clock.
type WatchRenderer interface {
	Event(e app.Event)
	Tick()
}

// Live redraws a block in place on a TTY.
type Live struct {
	o        Out
	last     provider.Build
	progress provider.Progress
	at       time.Time // when that estimate arrived, so Tick can extrapolate
	have     bool
	lines    int
}

func NewLive(o Out) *Live { return &Live{o: o} }

func (l *Live) Event(e app.Event) {
	if e.Type == app.EventError {
		return
	}
	l.last, l.have = e.Build, true
	l.progress, l.at = e.Progress, l.o.Now()
	l.draw()
	if e.Type == app.EventDone {
		fmt.Fprintln(l.o.W)
		Result(l.o, e.Build)
	}
}

func (l *Live) Tick() {
	if l.have && !l.last.State.Finished() {
		l.draw()
	}
}

func (l *Live) draw() {
	var b strings.Builder
	o := l.o
	o.W = &b
	block(o, l.last, advance(l.progress, l.o.Now().Sub(l.at)))
	text := b.String()
	if l.lines > 0 {
		fmt.Fprintf(l.o.W, "\x1b[%dA\x1b[J", l.lines)
	}
	fmt.Fprint(l.o.W, text)
	l.lines = strings.Count(text, "\n")
}

func elapsed(o Out, b provider.Build) time.Duration {
	switch {
	case b.State.Finished():
		return b.Duration
	case !b.StartedAt.IsZero():
		return o.Now().Sub(b.StartedAt).Truncate(time.Second)
	case !b.QueuedAt.IsZero():
		return o.Now().Sub(b.QueuedAt).Truncate(time.Second)
	}
	return 0
}

func block(o Out, b provider.Build, p provider.Progress) {
	head := style.Colored(o.Style, b.State, style.Glyph(b.State)+" "+style.Title(b.State))
	parts := []string{head}
	if b.Agent != "" {
		parts = append(parts, "agent "+b.Agent)
	}
	if d := elapsed(o, b); d > 0 {
		parts = append(parts, Duration(d))
	}
	if bar := Bar(o, p); bar != "" {
		parts = append(parts, bar)
	}
	fmt.Fprintln(o.W, strings.Join(parts, "  "))
	if len(b.Stages) == 0 {
		return
	}
	fmt.Fprintln(o.W)
	t := Table{Indent: "  ", Headers: []string{"", ""}}
	for _, s := range b.Stages {
		t.Rows = append(t.Rows, []string{style.StateGlyph(o.Style, s.State) + " " + s.Name, durationIfRun(s.State, s.Duration)})
		if len(s.Jobs) > 1 {
			for _, j := range s.Jobs {
				t.Rows = append(t.Rows, []string{"    " + style.StateGlyph(o.Style, j.State) + " " + j.Name, durationIfRun(j.State, j.Duration)})
			}
		}
	}
	_ = RenderRows(o, t)
}

// Lines prints one timestamped line per change, for pipes and CI logs.
type Lines struct{ o Out }

func NewLines(o Out) *Lines { return &Lines{o: o} }

func (l *Lines) Event(e app.Event) {
	ts := e.Time.UTC().Format(time.RFC3339)
	key := e.Build.Key
	switch e.Type {
	case app.EventState:
		extra := ""
		if e.Build.Agent != "" {
			extra = " agent=" + e.Build.Agent
		}
		fmt.Fprintf(l.o.W, "%s %s %s%s\n", ts, key, e.State, extra)
	case app.EventStage:
		fmt.Fprintf(l.o.W, "%s %s stage %s %s\n", ts, key, e.Name, e.State)
	case app.EventJob:
		fmt.Fprintf(l.o.W, "%s %s job %s %s\n", ts, key, e.Name, e.State)
	case app.EventDone:
		Result(l.o, e.Build)
	}
}

func (l *Lines) Tick() {}

// NDJSON writes every event as one JSON line.
type NDJSON struct{ o Out }

func NewNDJSON(o Out) *NDJSON { return &NDJSON{o: o} }

func (n *NDJSON) Event(e app.Event) {
	// Progress events are not in the NDJSON contract (spec §7); the estimate
	// still rides the state, stage and job lines.
	if e.Type == app.EventError || e.Type == app.EventProgress {
		return
	}
	_ = WriteNDJSON(n.o.W, EventJSON(e))
}

func (n *NDJSON) Tick() {}
