package view

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

func Projects(o Out, ps []provider.Project) error {
	t := Table{Headers: []string{"PROJECT", "NAME"}, Flex: []int{1}}
	for _, p := range ps {
		t.Rows = append(t.Rows, []string{o.Key(p.Key, p.URL), p.Name})
	}
	return t.Render(o)
}

// PlanGroup is one project's plans in bam plan list.
type PlanGroup struct {
	Project provider.Project
	Plans   []provider.Plan
}

func PlanList(o Out, groups []PlanGroup) error {
	for i, g := range groups {
		if i > 0 {
			fmt.Fprintln(o.W)
		}
		fmt.Fprintln(o.W, strings.TrimSpace(style.Bold(o.Style, g.Project.Key)+"  "+g.Project.Name))
		t := Table{Indent: "  ", Headers: []string{"PLAN", "NAME", "LAST", "STATE", "COMPLETED", "REASON"}, Flex: []int{5, 1}}
		for _, p := range g.Plans {
			if p.LastBuild == nil {
				t.Rows = append(t.Rows, []string{o.Key(p.Key, p.URL), planName(p), "–", style.Dim(o.Style, "– never"), "–", "Never built"})
				continue
			}
			lb := p.LastBuild
			t.Rows = append(t.Rows, []string{o.Key(p.Key, p.URL), planName(p), fmt.Sprintf("#%d", lb.Number), o.State(lb.State), o.Time(lb.FinishedAt), lb.Reason})
		}
		if err := t.Render(o); err != nil {
			return err
		}
	}
	return nil
}

func branchLabel(b string) string {
	if b == "" {
		return "default"
	}
	return b
}

func BuildList(o Out, bs []provider.Build) error { return BuildListWithProgress(o, bs, nil) }

// BuildListWithProgress adds an ETA column, filled for the running builds
// that the server could estimate. Without any estimate the table is the
// same one BuildList renders.
func BuildListWithProgress(o Out, bs []provider.Build, ps map[string]provider.Progress) error {
	eta := false
	for _, b := range bs {
		if p, ok := ps[b.Key]; ok && p.Valid {
			eta = true
		}
	}
	t := Table{Headers: []string{"BUILD", "STATE", "BRANCH", "STARTED", "DURATION", "REASON"}, Flex: []int{5}}
	if eta {
		t.Headers = []string{"BUILD", "STATE", "BRANCH", "STARTED", "DURATION", "ETA", "REASON"}
		t.Flex = []int{6}
	}
	for _, b := range bs {
		row := []string{o.Key(b.Key, b.URL), o.State(b.State), branchLabel(b.Branch), o.Time(b.StartedAt), Duration(b.Duration)}
		if eta {
			cell := ""
			if p, ok := ps[b.Key]; ok && p.Valid {
				cell = Duration(p.Remaining)
			}
			row = append(row, cell)
		}
		t.Rows = append(t.Rows, append(row, b.Reason))
	}
	return t.Render(o)
}

func field(o Out, label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(o.W, "%-11s %s\n", label, value)
}

func BuildDetail(o Out, b provider.Build) error {
	return BuildDetailWithProgress(o, b, provider.Progress{})
}

