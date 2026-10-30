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

// elapsed is a build's time so far; "–" only when it is not known.
func elapsed(o Out, b provider.Build) string {
	from := b.StartedAt
	if from.IsZero() {
		from = b.QueuedAt
	}
	switch {
	case b.State.Finished():
		return Duration(b.Duration)
	case from.IsZero():
		return "–"
	}
	d := o.Now().Sub(from)
	if d < time.Second {
		return "0s" // its first second, or a server clock a little ahead of ours
	}
	return Duration(d.Truncate(time.Second))
}

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
