package app

import (
	"context"

	"github.com/r0jjames/bam-cli/internal/provider"
)

// InvestigateOptions shape the error blocks of each failed job.
type InvestigateOptions struct {
	Context int // lines either side of each error
	Max     int // error blocks per job; 0 = all
}

// JobErrors is one failed job and the errors found in its log.
type JobErrors struct {
	Job     provider.Job
	Stage   string
	Short   string // job part of the key, e.g. APPLY
	Errors  []ErrorBlock
	Skipped int // lines at the start of a long log that were not read
}

// Investigation is why a build failed, as far as its logs tell.
type Investigation struct {
	Build       provider.Build
	Jobs        []JobErrors // failed jobs in stage order
	LikelyCause string      // empty when no log matched a pattern
	Changes     *Comparison // set by the caller for --compare; nil otherwise
}

// Investigate reads the failed jobs' logs of b and finds their error lines.
// The first error of the first failed job that has one is the likely cause.
func (s *Service) Investigate(ctx context.Context, b provider.Build, o InvestigateOptions) (Investigation, error) {
	inv := Investigation{Build: b}
	if b.State != provider.StateFailed {
		return inv, nil
	}
	logs, err := s.Logs(ctx, b, LogsOptions{Failed: true})
	if err != nil {
		return inv, err
	}
	for _, l := range logs {
		je := JobErrors{Job: l.Job, Stage: l.Stage, Short: ShortJobKey(b, l.Job), Errors: ErrorBlocks(l.Lines, o.Context, o.Max)}
		// The provider reads only the last part of a very long log; Next
		// still counts the whole log, so line numbers shift by what it left out.
		if je.Skipped = max(0, l.Next-len(l.Lines)); je.Skipped > 0 {
			for i := range je.Errors {
				je.Errors[i].Line += je.Skipped
				je.Errors[i].Start += je.Skipped
			}
		}
		if inv.LikelyCause == "" && len(je.Errors) > 0 {
			inv.LikelyCause = je.Errors[0].Text + " (job " + je.Short + ")"
		}
		inv.Jobs = append(inv.Jobs, je)
	}
	return inv, nil
}
