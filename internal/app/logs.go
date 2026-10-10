package app

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// LogsOptions selects which job logs to fetch.
type LogsOptions struct {
	Failed bool   // only failed jobs
	Job    string // JOB1, PROJ-PLAN-JOB1 or PROJ-PLAN-JOB1-44
	Stage  string // one stage's jobs, read while they run; wins over Failed
	Tail   int    // last N lines per job; 0 = all
}

// JobLog is the log of one job.
type JobLog struct {
	Job   provider.Job
	Stage string
	Lines []string
	Next  int // offset for FollowLog
}

type stagedJob struct {
	stage string
	job   provider.Job
}

func buildNumber(b provider.Build) string {
	if b.Number > 0 {
		return strconv.Itoa(b.Number)
	}
	return b.Key[strings.LastIndex(b.Key, "-")+1:]
}

// ShortJobKey returns the job part of a job result key: PROJ-PLAN-INT-44 → INT.
func ShortJobKey(b provider.Build, j provider.Job) string {
	k := strings.TrimSuffix(j.Key, "-"+buildNumber(b))
	return strings.TrimPrefix(k, b.PlanKey+"-")
}

func selectJobs(b provider.Build, o LogsOptions) ([]stagedJob, error) {
	var all []stagedJob
	for _, s := range b.Stages {
		for _, j := range s.Jobs {
			all = append(all, stagedJob{s.Name, j})
		}
	}
	if o.Job != "" {
		n := buildNumber(b)
		for _, sj := range all {
			k := sj.job.Key
			if k == o.Job || k == o.Job+"-"+n || k == b.PlanKey+"-"+o.Job+"-"+n {
				return []stagedJob{sj}, nil
			}
		}
		names := make([]string, 0, len(all))
		for _, sj := range all {
			names = append(names, ShortJobKey(b, sj.job)+" ("+sj.job.Name+")")
		}
		return nil, errs.Usagef("job %q is not in %s", o.Job, b.Key).WithWhy("jobs: " + strings.Join(names, ", "))
	}
	if o.Stage != "" {
		names := make([]string, 0, len(b.Stages))
		for _, st := range b.Stages {
			if st.Name == o.Stage {
				out := make([]stagedJob, 0, len(st.Jobs))
				for _, j := range st.Jobs {
					out = append(out, stagedJob{st.Name, j})
				}
				return out, nil
			}
			names = append(names, st.Name)
		}
		return nil, errs.Usagef("stage %q is not in %s", o.Stage, b.Key).WithWhy("stages: " + strings.Join(names, ", "))
	}
	if !o.Failed {
		return all, nil
	}
	var failed []stagedJob
	for _, sj := range all {
		if sj.job.State == provider.StateFailed {
			failed = append(failed, sj)
		}
	}
	return failed, nil
}

// Logs fetches the logs of the selected jobs of b, in stage order.
func (s *Service) Logs(ctx context.Context, b provider.Build, o LogsOptions) ([]JobLog, error) {
	jobs, err := selectJobs(b, o)
	if err != nil {
		return nil, err
	}
	out := make([]JobLog, 0, len(jobs))
	for _, sj := range jobs {
		// A stage is read while it runs: a job that has not started has no
		// log, and a running one may not have one yet. Only then; bam logs
		// keeps reporting a missing log.
		if o.Stage != "" && notStarted(sj.job.State) {
			out = append(out, JobLog{Job: sj.job, Stage: sj.stage})
			continue
		}
		chunk, err := s.P.FetchLog(ctx, sj.job.Key, provider.LogOptions{})
		if err != nil {
			if o.Stage == "" || !errors.Is(err, errs.ErrNotFound) {
				return nil, err
			}
			chunk = provider.LogChunk{}
		}
		lines := chunk.Lines
		if o.Tail > 0 && len(lines) > o.Tail {
			lines = lines[len(lines)-o.Tail:]
		}
		out = append(out, JobLog{Job: sj.job, Stage: sj.stage, Lines: lines, Next: chunk.Next})
	}
	return out, nil
}

func notStarted(s provider.State) bool {
	return s == provider.StateQueued || s == provider.StateSkipped || s == provider.StateNotBuilt
}

// CurrentStage names the stage a live log shows: the first running stage, or
// else the first that has neither finished nor been skipped.
func CurrentStage(b provider.Build) (string, bool) {
	for _, st := range b.Stages {
		if st.State == provider.StateRunning {
			return st.Name, true
		}
	}
	for _, st := range b.Stages {
		if !st.State.Finished() && st.State != provider.StateSkipped {
			return st.Name, true
		}
	}
	return "", false
}

// FollowLog emits new lines of job from offset until the job finishes.
func (s *Service) FollowLog(ctx context.Context, buildKey string, job provider.Job, offset int, emit func([]string)) error {
	for {
		chunk, err := s.P.FetchLog(ctx, job.Key, provider.LogOptions{Offset: offset})
		if err != nil {
			return err
		}
		if len(chunk.Lines) > 0 {
			emit(chunk.Lines)
		}
		offset = chunk.Next
		b, err := s.Build(ctx, buildKey)
		if err != nil {
			return err
		}
		if jobFinished(b, job.Key) {
			last, err := s.P.FetchLog(ctx, job.Key, provider.LogOptions{Offset: offset})
			if err != nil {
				return err
			}
			if len(last.Lines) > 0 {
				emit(last.Lines)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.Clock.After(MinPoll):
		}
	}
}

func jobFinished(b provider.Build, key string) bool {
	if b.State.Finished() {
		return true
	}
	for _, st := range b.Stages {
		for _, j := range st.Jobs {
			if j.Key == key {
				return j.State.Finished()
			}
		}
	}
	return false
}

// Cancel stops a queued or running build. A finished build is left alone and
// reported with alreadyFinished.
func (s *Service) Cancel(ctx context.Context, key string) (provider.Build, bool, error) {
	b, err := s.Build(ctx, key)
	if err != nil {
		return b, false, err
	}
	if b.State.Finished() {
		return b, true, nil
	}
	if err := s.P.StopBuild(ctx, key); err != nil {
		return b, false, err
	}
	return b, false, nil
}
