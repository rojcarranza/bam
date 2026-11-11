package view

import (
	"fmt"
	"strings"

	"github.com/rojcarranza/bam/internal/app"
)

// StatusDoc is bam status --json.
type StatusDoc struct {
	Builds []StatusBuildDoc `json:"builds"`
}

// StatusBuildDoc is one build bam started. State is empty and Error set when
// the server could not be asked about it.
type StatusBuildDoc struct {
	Key       string       `json:"key"`
	URL       string       `json:"url"`
	State     string       `json:"state,omitempty"`
	PlanKey   string       `json:"plan_key"`
	Branch    string       `json:"branch"`
	Preset    string       `json:"preset"`
	Server    string       `json:"server"`
	StartedAt *string      `json:"started_at"`
	Here      bool         `json:"here"`
	Progress  *ProgressDoc `json:"progress,omitempty"`
	Error     string       `json:"error,omitempty"`
}

// StatusJSON is the document for entries; url builds a build's page.
func StatusJSON(es []app.StatusEntry, url func(app.JournalRun) string) StatusDoc {
	return StatusDoc{Builds: Docs(es, func(e app.StatusEntry) StatusBuildDoc {
		d := StatusBuildDoc{Key: e.Run.BuildKey, URL: url(e.Run), PlanKey: e.Run.PlanKey, Branch: e.Run.Branch, Preset: e.Run.Target,
			Server: e.Run.Server, StartedAt: timePtr(e.Run.StartedAt), Here: e.Here, Progress: progressDoc(e.Progress)}
		if e.Err != nil {
			d.Error = e.Err.Error()
		} else {
			d.State = string(e.Build.State)
		}
		return d
	})}
}

// Status prints the builds bam started, one line each. The first line marked
// "here" is the build of the checked-out branch.
func Status(o Out, es []app.StatusEntry, url func(app.JournalRun) string) error {
	if len(es) == 0 {
		fmt.Fprintln(o.W, "No builds started with bam in the last day")
		return nil
	}
	t := Table{Headers: []string{"", "BUILD", "STATE", "PROGRESS", "STARTED", "WHAT", "LINK"}, Flex: []int{6}}
	for _, e := range es {
		here := ""
		if e.Here {
			here = "→"
		}
		state, prog := statusCells(o, e)
		t.Rows = append(t.Rows, []string{here, o.Key(e.Run.BuildKey, url(e.Run)), state, prog, o.Time(e.Run.StartedAt), statusWhat(e.Run), url(e.Run)})
	}
	return t.Render(o)
}

func statusCells(o Out, e app.StatusEntry) (state, prog string) {
	if e.Err != nil {
		return "? unknown", ""
	}
	if e.Progress.Valid && !e.Build.State.Finished() {
		prog = fmt.Sprintf("%.0f%%", e.Progress.Percent*100)
	}
	return o.State(e.Build.State), prog
}

// statusWhat is the preset, or the plan, with its branch.
func statusWhat(r app.JournalRun) string {
	w := r.Target
	if w == "" {
		w = r.PlanKey
	}
	if r.Branch != "" {
		w += " · " + r.Branch
	}
	return w
}

// StatusShort is one line for a shell prompt, with no colour and no link:
// the build of the checked-out branch, else the newest, with its state and
// progress, and how many other builds are still running. It is empty when
// nothing was started in the last day.
func StatusShort(es []app.StatusEntry) string {
	if len(es) == 0 {
		return ""
	}
	first := es[0]
	var sb strings.Builder
	sb.WriteString(first.Run.BuildKey + " ")
	switch {
	case first.Err != nil:
		sb.WriteString("unknown")
	default:
		sb.WriteString(string(first.Build.State))
		if first.Progress.Valid && !first.Build.State.Finished() {
			fmt.Fprintf(&sb, " %.0f%%", first.Progress.Percent*100)
		}
	}
	others := 0
	for _, e := range es[1:] {
		if e.Err == nil && !e.Build.State.Finished() {
			others++
		}
	}
	if others > 0 {
		fmt.Fprintf(&sb, " (+%d running)", others)
	}
	return sb.String()
}