// BuildDetailWithProgress prints a build, and the server's estimate for it
// when the build is still running.
func BuildDetailWithProgress(o Out, b provider.Build, p provider.Progress) error {
	fmt.Fprintf(o.W, "%s  %s  %s\n", style.Bold(o.Style, o.Key(b.Key, b.URL)), o.State(b.State), b.URL)
	plan := b.PlanKey
	if b.Branch != "" {
		plan += "  (branch " + b.Branch + ")"
	}
	field(o, "Plan", plan)
	field(o, "Reason", b.Reason)
	field(o, "Started", o.Time(b.StartedAt))
	if !b.FinishedAt.IsZero() {
		field(o, "Completed", o.Time(b.FinishedAt))
	}
	field(o, "Duration", Duration(b.Duration))
	if bar := Bar(o, p); bar != "" {
		field(o, "Estimate", Duration(p.Average)+"  "+bar)
	}
	if b.QueueDuration > 0 {
		field(o, "Queued", Duration(b.QueueDuration))
	}
	field(o, "Agent", b.Agent)
	if b.CustomBuild {
		field(o, "Flags", "CUSTOM BUILD")
	}
	field(o, "Labels", strings.Join(b.Labels, ", "))
	var revs []string
	for _, r := range b.Revisions {
		revs = append(revs, r.Repository+" "+r.Short())
	}
	field(o, "Revisions", strings.Join(revs, ", "))

	if len(b.Stages) > 0 {
		fmt.Fprintln(o.W)
		fmt.Fprintln(o.W, "Stages")
		t := Table{Indent: "  ", Headers: []string{"", ""}}
		for _, s := range b.Stages {
			t.Rows = append(t.Rows, []string{style.StateGlyph(o.Style, s.State) + " " + s.Name, durationIfRun(s.State, s.Duration)})
			for _, j := range s.Jobs {
				t.Rows = append(t.Rows, []string{"    " + style.StateGlyph(o.Style, j.State) + " " + j.Name, durationIfRun(j.State, j.Duration)})
			}
		}
		if err := RenderRows(o, t); err != nil {
			return err
		}
	}

	failed := b.FailedJobs()
	if len(failed) > 0 || len(b.FailedTests) > 0 {
		fmt.Fprintln(o.W)
		fmt.Fprintln(o.W, "Failure")
		for _, j := range failed {
			fmt.Fprintf(o.W, "  %s %s  bam logs %s --job %s\n", style.StateGlyph(o.Style, j.State), j.Name, b.Key, app.ShortJobKey(b, j))
		}
		if len(b.FailedTests) > 0 {
			shown := b.FailedTests
			more := ""
			if len(shown) > 5 {
				more = fmt.Sprintf(" (+%d more)", len(shown)-5)
				shown = shown[:5]
			}
			fmt.Fprintf(o.W, "  failed tests: %s%s\n", strings.Join(shown, ", "), more)
		}
	}
	return nil
}

func durationIfRun(s provider.State, d time.Duration) string {
	if s == provider.StateNotBuilt || s == provider.StateSkipped {
		return ""
	}
	return Duration(d)
}

// RenderRows renders a table without its header line.
func RenderRows(o Out, t Table) error {
	t.Headers = make([]string, len(t.Headers))
	var buf strings.Builder
	o2 := o
	o2.W = &buf
	if err := t.Render(o2); err != nil {
		return err
	}
	lines := strings.SplitAfterN(buf.String(), "\n", 2)
	if len(lines) == 2 {
		_, err := fmt.Fprint(o.W, lines[1])
		return err
	}
	return nil
}

// PlanDetail is everything bam plan show prints.
type PlanDetail struct {
	Plan     provider.Plan
	Branches []provider.Branch
	Vars     app.PlanVarsResult
	VarsErr  string
	Builds   []provider.Build
}

func PlanDetailView(o Out, d PlanDetail) error {
	fmt.Fprintf(o.W, "%s  %s  %s\n", style.Bold(o.Style, o.Key(d.Plan.Key, d.Plan.URL)), planName(d.Plan), d.Plan.URL)
	field(o, "Project", d.Plan.ProjectKey)
	var names []string
	for _, b := range d.Branches {
		names = append(names, branchName(b))
	}
	if len(names) == 0 {
		field(o, "Branches", "none")
	} else {
		// The count leads so it never reads as part of a "(disabled)" marker.
		field(o, "Branches", fmt.Sprintf("%d: %s", len(names), strings.Join(names, ", ")))
	}
	switch {
	case d.VarsErr != "":
		field(o, "Variables", d.VarsErr)
	case len(d.Vars.Rows) == 0:
		field(o, "Variables", "none")
	default:
		var vs []string
		for _, r := range d.Vars.Rows {
			v := r.Value
			if r.Masked {
				v = app.MaskedDisplay
			}
			vs = append(vs, r.Name+"="+v)
		}
		field(o, "Variables", strings.Join(vs, "  "))
	}
	if len(d.Builds) > 0 {
		fmt.Fprintln(o.W)
		fmt.Fprintln(o.W, "Recent builds")
		t := Table{Indent: "  ", Headers: []string{"BUILD", "STATE", "STARTED", "DURATION", "REASON"}, Flex: []int{4}}
		for _, b := range d.Builds {
			t.Rows = append(t.Rows, []string{o.Key(b.Key, b.URL), o.State(b.State), o.Time(b.StartedAt), Duration(b.Duration), b.Reason})
		}
		return t.Render(o)
	}
	return nil
}

