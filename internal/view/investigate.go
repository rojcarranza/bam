package view

import (
	"fmt"
	"strings"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

// Investigation prints why a failed build failed: each failed job with its
// error lines in context, then the likely cause and what to try next.
func Investigation(o Out, inv app.Investigation) error {
	b := inv.Build
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s  %s\n", o.Key(b.Key, b.URL), o.State(b.State))
	if b.Reason != "" {
		fmt.Fprintf(&sb, "Reason: %s\n", b.Reason)
	}
	for _, j := range inv.Jobs {
		fmt.Fprintf(&sb, "\n%s / %s (%s)", j.Stage, j.Job.Name, j.Short)
		if len(j.Errors) == 0 {
			sb.WriteString("  no known error line\n")
			continue
		}
		fmt.Fprintf(&sb, "  first error at line %d\n", j.Errors[0].Line)
		for _, e := range j.Errors {
			for i, l := range e.Context {
				n, mark := e.Start+i, "  "
				if n == e.Line {
					mark, l = "> ", style.Bold(o.Style, l)
				}
				fmt.Fprintf(&sb, "  %s%5d  %s\n", mark, n, l)
			}
			if e.Count > 1 {
				fmt.Fprintf(&sb, "         (seen %d times)\n", e.Count)
			}
		}
	}
	sb.WriteString("\n")
	if inv.LikelyCause != "" {
		fmt.Fprintf(&sb, "Likely cause: %s\n", inv.LikelyCause)
	} else {
		sb.WriteString("Likely cause: unknown, no log line matched a known error pattern\n")
	}
	for _, j := range inv.Jobs {
		fmt.Fprintf(&sb, "try: bam build logs %s --job %s\n", b.Key, j.Short)
	}
	_, err := fmt.Fprint(o.W, sb.String())
	return err
}

type ErrorBlockDoc struct {
	Line    int      `json:"line"`
	Text    string   `json:"text"`
	Pattern string   `json:"pattern"`
	Start   int      `json:"start"`
	Context []string `json:"context"`
	Count   int      `json:"count"`
}

type JobErrorsDoc struct {
	Key    string          `json:"key"`
	URL    string          `json:"url"`
	Name   string          `json:"name"`
	Stage  string          `json:"stage"`
	Short  string          `json:"short"`
	Errors []ErrorBlockDoc `json:"errors"`
}

type InvestigationDoc struct {
	Build       BuildDoc       `json:"build"`
	LikelyCause string         `json:"likely_cause"`
	Jobs        []JobErrorsDoc `json:"jobs"`
}

// InvestigationJSON is the `build investigate --json` document.
func InvestigationJSON(inv app.Investigation) InvestigationDoc {
	d := InvestigationDoc{Build: BuildJSON(inv.Build), LikelyCause: inv.LikelyCause, Jobs: []JobErrorsDoc{}}
	for _, j := range inv.Jobs {
		jd := JobErrorsDoc{Key: j.Job.Key, URL: j.Job.URL, Name: j.Job.Name, Stage: j.Stage, Short: j.Short, Errors: []ErrorBlockDoc{}}
		for _, e := range j.Errors {
			jd.Errors = append(jd.Errors, ErrorBlockDoc{Line: e.Line, Text: e.Text, Pattern: e.Pattern, Start: e.Start, Context: e.Context, Count: e.Count})
		}
		d.Jobs = append(d.Jobs, jd)
	}
	return d
}
