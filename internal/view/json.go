package view

import (
	"encoding/json"
	"io"
	"sort"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
)

type RevisionDoc struct {
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	Short      string `json:"short"`
}

type JobDoc struct {
	Key        string `json:"key"`
	URL        string `json:"url"`
	Name       string `json:"name"`
	State      string `json:"state"`
	DurationMS int64  `json:"duration_ms"`
}

type StageDoc struct {
	Name       string   `json:"name"`
	State      string   `json:"state"`
	DurationMS int64    `json:"duration_ms"`
	Jobs       []JobDoc `json:"jobs"`
}

type BuildDoc struct {
	Key             string        `json:"key"`
	URL             string        `json:"url"`
	PlanKey         string        `json:"plan_key"`
	PlanName        string        `json:"plan_name"`
	Branch          string        `json:"branch"`
	Number          int           `json:"number"`
	State           string        `json:"state"`
	Reason          string        `json:"reason"`
	CustomBuild     bool          `json:"custom_build"`
	Labels          []string      `json:"labels"`
	QueuedAt        *string       `json:"queued_at"`
	StartedAt       *string       `json:"started_at"`
	FinishedAt      *string       `json:"finished_at"`
	QueueDurationMS int64         `json:"queue_duration_ms"`
	DurationMS      int64         `json:"duration_ms"`
	Agent           string        `json:"agent"`
	Revisions       []RevisionDoc `json:"revisions"`
	Stages          []StageDoc    `json:"stages"`
	FailedTests     []string      `json:"failed_tests"`
	Progress        *ProgressDoc  `json:"progress,omitempty"`
}

// ProgressDoc is the server's estimate for a running build. It is absent from
// a document when the server had no estimate.
type ProgressDoc struct {
	Percent     float64 `json:"percent"`
	AverageMS   int64   `json:"average_ms"`
	ElapsedMS   int64   `json:"elapsed_ms"`
	RemainingMS int64   `json:"remaining_ms"`
	Stage       string  `json:"stage,omitempty"`
	Source      string  `json:"source,omitempty"`
}

type LastBuildDoc struct {
	Key        string  `json:"key"`
	Number     int     `json:"number"`
	State      string  `json:"state"`
	FinishedAt *string `json:"finished_at"`
	Reason     string  `json:"reason"`
}

type PlanDoc struct {
	Key        string        `json:"key"`
	URL        string        `json:"url"`
	Name       string        `json:"name"`
	ProjectKey string        `json:"project_key"`
	LastBuild  *LastBuildDoc `json:"last_build"`
}

type ProjectDoc struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type BranchDoc struct {
	Key       string `json:"key"`
	URL       string `json:"url"`
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
	PlanKey   string `json:"plan_key"`
	Enabled   bool   `json:"enabled"`
}

type VariableDoc struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Masked   bool   `json:"masked"`
	Source   string `json:"source"`
	LastUsed string `json:"last_used"`
}

type TargetDoc struct {
	Name      string              `json:"name"`
	PlanKey   string              `json:"plan_key"`
	Server    string              `json:"server"`
	Branch    string              `json:"branch"`
	Defaults  map[string]string   `json:"defaults"`
	Options   map[string][]string `json:"options"`
	Required  []string            `json:"required"`
	Watch     bool                `json:"watch"`
	TimeoutMS int64               `json:"timeout_ms"`
	DefinedIn []string            `json:"defined_in"`
}

type RunDoc struct {
	Key          string            `json:"key"`
	URL          string            `json:"url"`
	PlanKey      string            `json:"plan_key"`
	Branch       string            `json:"branch"`
	Revision     string            `json:"revision"`
	RevisionFrom string            `json:"revision_from"`
	Variables    map[string]string `json:"variables"`
}

type EventDoc struct {
	Type     string       `json:"type"`
	Time     string       `json:"time"`
	BuildKey string       `json:"build_key,omitempty"`
	Name     string       `json:"name,omitempty"`
	Key      string       `json:"key,omitempty"`
	State    string       `json:"state,omitempty"`
	Message  string       `json:"message,omitempty"`
	Build    *BuildDoc    `json:"build,omitempty"`
	Progress *ProgressDoc `json:"progress,omitempty"`
}