func Branches(o Out, bs []provider.Branch) error {
	t := Table{Headers: []string{"BRANCH", "KEY"}}
	for _, b := range bs {
		t.Rows = append(t.Rows, []string{branchName(b), o.Key(b.Key, b.URL)})
	}
	return t.Render(o)
}

// MarkDisabled adds the marker for a plan or branch that Bamboo will not
// build until it is enabled.
func MarkDisabled(name string, disabled bool) string {
	if disabled {
		return name + " (disabled)"
	}
	return name
}

func planName(p provider.Plan) string { return MarkDisabled(p.Name, p.Disabled) }

func branchName(b provider.Branch) string { return MarkDisabled(b.ShortName, b.Disabled) }

func Vars(o Out, res app.PlanVarsResult) error {
	last := "LAST USED"
	if res.FromBuild != "" {
		last = fmt.Sprintf("LAST USED (#%s)", res.FromBuild[strings.LastIndex(res.FromBuild, "-")+1:])
	}
	t := Table{Headers: []string{"NAME", "VALUE", last}, Flex: []int{1, 2}}
	for _, r := range res.Rows {
		value, used := r.Value, r.LastUsed
		if r.Masked {
			value, used = app.MaskedDisplay, maskIfSet(used)
		}
		t.Rows = append(t.Rows, []string{r.Name, dashIfEmpty(value), dashIfEmpty(used)})
	}
	return t.Render(o)
}

func maskIfSet(v string) string {
	if v == "" {
		return ""
	}
	return app.MaskedDisplay
}

func dashIfEmpty(v string) string {
	if v == "" {
		return "–"
	}
	return v
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func Targets(o Out, ts []app.TargetInfo) error {
	t := Table{Headers: []string{"PRESET", "PLAN", "BRANCH", "SERVER", "WATCH", "DEFINED IN"}}
	for _, x := range ts {
		where := "-"
		if n := len(x.DefinedIn); n > 0 {
			where = x.DefinedIn[n-1]
		}
		t.Rows = append(t.Rows, []string{x.Name, x.Plan, dashIfEmpty(x.Branch), serverLabel(x.Server), yesNo(x.Watch), where})
	}
	return t.Render(o)
}

func serverLabel(s string) string {
	if s == "" {
		return "(default)"
	}
	return s
}

func TargetDetail(o Out, x app.TargetInfo) error {
	fmt.Fprintln(o.W, style.Bold(o.Style, x.Name))
	field(o, "Plan", x.Plan)
	field(o, "Branch", dashIfEmpty(x.Branch))
	field(o, "Server", serverLabel(x.Server))
	field(o, "Watch", yesNo(x.Watch))
	if x.Timeout > 0 {
		field(o, "Timeout", x.Timeout.String())
	}
	field(o, "Required", strings.Join(x.Required, ", "))
	field(o, "Defined in", strings.Join(x.DefinedIn, ", "))
	if len(x.Defaults) > 0 {
		fmt.Fprintln(o.W)
		fmt.Fprintln(o.W, "Variables")
		t := Table{Indent: "  ", Headers: []string{"", ""}}
		for _, v := range x.Defaults {
			t.Rows = append(t.Rows, []string{v.Name, v.Display()})
		}
		if err := RenderRows(o, t); err != nil {
			return err
		}
	}
	if len(x.Options) > 0 {
		fmt.Fprintln(o.W)
		fmt.Fprintln(o.W, "Options")
		names := make([]string, 0, len(x.Options))
		for n := range x.Options {
			names = append(names, n)
		}
		sort.Strings(names)
		t := Table{Indent: "  ", Headers: []string{"", ""}}
		for _, n := range names {
			t.Rows = append(t.Rows, []string{n, strings.Join(x.Options[n], ", ")})
		}
		return RenderRows(o, t)
	}
	return nil
}
