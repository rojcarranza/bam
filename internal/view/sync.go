package view

import (
	"fmt"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

// Sync statuses, as they appear in the --json status field.
const (
	SyncSynced    = "synced"
	SyncUpToDate  = "up_to_date"
	SyncWouldSync = "would_sync"
	SyncError     = "error"
)

// SyncResult is one target's outcome of bam preset sync.
type SyncResult struct {
	Sync       app.SyncPlan
	File       string   // Sync.File as shown in text: relative to the repository when inside it
	Status     string   // one of the Sync* constants
	Redeclared []string // names whose "not declared" marker was removed
	Err        error
}

// Sync renders one block per target: a header, then + added, ! stale and
// ~ re-declared names.
func Sync(o Out, rs []SyncResult, dryRun bool) error {
	for _, r := range rs {
		p := r.Sync
		switch r.Status {
		case SyncError:
			fmt.Fprintf(o.W, "%s  %s  error: %s\n", style.Bold(o.Style, p.Name), p.Plan, r.Err)
			continue
		case SyncUpToDate:
			fmt.Fprintf(o.W, "%s  %s  %s  up to date\n", style.Bold(o.Style, p.Name), p.Plan, r.File)
		case SyncWouldSync:
			fmt.Fprintf(o.W, "would sync: %s  %s  %s\n", style.Bold(o.Style, p.Name), p.Plan, r.File)
		default:
			fmt.Fprintf(o.W, "%s  %s  %s\n", style.Bold(o.Style, p.Name), p.Plan, r.File)
		}
		var rows [][]string
		for _, v := range p.Added {
			rows = append(rows, []string{"+", v.Name, quote(app.DisplayDraftValue(v)), v.Comment})
		}
		for _, n := range p.Stale {
			rows = append(rows, []string{"!", n, "", "not declared on " + p.Plan + " (kept)"})
		}
		for _, n := range r.Redeclared {
			rows = append(rows, []string{"~", n, "", "declared again on " + p.Plan + " (marker removed)"})
		}
		if len(rows) == 0 {
			continue
		}
		t := Table{Indent: "  ", Headers: rows[0], Rows: rows[1:]}
		if err := t.Render(o); err != nil {
			return err
		}
	}
	if dryRun {
		_, err := fmt.Fprintln(o.W, "dry run: no files written")
		return err
	}
	return nil
}

func quote(v string) string { return `"` + v + `"` }

type SyncVarDoc struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Comment string `json:"comment"`
}

type SyncDoc struct {
	Name       string       `json:"name"`
	PlanKey    string       `json:"plan_key"`
	File       string       `json:"file"`
	Status     string       `json:"status"`
	Added      []SyncVarDoc `json:"added"`
	Stale      []string     `json:"stale"`
	Redeclared []string     `json:"redeclared"`
	Error      string       `json:"error"`
}

// SyncJSON returns one document per target; File is the absolute path.
func SyncJSON(rs []SyncResult) []SyncDoc {
	docs := make([]SyncDoc, 0, len(rs))
	for _, r := range rs {
		d := SyncDoc{Name: r.Sync.Name, PlanKey: r.Sync.Plan, File: r.Sync.File, Status: r.Status,
			Added: []SyncVarDoc{}, Stale: nonNil(r.Sync.Stale), Redeclared: nonNil(r.Redeclared)}
		for _, v := range r.Sync.Added {
			d.Added = append(d.Added, SyncVarDoc{Name: v.Name, Value: app.DisplayDraftValue(v), Comment: v.Comment})
		}
		if r.Err != nil {
			d.Error = r.Err.Error()
		}
		docs = append(docs, d)
	}
	return docs
}
