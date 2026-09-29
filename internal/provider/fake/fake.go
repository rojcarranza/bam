// Package fake provides a scripted provider.Provider for tests.
package fake

import (
	"context"
	"sync"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

var _ provider.Provider = (*Provider)(nil)

// Provider returns whatever its fields hold. Zero value is usable.
type Provider struct {
	BaseURL       string
	User          provider.User
	UserErr       error
	Info          provider.ServerInfo
	Projects      []provider.Project
	Plans         map[string][]provider.Plan     // by project key
	Branches      map[string][]provider.Branch   // by master plan key
	Variables     map[string][]provider.Variable // by plan key
	VariablesErr  error
	History       map[string][]provider.Build // by plan key, newest first
	Sequences     map[string][]provider.Build // by build key; GetBuild walks it and repeats the last entry
	BuildVars     map[string]map[string]string
	BuildVarsErr  error
	Logs          map[string][]string // by job result key
	TriggerResult provider.Build
	TriggerErr    error
	// by plan key; TriggerResult and TriggerErr apply when a key is missing
	TriggerResults map[string]provider.Build
	TriggerErrs    map[string]error
	StopErr        error
	StopErrs       []error                        // returned by StopBuild in order, one per call; after them StopErr applies
	LogErrs        []error                        // returned by FetchLog in order, one per call; nil entries succeed
	Progressions   map[string][]provider.Progress // by build key; BuildProgress walks it and repeats the last entry
	ProgressErr    error

	mu           sync.Mutex
	Triggered    []provider.TriggerRequest
	Stopped      []string
	getCalls     map[string]int
	progressCall map[string]int
}

func notFound(what, key string) error {
	return errs.Bamboof("%s %s not found", what, key).Wrap(errs.ErrNotFound)
}

func (f *Provider) CurrentUser(context.Context) (provider.User, error) { return f.User, f.UserErr }

func (f *Provider) ServerInfo(context.Context) (provider.ServerInfo, error) { return f.Info, nil }

func (f *Provider) ListProjects(context.Context) ([]provider.Project, error) { return f.Projects, nil }

func (f *Provider) ListPlans(_ context.Context, project string) ([]provider.Plan, error) {
	plans, ok := f.Plans[project]
	if !ok {
		return nil, notFound("project", project)
	}
	return plans, nil
}

func (f *Provider) GetPlan(_ context.Context, key string) (provider.Plan, error) {
	for _, plans := range f.Plans {
		for _, p := range plans {
			if p.Key == key {
				return p, nil
			}
		}
	}
	return provider.Plan{}, notFound("plan", key)
}

func (f *Provider) ListBranches(_ context.Context, key string) ([]provider.Branch, error) {
	return f.Branches[key], nil
}

func (f *Provider) ListVariables(_ context.Context, key string) ([]provider.Variable, error) {
	if f.VariablesErr != nil {
		return nil, f.VariablesErr
	}
	return f.Variables[key], nil
}

func (f *Provider) ListBuilds(_ context.Context, key string, o provider.ListOptions) ([]provider.Build, error) {
	var out []provider.Build
	for _, b := range f.History[key] {
		if o.State != "" && b.State != o.State {
			continue
		}
		out = append(out, b)
		if o.Limit > 0 && len(out) == o.Limit {
			break
		}
	}
	return out, nil
}

func (f *Provider) GetBuild(_ context.Context, key string) (provider.Build, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getCalls == nil {
		f.getCalls = map[string]int{}
	}
	n := f.getCalls[key]
	f.getCalls[key] = n + 1
	if seq, ok := f.Sequences[key]; ok && len(seq) > 0 {
		if n >= len(seq) {
			n = len(seq) - 1
		}
		return seq[n], nil
	}
	for _, builds := range f.History {
		for _, b := range builds {
			if b.Key == key {
				return b, nil
			}
		}
	}
	return provider.Build{}, notFound("build", key)
}

func (f *Provider) BuildProgress(_ context.Context, key string) (provider.Progress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.progressCall == nil {
		f.progressCall = map[string]int{}
	}
	n := f.progressCall[key]
	f.progressCall[key] = n + 1
	if f.ProgressErr != nil {
		return provider.Progress{}, f.ProgressErr
	}
	seq, ok := f.Progressions[key]
	if !ok || len(seq) == 0 {
		return provider.Progress{}, nil
	}
	if n >= len(seq) {
		n = len(seq) - 1
	}
	return seq[n], nil
}

// ProgressCalls reports how many times BuildProgress was called for key.
func (f *Provider) ProgressCalls(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.progressCall[key]
}

// GetCalls reports how many times GetBuild was called for key.
func (f *Provider) GetCalls(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.getCalls[key]
}

func (f *Provider) BuildVariables(_ context.Context, key string) (map[string]string, error) {
	if f.BuildVarsErr != nil {
		return nil, f.BuildVarsErr
	}
	return f.BuildVars[key], nil
}

func (f *Provider) Trigger(_ context.Context, req provider.TriggerRequest) (provider.Build, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Triggered = append(f.Triggered, req)
	if err, ok := f.TriggerErrs[req.PlanKey]; ok {
		return provider.Build{}, err
	}
	if b, ok := f.TriggerResults[req.PlanKey]; ok {
		return b, nil
	}
	return f.TriggerResult, f.TriggerErr
}

func (f *Provider) StopBuild(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Stopped = append(f.Stopped, key)
	if len(f.StopErrs) > 0 {
		err := f.StopErrs[0]
		f.StopErrs = f.StopErrs[1:]
		return err
	}
	return f.StopErr
}

func (f *Provider) FetchLog(_ context.Context, key string, o provider.LogOptions) (provider.LogChunk, error) {
	f.mu.Lock()
	if len(f.LogErrs) > 0 {
		err := f.LogErrs[0]
		f.LogErrs = f.LogErrs[1:]
		if err != nil {
			f.mu.Unlock()
			return provider.LogChunk{}, err
		}
	}
	f.mu.Unlock()
	lines, ok := f.Logs[key]
	if !ok {
		return provider.LogChunk{}, notFound("job", key)
	}
	if o.Offset > len(lines) {
		o.Offset = len(lines)
	}
	return provider.LogChunk{Lines: lines[o.Offset:], Next: len(lines)}, nil
}

func (f *Provider) URL(key string) string { return f.BaseURL + "/browse/" + key }
