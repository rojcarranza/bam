package view

import (
	"fmt"
	"strings"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
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
		writeJobErrors(&sb, o, j)
	}
	sb.WriteString("\n")
	writeCause(&sb, inv)
	writeInvestigationHints(&sb, inv)
	_, err := fmt.Fprint(o.W, sb.String())
	return err
}

// writeJobErrors prints one failed job: its header, a truncation note, the
// error blocks in context and how many more errors were left out.
func writeJobErrors(sb *strings.Builder, o Out, j app.JobErrors) {
	fmt.Fprintf(sb, "\n%s / %s (%s)", j.Stage, j.Job.Name, j.Short)
	if len(j.Errors) == 0 {
		sb.WriteString("  no known error line\n")
	} else {
		fmt.Fprintf(sb, "  first error at line %d\n", j.Errors[0].Line)
	}
	if j.Skipped > 0 {
		fmt.Fprintf(sb, "  log truncated: the first %d lines were not read\n", j.Skipped)
	}
	writeErrorBlocks(sb, o, j.Errors)
	if j.Hidden == 1 {
		sb.WriteString("  1 more error not shown; --max 0 shows all\n")
	} else if j.Hidden > 1 {
		fmt.Fprintf(sb, "  %d more errors not shown; --max 0 shows all\n", j.Hidden)
	}
}

// writeErrorBlocks prints the error blocks of one job. Blocks close together
// share context lines; print each line once and mark every error line, even
// one shown as another's context.
func writeErrorBlocks(sb *strings.Builder, o Out, blocks []app.ErrorBlock) {
	errLines := map[int]bool{}
	for _, e := range blocks {
		errLines[e.Line] = true
	}
	last := 0
	for _, e := range blocks {
		for i, l := range e.Context {
			n := e.Start + i
			if n <= last {
				continue
			}
			last = n
			mark := "  "
			if errLines[n] {
				mark, l = "> ", style.Bold(o.Style, l)
			}
			fmt.Fprintf(sb, "  %s%5d  %s\n", mark, n, l)
		}
		if e.Count > 1 {
			fmt.Fprintf(sb, "         (seen %d times)\n", e.Count)
		}
	}
}

// writeCause prints the failed tests, the likely cause and, with --compare,
// what changed since the last success.
func writeCause(sb *strings.Builder, inv app.Investigation) {
	if t := inv.Build.FailedTests; len(t) > 5 {
		fmt.Fprintf(sb, "Failed tests: %s (+%d more)\n", strings.Join(t[:5], ", "), len(t)-5)
	} else if len(t) > 0 {
		fmt.Fprintf(sb, "Failed tests: %s\n", strings.Join(t, ", "))
	} else {
		sb.WriteString("Failed tests: none reported\n")
	}
	if inv.LikelyCause != "" {
		fmt.Fprintf(sb, "Likely cause: %s\n", inv.LikelyCause)
	} else {
		sb.WriteString("Likely cause: unknown, no log line matched a known error pattern\n")
	}
	if c := inv.Changes; c != nil {
		sb.WriteString(changesLine(*c))
	}
}

// writeInvestigationHints prints the try: lines for the next commands.
func writeInvestigationHints(sb *strings.Builder, inv app.Investigation) {
	b := inv.Build
	for _, j := range inv.Jobs {
		fmt.Fprintf(sb, "try: bam logs %s --job %s\n", b.Key, j.Short)
	}
	parent := b.Parent
	if parent == "" {
		parent, _ = app.ParentOf(b.Reason)
	}
	if parent != "" {
		fmt.Fprintf(sb, "try: bam build tree %s\n", parent)
	}
	if len(inv.Children) > 0 {
		fmt.Fprintf(sb, "try: bam build tree %s\n", b.Key)
	}
	if !inv.NoRerun {
		fmt.Fprintf(sb, "try: bam build rerun %s\n", b.Key)
	}
	fmt.Fprintf(sb, "try: bam open %s\n", b.Key)
}

// changesLine is the --compare line: what differs from the last success.
func changesLine(c app.Comparison) string {
	if c.Since.Key == "" {
		return "Changed since last success: no earlier successful build\n"
	}
	var parts []string
	for _, r := range c.Revisions {
		parts = append(parts, changeText(r.Name+" ", shortRev(r.Now), shortRev(r.Was)))
	}
	for _, v := range c.Variables {
		parts = append(parts, changeText("var "+v.Name+"=", v.Now, v.Was))
	}
	switch {
	case len(parts) == 0 && c.Variables == nil:
		parts = []string{"no revision changed"}
	case len(parts) == 0:
		parts = []string{"no revision or variable changed"}
	}
	if c.Variables == nil {
		parts = append(parts, "variables not readable on this server")
	}
	return fmt.Sprintf("Changed since last success (%s): %s\n", c.Since.Key, strings.Join(parts, ", "))
}

func changeText(label, now, was string) string {
	switch {
	case now == "":
		return label + "(removed, was " + was + ")"
	case was == "":
		return label + now + " (new)"
	}
	return label + now + " (was " + was + ")"
}

func shortRev(r string) string { return provider.Revision{Revision: r}.Short() }

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
	// SkippedLines counts the lines at the start of a long log that were not read.
	SkippedLines int `json:"skipped_lines"`
	// HiddenErrors counts the error blocks --max left out.
	HiddenErrors int `json:"hidden_errors"`
}

type InvestigationDoc struct {
	Build       BuildDoc       `json:"build"`
	LikelyCause string         `json:"likely_cause"`
	Jobs        []JobErrorsDoc `json:"jobs"`
	Changes     *ChangesDoc    `json:"changes"` // null without --compare
}

type ChangeDoc struct {
	Name string `json:"name"`
	Was  string `json:"was"`
	Now  string `json:"now"`
}

type ChangesDoc struct {
	Since     *BuildDoc   `json:"since"`     // null when no earlier build succeeded
	Revisions []ChangeDoc `json:"revisions"` // by repository name
	Variables []ChangeDoc `json:"variables"` // null when the server cannot read build variables back
}

// InvestigationJSON is the `build investigate --json` document.
func InvestigationJSON(inv app.Investigation) InvestigationDoc {
	d := InvestigationDoc{Build: BuildJSON(inv.Build), LikelyCause: inv.LikelyCause}
	d.Jobs = Docs(inv.Jobs, func(j app.JobErrors) JobErrorsDoc {
		return JobErrorsDoc{Key: j.Job.Key, URL: j.Job.URL, Name: j.Job.Name, Stage: j.Stage, Short: j.Short, SkippedLines: j.Skipped, HiddenErrors: j.Hidden,
			Errors: Docs(j.Errors, func(e app.ErrorBlock) ErrorBlockDoc {
				return ErrorBlockDoc{Line: e.Line, Text: e.Text, Pattern: e.Pattern, Start: e.Start, Context: e.Context, Count: e.Count}
			})}
	})
	if c := inv.Changes; c != nil {
		cd := &ChangesDoc{Revisions: changeDocs(c.Revisions)}
		if c.Since.Key != "" {
			sd := BuildJSON(c.Since)
			cd.Since = &sd
		}
		if c.Variables != nil {
			cd.Variables = changeDocs(c.Variables)
		}
		d.Changes = cd
	}
	return d
}

func changeDocs(cs []app.Change) []ChangeDoc {
	return Docs(cs, func(c app.Change) ChangeDoc { return ChangeDoc(c) })
}
