package view

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

// Sets is bam set list.
func Sets(o Out, ss []app.SetInfo) error {
	t := Table{Headers: []string{"SET", "STAGES", "RUNS", "DEFINED IN"}}
	for _, s := range ss {
		runs := 0
		for _, st := range s.Stages {
			for _, r := range st {
				runs += max(len(r.Runs), 1)
			}
		}
		t.Rows = append(t.Rows, []string{s.Name, strconv.Itoa(len(s.Stages)), strconv.Itoa(runs), s.DefinedIn[len(s.DefinedIn)-1]})
	}
	return t.Render(o)
}

// SetDetail is bam set show.
func SetDetail(o Out, s app.SetInfo) error {
	fmt.Fprintln(o.W, style.Bold(o.Style, s.Name))
	if s.Timeout > 0 {
		field(o, "Timeout", s.Timeout.String())
	}
	field(o, "Defined in", strings.Join(s.DefinedIn, ", "))
	fmt.Fprintln(o.W)
	for i, st := range s.Stages {
		names := make([]string, len(st))
		for j, r := range st {
			names[j] = r.Target
		}
		fmt.Fprintf(o.W, "stage %d  %s\n", i+1, strings.Join(names, ", "))
		for _, r := range st {
			if r.MatrixLine == "" {
				continue
			}
			fmt.Fprintf(o.W, "  %s  matrix %s\n", r.Target, r.MatrixLine)
			for _, n := range r.Runs {
				fmt.Fprintf(o.W, "    %s\n", n)
			}
		}
		t := Table{Indent: "    ", Headers: []string{"", "", ""}}
		for _, r := range st {
			for _, v := range r.Vars {
				t.Rows = append(t.Rows, []string{r.Target, v.Name, v.Display()})
			}
		}
		if len(t.Rows) > 0 {
			if err := RenderRows(o, t); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetDryRun is bam set run --dry-run: every run as it would trigger.
func SetDryRun(o Out, p app.SetPlan) error {
	fmt.Fprintln(o.W, style.Bold(o.Style, p.Name))
	for i, st := range p.Stages {
		fmt.Fprintf(o.W, "stage %d/%d\n", i+1, len(p.Stages))
		for _, r := range st {
			fmt.Fprintf(o.W, "  %s  %s  branch %s\n", app.PlanRunName(r), r.Ref.PlanKey, branchLabel(r.Ref.Branch))
			for _, n := range sortedChanged(r.Vars) {
				v, _ := r.Vars.Get(n)
				fmt.Fprintf(o.W, "      %s = %s\n", n, v.Display())
			}
		}
	}
	fmt.Fprintln(o.W, "dry run: nothing triggered")
	return nil
}

func sortedChanged(vs app.VarSet) []string {
	changed := vs.Changed()
	names := make([]string, 0, len(changed))
	for n := range changed {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// SetStatusView is bam set status: the record with each build's live state.
func SetStatusView(o Out, rec app.SetRecord, builds map[string]provider.Build) error {
	fmt.Fprintf(o.W, "%s  %s  %s\n", style.Bold(o.Style, rec.Set), rec.ID, rec.Status)
	if rec.File != "" {
		fmt.Fprintf(o.W, "file %s\n", rec.File)
	}
	for i, st := range rec.Stages {
		fmt.Fprintf(o.W, "stage %d/%d\n", i+1, len(rec.Stages))
		t := Table{Indent: "  ", Headers: []string{"", "", "", ""}}
		for _, r := range st.Runs {
			state := r.State
			if b, ok := builds[r.BuildKey]; ok {
				state = b.State
			}
			t.Rows = append(t.Rows, []string{runGlyph(o, state) + " " + r.RunName(), dashIfEmpty(r.BuildKey), rerunTitle(state, r.Reruns), r.Error})
		}
		if err := RenderRows(o, t); err != nil {
			return err
		}
	}
	if rec.Status == app.SetInterrupted || rec.Status == app.SetTimedOut {
		fmt.Fprintf(o.W, "try: bam set resume %s\n", rec.ID)
	}
	if rec.Status == app.SetFailed {
		fmt.Fprintf(o.W, "try: bam set rerun --failed %s\n", rec.ID)
	}
	return nil
}

// runGlyph is a run's glyph; a run with no state yet is pending.
func runGlyph(o Out, s provider.State) string {
	if s == "" {
		return style.Pending(o.Style)
	}
	return style.StateGlyph(o.Style, s)
}

func stateTitle(s provider.State) string {
	if s == "" {
		return "pending"
	}
	return style.Title(s)
}

// rerunTitle is a run's state title, marked when bam restarted its build.
func rerunTitle(s provider.State, reruns int) string {
	if reruns > 0 {
		return fmt.Sprintf("%s (rerun %d)", stateTitle(s), reruns)
	}
	return stateTitle(s)
}

type SetDoc struct {
	Name      string        `json:"name"`
	TimeoutMS int64         `json:"timeout_ms,omitempty"`
	DefinedIn []string      `json:"defined_in"`
	Stages    [][]SetRunDoc `json:"stages"`
}

type SetRunDoc struct {
	Target string            `json:"target"`
	Vars   map[string]string `json:"vars"`
	Matrix any               `json:"matrix,omitempty"`
	Runs   []string          `json:"runs"`
}

type SetPlanDoc struct {
	Name     string            `json:"name"`
	Origin   string            `json:"origin"`
	Stages   [][]SetRunPlanDoc `json:"stages"`
	Warnings []string          `json:"warnings"`
}

type SetRunPlanDoc struct {
	Target    string            `json:"target"`
	Name      string            `json:"name"`
	PlanKey   string            `json:"plan_key"`
	Branch    string            `json:"branch"`
	Variables map[string]string `json:"variables"`
}

type SetRecordDoc struct {
	ID         string        `json:"id"`
	Set        string        `json:"set"`
	File       string        `json:"file,omitempty"`
	Origin     string        `json:"origin"`
	Repo       string        `json:"repo"`
	Status     string        `json:"status"`
	StartedAt  string        `json:"started_at"`
	FinishedAt *string       `json:"finished_at"`
	VarNames   []string      `json:"var_names"`
	Stages     []SetStageDoc `json:"stages"`
}

type SetStageDoc struct {
	Runs []SetRunRecordDoc `json:"runs"`
}

type SetRunRecordDoc struct {
	Target   string    `json:"target"`
	Name     string    `json:"name"`
	PlanKey  string    `json:"plan_key"`
	BuildKey string    `json:"build_key"`
	State    string    `json:"state"`
	Reruns   int       `json:"reruns"`
	Error    string    `json:"error,omitempty"`
	Build    *BuildDoc `json:"build,omitempty"`
}

type ErrorDoc struct {
	Kind string `json:"kind"`
	What string `json:"what"`
	Why  string `json:"why,omitempty"`
	Try  string `json:"try,omitempty"`
}

type SetEventDoc struct {
	Type   string    `json:"type"`
	Time   string    `json:"time"`
	Set    string    `json:"set"`
	ID     string    `json:"id"`
	Stage  int       `json:"stage"`
	Target string    `json:"target,omitempty"`
	Run    string    `json:"run,omitempty"`
	Build  *BuildDoc `json:"build,omitempty"`
	Event  *EventDoc `json:"event,omitempty"`
	State  string    `json:"state,omitempty"`
	Rerun  int       `json:"rerun"`
	Error  *ErrorDoc `json:"error,omitempty"`
}

// varsDoc shows configured variables as bam target show --json does: an
// ${ENV} reference as written, a secret literal masked.
func varsDoc(vs []app.TargetVar) map[string]string {
	out := map[string]string{}
	for _, v := range vs {
		switch {
		case v.EnvRef != "":
			out[v.Name] = v.Value
		case v.Secret:
			out[v.Name] = app.MaskedDisplay
		default:
			out[v.Name] = v.Value
		}
	}
	return out
}

func SetJSON(s app.SetInfo) SetDoc {
	d := SetDoc{Name: s.Name, TimeoutMS: ms(s.Timeout), DefinedIn: nonNil(s.DefinedIn), Stages: [][]SetRunDoc{}}
	for _, st := range s.Stages {
		runs := []SetRunDoc{}
		for _, r := range st {
			rd := SetRunDoc{Target: r.Target, Vars: varsDoc(r.Vars), Matrix: r.Matrix, Runs: r.Runs}
			if len(rd.Runs) == 0 {
				rd.Runs = []string{r.Target}
			}
			runs = append(runs, rd)
		}
		d.Stages = append(d.Stages, runs)
	}
	return d
}

func SetPlanJSON(p app.SetPlan) SetPlanDoc {
	d := SetPlanDoc{Name: p.Name, Origin: p.Origin, Stages: [][]SetRunPlanDoc{}, Warnings: nonNil(p.Warnings)}
	for _, st := range p.Stages {
		runs := []SetRunPlanDoc{}
		for _, r := range st {
			run := RunJSON(provider.Build{}, r.Ref, r.Vars)
			runs = append(runs, SetRunPlanDoc{Target: r.Target, Name: app.PlanRunName(r), PlanKey: run.PlanKey, Branch: run.Branch, Variables: run.Variables})
		}
		d.Stages = append(d.Stages, runs)
	}
	return d
}

// SetRecordJSON is bam set status --json: each run's state is the live one
// where the build could be fetched.
func SetRecordJSON(rec app.SetRecord, builds map[string]provider.Build) SetRecordDoc {
	d := SetRecordDoc{ID: rec.ID, Set: rec.Set, File: rec.File, Origin: rec.Origin, Repo: rec.Repo, Status: string(rec.Status),
		StartedAt: rec.StartedAt.UTC().Format(time.RFC3339), VarNames: nonNil(rec.VarNames), Stages: []SetStageDoc{}}
	if rec.FinishedAt != nil {
		d.FinishedAt = timePtr(*rec.FinishedAt)
	}
	for _, st := range rec.Stages {
		sd := SetStageDoc{Runs: []SetRunRecordDoc{}}
		for _, r := range st.Runs {
			rd := SetRunRecordDoc{Target: r.Target, Name: r.RunName(), PlanKey: r.PlanKey, BuildKey: r.BuildKey, State: string(r.State), Reruns: r.Reruns, Error: r.Error}
			if b, ok := builds[r.BuildKey]; ok {
				bd := BuildJSON(b)
				rd.Build, rd.State = &bd, string(b.State)
			}
			sd.Runs = append(sd.Runs, rd)
		}
		d.Stages = append(d.Stages, sd)
	}
	return d
}

func errorDoc(err error) *ErrorDoc {
	if err == nil {
		return nil
	}
	var e *errs.Error
	if errors.As(err, &e) {
		return &ErrorDoc{Kind: e.Kind.String(), What: e.What, Why: e.Why, Try: e.Try}
	}
	return &ErrorDoc{Kind: errs.KindOf(err).String(), What: err.Error()}
}

func SetEventJSON(e app.SetEvent) SetEventDoc {
	d := SetEventDoc{Type: string(e.Type), Time: e.Time.UTC().Format(time.RFC3339), Set: e.Set, ID: e.ID,
		Stage: e.Stage, Target: e.Target, Run: e.Run, State: string(e.State), Rerun: e.Rerun, Error: errorDoc(e.Err)}
	if e.Build.Key != "" {
		b := BuildJSON(e.Build)
		d.Build = &b
	}
	if e.Event != nil {
		ev := EventJSON(*e.Event)
		d.Event = &ev
	}
	return d
}
