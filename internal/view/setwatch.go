package view

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

// SetRenderer shows set events. Tick lets a live display move its clocks.
type SetRenderer interface {
	Event(e app.SetEvent)
	Tick()
}

type setRow struct {
	target string
	build  provider.Build
	prog   provider.Progress
	at     time.Time // when prog arrived
	err    error
	rerun  int // how many times its build was restarted
}

// setBoard is what a renderer knows about the set so far.
type setBoard struct {
	name, id string
	stages   [][]*setRow
	final    *app.SetEvent
}

func newBoard(p app.SetPlan) *setBoard {
	b := &setBoard{name: p.Name}
	for _, st := range p.Stages {
		rows := make([]*setRow, 0, len(st))
		for _, r := range st {
			rows = append(rows, &setRow{target: planRunName(r)})
		}
		b.stages = append(b.stages, rows)
	}
	return b
}

func (b *setBoard) row(stage int, target string) *setRow {
	for len(b.stages) <= stage {
		b.stages = append(b.stages, nil)
	}
	for _, r := range b.stages[stage] {
		if r.target == target {
			return r
		}
	}
	r := &setRow{target: target}
	b.stages[stage] = append(b.stages[stage], r)
	return r
}

func (b *setBoard) apply(e app.SetEvent, now time.Time) {
	b.id = e.ID
	if b.name == "" {
		b.name = e.Set
	}
	switch e.Type {
	case app.SetRunTriggered:
		r := b.row(e.Stage, runOf(e))
		r.build, r.err = e.Build, nil
		if e.Rerun > 0 {
			r.rerun = e.Rerun
		}
	case app.SetRunEvent:
		r := b.row(e.Stage, runOf(e))
		r.build = e.Build
		if e.Event != nil {
			r.prog, r.at = e.Event.Progress, now
		}
	case app.SetRunDone:
		r := b.row(e.Stage, runOf(e))
		if e.Build.Key != "" {
			r.build = e.Build
		}
		r.build.State, r.err, r.prog = e.State, e.Err, provider.Progress{}
	case app.SetDone, app.SetError:
		ev := e
		b.final = &ev
	}
}

// summary is the closing lines: the result, then what to try per failed run.
func (b *setBoard) summary(o Out) {
	if b.final == nil {
		return
	}
	if b.final.Type == app.SetError {
		return // the command prints the error, with what to try
	}
	if b.final.State == provider.StateSuccess {
		fmt.Fprintln(o.W, style.Colored(o.Style, provider.StateSuccess, "set succeeded"))
		return
	}
	if b.final.Stage >= len(b.stages) {
		fmt.Fprintln(o.W, style.Colored(o.Style, provider.StateFailed, "set failed"))
		fmt.Fprintf(o.W, "  try: bam set rerun --failed %s\n", b.id)
		return
	}
	st := b.stages[b.final.Stage]
	failed := 0
	for _, r := range st {
		if r.build.State != provider.StateSuccess {
			failed++
		}
	}
	fmt.Fprintln(o.W, style.Colored(o.Style, provider.StateFailed,
		fmt.Sprintf("set failed: stage %d (%d of %d runs failed)", b.final.Stage+1, failed, len(st))))
	for _, r := range st {
		switch {
		case r.build.Key == "" && r.err != nil:
			fmt.Fprintf(o.W, "  %s: %s\n", r.target, errWhat(r.err))
		case r.build.Key != "" && r.build.State != provider.StateSuccess:
			fmt.Fprintf(o.W, "  try: bam logs %s --failed\n", r.build.Key)
		}
	}
	fmt.Fprintf(o.W, "  try: bam set rerun --failed %s\n", b.id)
}

func errWhat(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.What
	}
	return err.Error()
}

// SetLive redraws the whole set in place on a TTY.
type SetLive struct {
	o     Out
	b     *setBoard
	lines int
	done  bool
}

// NewSetLive starts from the plan, so stages that have not started show
// their presets as pending.
func NewSetLive(o Out, p app.SetPlan) *SetLive { return &SetLive{o: o, b: newBoard(p)} }

// Seed fills in what a record already knows, so a resumed or rerun set shows
// the runs that already have a build instead of pending ones.
func (l *SetLive) Seed(rec app.SetRecord) {
	l.b.id = rec.ID
	for i, st := range rec.Stages {
		for _, run := range st.Runs {
			if run.BuildKey == "" && run.Error == "" {
				continue
			}
			r := l.b.row(i, run.RunName())
			r.build = provider.Build{Key: run.BuildKey, State: run.State}
			r.rerun = run.Reruns
			if run.Error != "" {
				r.err = errors.New(run.Error)
			}
		}
	}
}

