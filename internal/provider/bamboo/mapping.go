package bamboo

import (
	"errors"
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// buildExpand is what GetBuild asks for.
// metadata carries the parent of a child build, which a restart drops from
// its reason ("Rebuilt by ...").
const buildExpand = "stages.stage.results.result,labels,vcsRevisions,metadata"

var (
	tagRe   = regexp.MustCompile(`<[^>]*>`)
	spaceRe = regexp.MustCompile(`\s+`)
)

// mapState turns Bamboo's lifeCycleState and state into a neutral State.
// A finished build whose state is Unknown was stopped.
func mapState(lifeCycle, state string) provider.State {
	switch strings.ToLower(lifeCycle) {
	case "queued", "pending":
		return provider.StateQueued
	case "inprogress":
		return provider.StateRunning
	case "notbuilt":
		return provider.StateNotBuilt
	case "skipped":
		return provider.StateSkipped
	case "finished":
		switch strings.ToLower(state) {
		case "successful":
			return provider.StateSuccess
		case "failed":
			return provider.StateFailed
		case "unknown":
			return provider.StateStopped
		}
	}
	return provider.StateUnknown
}

// plainReason strips Bamboo's HTML from a build reason.
func plainReason(s string) string {
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

var timeLayouts = []string{"2006-01-02T15:04:05.000Z07:00", time.RFC3339Nano, time.RFC3339}

func parseTime(s string) time.Time {
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func durationOf(r resultDTO) time.Duration {
	if r.BuildDuration > 0 {
		return time.Duration(r.BuildDuration) * time.Millisecond
	}
	return time.Duration(r.BuildDurationInSeconds) * time.Second
}

func resultKey(r resultDTO) string {
	if r.BuildResultKey != "" {
		return r.BuildResultKey
	}
	return r.Key
}

func stateOf(r resultDTO) provider.State {
	s := r.State
	if s == "" {
		s = r.BuildState
	}
	st := mapState(r.LifeCycleState, s)
	// Bamboo reports a build that was stopped as NotBuilt with state
	// Unknown -- the same shape as a job that never ran. A start time and
	// notRunYet=false say this one did run, so it was stopped.
	if st == provider.StateNotBuilt && !r.NotRunYet && r.BuildStartedTime != "" {
		return provider.StateStopped
	}
	return st
}

// jobState maps a job result. Bamboo omits notRunYet on nested job results
// and stamps a start time even on a job of a stage that never ran, so the
// build-level "stopped" heuristic must not be applied here: a NotBuilt job
// stays "not built", whether its build failed, was stopped, or skipped the
// stage.
func jobState(r resultDTO) provider.State {
	s := r.State
	if s == "" {
		s = r.BuildState
	}
	return mapState(r.LifeCycleState, s)
}

func (c *Client) mapBuild(r resultDTO) provider.Build {
	key := resultKey(r)
	reason := r.ReasonSummary
	if reason == "" {
		reason = r.BuildReason
	}
	b := provider.Build{
		Key:        key,
		URL:        c.URL(key),
		PlanKey:    r.Plan.Key,
		Number:     r.BuildNumber,
		State:      stateOf(r),
		Reason:     plainReason(reason),
		StartedAt:  parseTime(r.BuildStartedTime),
		FinishedAt: parseTime(r.BuildCompletedTime),
		Duration:   durationOf(r),
		Revision:   r.VcsRevisionKey,
	}
	if b.PlanKey == "" {
		if i := strings.LastIndex(key, "-"); i > 0 {
			b.PlanKey = key[:i]
		}
	}
	// A branch build's planName and plan short name are the branch's; the
	// pipeline's own name is its master plan's.
	b.PlanName = r.Plan.ShortName
	if b.PlanName == "" {
		b.PlanName = r.PlanName
	}
	if r.Plan.Master != nil {
		b.Branch = r.Plan.ShortName
		b.MasterKey = r.Plan.Master.Key
		b.PlanName = r.Plan.Master.ShortName
	}
	b.CustomBuild = strings.Contains(strings.ToLower(b.Reason), "custom build")
	for _, m := range r.Metadata.Item {
		if m.Key == "dependency.parent.0" {
			b.Parent = m.Value
		}
	}
	for _, l := range r.Labels.Label {
		b.Labels = append(b.Labels, l.Name)
	}
	for _, v := range r.VcsRevisions.VcsRevision {
		b.Revisions = append(b.Revisions, provider.Revision{Repository: v.RepositoryName, Revision: v.VcsRevisionKey})
	}
	for _, s := range r.Stages.Stage {
		st := provider.Stage{Name: s.Name, State: mapState(s.LifeCycleState, s.State)}
		for _, j := range s.Results.Result {
			job := provider.Job{
				Key:      resultKey(j),
				Name:     j.Plan.ShortName,
				State:    jobState(j),
				Duration: durationOf(j),
				URL:      c.URL(resultKey(j)),
			}
			if job.Duration > st.Duration {
				st.Duration = job.Duration
			}
			st.Jobs = append(st.Jobs, job)
		}
		b.Stages = append(b.Stages, st)
	}
	return b
}

// notFoundAs rewrites a bare 404 into a message naming the entity.
func (c *Client) notFoundAs(err error, what, key, try string) error {
	if !errors.Is(err, errs.ErrNotFound) {
		return err
	}
	e := errs.Bamboof("%s %s not found on %s", what, key, c.base.Host).Wrap(errs.ErrNotFound)
	if try != "" {
		_ = e.WithTry(try)
	}
	return e
}
