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

// summary is the closing lines: the result, then what to try per failed run.
func summary(o Out, b *app.SetBoard) {
	final := b.Final()
	if final == nil {
		return
	}
	if final.Type == app.SetError {
		return // the command prints the error, with what to try
	}
	if final.State == provider.StateSuccess {
		fmt.Fprintln(o.W, style.Colored(o.Style, provider.StateSuccess, "set succeeded"))
		return
	}
	if final.Stage >= len(b.Stages()) {
		fmt.Fprintln(o.W, style.Colored(o.Style, provider.StateFailed, "set failed"))
		fmt.Fprintf(o.W, "  try: bam set rerun --failed %s\n", b.ID())
		return
	}
	st := b.Stages()[final.Stage]
	failed := 0
	for _, r := range st {
		if r.Build.State != provider.StateSuccess {
			failed++
		}
	}
	fmt.Fprintln(o.W, style.Colored(o.Style, provider.StateFailed,
		fmt.Sprintf("set failed: stage %d (%d of %d runs failed)", final.Stage+1, failed, len(st))))
	for _, r := range st {
		switch {
		case r.Build.Key == "" && r.Err != nil:
			fmt.Fprintf(o.W, "  %s: %s\n", r.Name, errWhat(r.Err))
		case r.Build.Key != "" && r.Build.State != provider.StateSuccess:
			fmt.Fprintf(o.W, "  try: bam logs %s --failed\n", r.Build.Key)
		}
	}
	fmt.Fprintf(o.W, "  try: bam set rerun --failed %s\n", b.ID())
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
	b     *app.SetBoard
	lines int
	done  bool
}

// NewSetLive starts from the plan, so stages that have not started show
// their presets as pending.
func NewSetLive(o Out, p app.SetPlan) *SetLive { return &SetLive{o: o, b: app.NewSetBoard(p)} }

// Seed fills in what a record already knows, so a resumed or rerun set shows
// the runs that already have a build instead of pending ones.
func (l *SetLive) Seed(rec app.SetRecord) { l.b.Seed(rec) }

func (l *SetLive) Event(e app.SetEvent) {
	if l.done {
		return
	}
	l.b.Apply(e, l.o.Now())
	l.draw()
	if l.b.Final() != nil {
		l.done = true
		fmt.Fprintln(l.o.W)
		summary(l.o, l.b)
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
	fmt.Fprintf(o.W, "%s  %s\n", style.Bold(o.Style, l.b.Name()), l.b.ID())
	stages := l.b.Stages()
	for i, st := range stages {
		fmt.Fprintf(o.W, "stage %d/%d\n", i+1, len(stages))
		t := Table{Indent: "  ", Headers: []string{"", "", "", "", ""}}
		for _, r := range st {
			state := r.Build.State
			if r.Build.Key == "" && r.Err == nil {
				state = ""
			}
			dur := ""
			if d := elapsed(l.o, r.Build); r.Build.Key != "" && d > 0 {
				dur = Duration(d)
			}
			bar := ""
			if state == provider.StateRunning {
				bar = Bar(l.o, advance(r.Progress, l.o.Now().Sub(r.At)))
			}
			t.Rows = append(t.Rows, []string{runGlyph(l.o, state) + " " + r.Name, dashIfEmpty(r.Build.Key), rerunTitle(state, r.Rerun), dur, bar})
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
	b *app.SetBoard
}

func NewSetLines(o Out) *SetLines { return &SetLines{o: o, b: app.NewSetBoard(app.SetPlan{})} }

func (l *SetLines) Event(e app.SetEvent) {
	l.b.Apply(e, e.Time)
	ts := e.Time.UTC().Format(time.RFC3339)
	head := fmt.Sprintf("%s stage %d %s", ts, e.Stage+1, app.EventRun(e))
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
		summary(l.o, l.b)
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
