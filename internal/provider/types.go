// Package provider holds the CI-neutral domain types and the Provider
// interface. Nothing Bamboo-shaped appears here.
package provider

import "time"

// State is the neutral state of a build, stage or job.
type State string

const (
	StateQueued   State = "queued"
	StateRunning  State = "running"
	StateSuccess  State = "success"
	StateFailed   State = "failed"
	StateStopped  State = "stopped"
	StateSkipped  State = "skipped"
	StateNotBuilt State = "not_built"
	StateUnknown  State = "unknown"
)

// AllStates lists every state, for exhaustive tests and renderers.
func AllStates() []State {
	return []State{StateQueued, StateRunning, StateSuccess, StateFailed, StateStopped, StateSkipped, StateNotBuilt, StateUnknown}
}

// Finished reports whether a build in this state will not change again.
func (s State) Finished() bool {
	switch s {
	case StateSuccess, StateFailed, StateStopped, StateNotBuilt:
		return true
	}
	return false
}

type User struct {
	Name     string
	FullName string
}

type ServerInfo struct {
	Version string
}

type Project struct {
	Key  string
	Name string
	URL  string
}

// BuildSummary is the last build shown next to a plan in listings.
type BuildSummary struct {
	Key        string
	Number     int
	State      State
	FinishedAt time.Time
	Reason     string
}

type Plan struct {
	Key        string
	Name       string
	ProjectKey string
	URL        string
	LastBuild  *BuildSummary
}

type Branch struct {
	Key       string // branch plan key, e.g. PROJ-PLAN12
	Name      string
	ShortName string
	PlanKey   string // master plan key
	URL       string
}

type Variable struct {
	Name   string
	Value  string
	Masked bool
}

type Job struct {
	Key      string // job result key, e.g. PROJ-PLAN-JOB1-44
	Name     string
	State    State
	Duration time.Duration
	URL      string
}

type Stage struct {
	Name     string
	State    State
	Duration time.Duration
	Jobs     []Job
}

type Revision struct {
	Repository string
	Revision   string
}

// Short returns the first seven characters of the revision.
func (r Revision) Short() string {
	if len(r.Revision) > 7 {
		return r.Revision[:7]
	}
	return r.Revision
}

type Build struct {
	Key           string // e.g. PROJ-PLAN-44 or PROJ-PLAN12-5
	URL           string
	PlanKey       string // plan (or branch plan) the build belongs to
	Branch        string // branch short name; empty for the default branch
	Number        int
	State         State
	Reason        string // plain text, no HTML
	Parent        string // the build that triggered this one as a child; GetBuild only, empty when none
	CustomBuild   bool
	Labels        []string
	QueuedAt      time.Time
	StartedAt     time.Time
	FinishedAt    time.Time
	QueueDuration time.Duration
	Duration      time.Duration
	Agent         string
	Revision      string // default repository's commit; empty when Bamboo reports none
	Revisions     []Revision
	Stages        []Stage
	FailedTests   []string
}

// FailedJobs returns failed jobs in stage order.
func (b Build) FailedJobs() []Job {
	var out []Job
	for _, s := range b.Stages {
		for _, j := range s.Jobs {
			if j.State == StateFailed {
				out = append(out, j)
			}
		}
	}
	return out
}

// Progress is how far a running build has got, as the server reports it. The
// zero value means "no estimate", which is not an error.
type Progress struct {
	Valid     bool          // the server had a usable estimate
	Average   time.Duration // the plan's average build duration
	Elapsed   time.Duration // build time so far, as the server counts it
	Remaining time.Duration // Average - Elapsed, floored at zero
	Percent   float64       // 0..1, clamped; 1 once Elapsed >= Average
	Stage     string        // the stage running now; may be empty
	Source    string        // "bamboo" or "history"; empty without an estimate
}

type ListOptions struct {
	Limit int   // 0 means the provider default
	State State // empty means all states
	// Unfinished also lists queued and running builds. Without it a provider
	// lists finished builds only, as Bamboo does by default.
	Unfinished bool
}

type TriggerRequest struct {
	PlanKey   string
	Variables map[string]string
	Secret    map[string]bool // names whose values must be redacted in logs
	Revision  string          // commit to build; empty builds the newest
}

type LogOptions struct {
	Offset int // number of lines already read
}

type LogChunk struct {
	Lines []string
	Next  int // offset for the next call
}