func timePtr(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func ms(d time.Duration) int64 { return d.Milliseconds() }

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func BuildJSON(b provider.Build) BuildDoc {
	return BuildJSONWithProgress(b, provider.Progress{})
}

// BuildJSONWithProgress is BuildJSON with the server's estimate attached.
func BuildJSONWithProgress(b provider.Build, pr provider.Progress) BuildDoc {
	d := BuildDoc{Key: b.Key, URL: b.URL, PlanKey: b.PlanKey, PlanName: b.PlanName, Branch: b.Branch, Number: b.Number, State: string(b.State),
		Reason: b.Reason, CustomBuild: b.CustomBuild, Labels: nonNil(b.Labels), QueuedAt: timePtr(b.QueuedAt),
		StartedAt: timePtr(b.StartedAt), FinishedAt: timePtr(b.FinishedAt), QueueDurationMS: ms(b.QueueDuration),
		DurationMS: ms(b.Duration), Agent: b.Agent, Revisions: []RevisionDoc{}, Stages: []StageDoc{}, FailedTests: nonNil(b.FailedTests),
		Progress: progressDoc(pr)}
	for _, r := range b.Revisions {
		d.Revisions = append(d.Revisions, RevisionDoc{Repository: r.Repository, Revision: r.Revision, Short: r.Short()})
	}
	for _, s := range b.Stages {
		sd := StageDoc{Name: s.Name, State: string(s.State), DurationMS: ms(s.Duration), Jobs: []JobDoc{}}
		for _, j := range s.Jobs {
			sd.Jobs = append(sd.Jobs, JobDoc{Key: j.Key, URL: j.URL, Name: j.Name, State: string(j.State), DurationMS: ms(j.Duration)})
		}
		d.Stages = append(d.Stages, sd)
	}
	return d
}

func BuildsJSON(bs []provider.Build) []BuildDoc {
	out := []BuildDoc{}
	for _, b := range bs {
		out = append(out, BuildJSON(b))
	}
	return out
}

func PlanJSON(p provider.Plan) PlanDoc {
	d := PlanDoc{Key: p.Key, URL: p.URL, Name: p.Name, ProjectKey: p.ProjectKey}
	if lb := p.LastBuild; lb != nil {
		d.LastBuild = &LastBuildDoc{Key: lb.Key, Number: lb.Number, State: string(lb.State), FinishedAt: timePtr(lb.FinishedAt), Reason: lb.Reason}
	}
	return d
}

func ProjectJSON(p provider.Project) ProjectDoc {
	return ProjectDoc{Key: p.Key, Name: p.Name, URL: p.URL}
}

func BranchJSON(b provider.Branch) BranchDoc {
	return BranchDoc{Key: b.Key, URL: b.URL, Name: b.Name, ShortName: b.ShortName, PlanKey: b.PlanKey, Enabled: !b.Disabled}
}

func VarRowsJSON(res app.PlanVarsResult) []VariableDoc {
	out := []VariableDoc{}
	for _, r := range res.Rows {
		d := VariableDoc{Name: r.Name, Value: r.Value, Masked: r.Masked, Source: "plan", LastUsed: r.LastUsed}
		if r.Masked {
			d.Value, d.LastUsed = app.MaskedDisplay, maskIfSet(r.LastUsed)
		}
		out = append(out, d)
	}
	return out
}

func TargetJSON(t app.TargetInfo) TargetDoc {
	d := TargetDoc{Name: t.Name, PlanKey: t.Plan, Server: t.Server, Branch: t.Branch, Defaults: map[string]string{},
		Options: map[string][]string{}, Required: nonNil(t.Required), Watch: t.Watch, TimeoutMS: ms(t.Timeout), DefinedIn: nonNil(t.DefinedIn)}
	for _, v := range t.Defaults {
		switch {
		case v.EnvRef != "":
			d.Defaults[v.Name] = v.Value
		case v.Secret:
			d.Defaults[v.Name] = app.MaskedDisplay
		default:
			d.Defaults[v.Name] = v.Value
		}
	}
	for k, v := range t.Options {
		d.Options[k] = v
	}
	return d
}

func RunJSON(b provider.Build, ref app.PlanRef, vs app.VarSet) RunDoc {
	d := RunDoc{Key: b.Key, URL: b.URL, PlanKey: ref.PlanKey, Branch: ref.Branch, Revision: ref.Revision, RevisionFrom: ref.RevisionFrom, Variables: map[string]string{}}
	changed := vs.Changed()
	names := make([]string, 0, len(changed))
	for n := range changed {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v, _ := vs.Get(n)
		d.Variables[n] = v.Display()
	}
	return d
}

// progressDoc converts an estimate, or nil when there is none.
func progressDoc(p provider.Progress) *ProgressDoc {
	if !p.Valid || p.Average <= 0 {
		return nil
	}
	return &ProgressDoc{Percent: p.Percent, AverageMS: ms(p.Average), ElapsedMS: ms(p.Elapsed),
		RemainingMS: ms(p.Remaining), Stage: p.Stage, Source: p.Source}
}

func EventJSON(e app.Event) EventDoc {
	d := EventDoc{Type: string(e.Type), Time: e.Time.UTC().Format(time.RFC3339), BuildKey: e.Build.Key,
		Name: e.Name, Key: e.Key, State: string(e.State), Progress: progressDoc(e.Progress)}
	switch e.Type {
	case app.EventDone:
		b := BuildJSON(e.Build)
		d.Build = &b
	case app.EventError:
		if e.Err != nil {
			d.Message = e.Err.Error()
		}
	}
	return d
}

// WriteJSON writes one indented document and a newline.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// WriteNDJSON writes one compact line.
func WriteNDJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