func (l *SetLive) Event(e app.SetEvent) {
	if l.done {
		return
	}
	l.b.apply(e, l.o.Now())
	l.draw()
	if l.b.final != nil {
		l.done = true
		fmt.Fprintln(l.o.W)
		l.b.summary(l.o)
	}
}

func (l *SetLive) Tick() {
	if !l.done && l.lines > 0 {
		l.draw()
	}
}

func (l *SetLive) draw() {
	var sb strings.Builder
	o := l.o
	o.W = &sb
	fmt.Fprintf(o.W, "%s  %s\n", style.Bold(o.Style, l.b.name), l.b.id)
	for i, st := range l.b.stages {
		fmt.Fprintf(o.W, "stage %d/%d\n", i+1, len(l.b.stages))
		t := Table{Indent: "  ", Headers: []string{"", "", "", "", ""}}
		for _, r := range st {
			state := r.build.State
			if r.build.Key == "" && r.err == nil {
				state = ""
			}
			dur := ""
			if d := elapsed(l.o, r.build); r.build.Key != "" && d > 0 {
				dur = Duration(d)
			}
			bar := ""
			if state == provider.StateRunning {
				bar = Bar(l.o, advance(r.prog, l.o.Now().Sub(r.at)))
			}
			t.Rows = append(t.Rows, []string{runGlyph(l.o, state) + " " + r.target, dashIfEmpty(r.build.Key), rerunTitle(state, r.rerun), dur, bar})
		}
		_ = RenderRows(o, t)
	}
	text := sb.String()
	if l.lines > 0 {
		fmt.Fprintf(l.o.W, "\x1b[%dA\x1b[J", l.lines)
	}
	fmt.Fprint(l.o.W, text)
	l.lines = strings.Count(text, "\n")
}

// SetLines prints one timestamped line per change, for pipes and CI logs.
type SetLines struct {
	o Out
	b *setBoard
}

func NewSetLines(o Out) *SetLines { return &SetLines{o: o, b: &setBoard{}} }

func (l *SetLines) Event(e app.SetEvent) {
	l.b.apply(e, e.Time)
	ts := e.Time.UTC().Format(time.RFC3339)
	head := fmt.Sprintf("%s stage %d %s", ts, e.Stage+1, runOf(e))
	switch e.Type {
	case app.SetRunTriggered:
		if e.Rerun > 0 {
			fmt.Fprintf(l.o.W, "%s %s rerun %d\n", head, e.Build.Key, e.Rerun)
			return
		}
		fmt.Fprintf(l.o.W, "%s %s triggered\n", head, e.Build.Key)
	case app.SetRunEvent:
		if e.Event != nil && e.Event.Type == app.EventState {
			fmt.Fprintf(l.o.W, "%s %s %s\n", head, e.Build.Key, e.Event.State)
		}
	case app.SetRunDone:
		if e.Build.Key == "" {
			fmt.Fprintf(l.o.W, "%s failed: %s\n", head, errWhat(e.Err))
			return
		}
		line := fmt.Sprintf("%s %s %s", head, e.Build.Key, e.State)
		if e.Build.Duration > 0 {
			line += " " + Duration(e.Build.Duration)
		}
		if e.Err != nil {
			line += ": " + errWhat(e.Err)
		}
		fmt.Fprintln(l.o.W, line)
	case app.SetDone, app.SetError:
		l.b.summary(l.o)
	}
}

func (l *SetLines) Tick() {}

// SetNDJSON writes every set event as one JSON line.
type SetNDJSON struct{ o Out }

func NewSetNDJSON(o Out) *SetNDJSON { return &SetNDJSON{o: o} }

func (n *SetNDJSON) Event(e app.SetEvent) {
	// Progress-only events are not in the NDJSON contract, as for watch.
	if e.Type == app.SetRunEvent && e.Event != nil && e.Event.Type == app.EventProgress {
		return
	}
	_ = WriteNDJSON(n.o.W, SetEventJSON(e))
}

func (n *SetNDJSON) Tick() {}

// runOf is the run an event is about: its name, or for events from before
// run names existed, its preset.
func runOf(e app.SetEvent) string {
	if e.Run != "" {
		return e.Run
	}
	return e.Target
}

func planRunName(r app.SetRunPlan) string {
	if r.Name != "" {
		return r.Name
	}
	return r.Target
}
