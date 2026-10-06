package app

import (
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
)

// SetBoardRow is one run of a set as a display knows it.
type SetBoardRow struct {
	Name     string
	Build    provider.Build
	Progress provider.Progress
	At       time.Time // when Progress arrived
	Err      error
	Rerun    int // how many times its build was restarted
}

// SetBoard is what is known about a set run so far: its runs per stage,
// built from a plan or a record and moved on by events. The command line's
// set block and the terminal UI's set screen both draw from it. It never
// prints.
type SetBoard struct {
	name, id string
	stages   [][]*SetBoardRow
	final    *SetEvent
}

// NewSetBoard starts from the plan, so stages that have not started list
// their runs as pending.
func NewSetBoard(p SetPlan) *SetBoard {
	b := &SetBoard{name: p.Name}
	for _, st := range p.Stages {
		rows := make([]*SetBoardRow, 0, len(st))
		for _, r := range st {
			rows = append(rows, &SetBoardRow{Name: PlanRunName(r)})
		}
		b.stages = append(b.stages, rows)
	}
	return b
}

// PlanRunName is a planned run's name: its matrix name, or its preset.
func PlanRunName(r SetRunPlan) string {
	if r.Name != "" {
		return r.Name
	}
	return r.Target
}

// EventRun is the run an event is about: its name, or for events from
// before run names existed, its preset.
func EventRun(e SetEvent) string {
	if e.Run != "" {
		return e.Run
	}
	return e.Target
}

func (b *SetBoard) row(stage int, name string) *SetBoardRow {
	for len(b.stages) <= stage {
		b.stages = append(b.stages, nil)
	}
	for _, r := range b.stages[stage] {
		if r.Name == name {
			return r
		}
	}
	r := &SetBoardRow{Name: name}
	b.stages[stage] = append(b.stages[stage], r)
	return r
}

// Seed fills in what a record already knows, so a resumed or rerun set shows
// the runs that already have a build instead of pending ones.
func (b *SetBoard) Seed(rec SetRecord) {
	b.id = rec.ID
	if b.name == "" {
		b.name = rec.Set
	}
	for i, st := range rec.Stages {
		for _, run := range st.Runs {
			if run.BuildKey == "" && run.Error == "" {
				continue
			}
			r := b.row(i, run.RunName())
			r.Build = provider.Build{Key: run.BuildKey, State: run.State}
			r.Rerun = run.Reruns
			if run.Error != "" {
				r.Err = recordedError(run.Error)
			}
		}
	}
}

// recordedError is an error read back from a record, where only its text
// was kept.
type recordedError string

func (e recordedError) Error() string { return string(e) }

// Apply moves the board on by one event; now stamps the progress it carries.
func (b *SetBoard) Apply(e SetEvent, now time.Time) {
	b.id = e.ID
	if b.name == "" {
		b.name = e.Set
	}
	switch e.Type {
	case SetRunTriggered:
		r := b.row(e.Stage, EventRun(e))
		r.Build, r.Err = e.Build, nil
		if e.Rerun > 0 {
			r.Rerun = e.Rerun
		}
	case SetRunEvent:
		r := b.row(e.Stage, EventRun(e))
		r.Build = e.Build
		if e.Event != nil {
			r.Progress, r.At = e.Event.Progress, now
		}
	case SetRunDone:
		r := b.row(e.Stage, EventRun(e))
		if e.Build.Key != "" {
			r.Build = e.Build
		}
		r.Build.State, r.Err, r.Progress = e.State, e.Err, provider.Progress{}
	case SetDone, SetError:
		ev := e
		b.final = &ev
	}
}

func (b *SetBoard) Name() string { return b.name }
func (b *SetBoard) ID() string   { return b.id }

// Final is the set_done or set_error event, nil while the set runs.
func (b *SetBoard) Final() *SetEvent { return b.final }

// Stages is a copy of the rows, safe to read while the board moves on.
func (b *SetBoard) Stages() [][]SetBoardRow {
	out := make([][]SetBoardRow, len(b.stages))
	for i, st := range b.stages {
		out[i] = make([]SetBoardRow, len(st))
		for j, r := range st {
			out[i][j] = *r
		}
	}
	return out
}
