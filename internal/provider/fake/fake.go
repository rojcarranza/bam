// Package fake provides a scripted provider.Provider for tests.
package fake

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
)

var (
	_ provider.Provider       = (*Provider)(nil)
	_ provider.PlanSearcher   = (*Provider)(nil)
	_ provider.ActivityReader = (*Provider)(nil)
)

// Provider returns whatever its fields hold. Zero value is usable.
type Provider struct {
	BaseURL      string
	User         provider.User
	UserErr      error
	Info         provider.ServerInfo
	Projects     []provider.Project
	Plans        map[string][]provider.Plan     // by project key
	Branches     map[string][]provider.Branch   // by master plan key
	Variables    map[string][]provider.Variable // by plan key
	VariablesErr error
	History      map[string][]provider.Build // by plan key, newest first
	Sequences    map[string][]provider.Build // by build key; GetBuild walks it and repeats the last entry
	BuildVars    map[string]map[string]string
	BuildVarsErr error
	Logs         map[string][]string // by job result key
	LogSkipped   map[string]int      // by job result key: lines before Logs that a long log's read left out
	// LogGrowth is a log that grows: the n-th FetchLog of a key sees the n-th
	// snapshot of the whole log, the last one repeating. It wins over Logs.
	LogGrowth     map[string][][]string // by job result key
	TriggerResult provider.Build
	TriggerErr    error
	// by plan key; TriggerResult and TriggerErr apply when a key is missing
	TriggerResults map[string]provider.Build
	TriggerErrs    map[string]error
	StopErr        error
	StopErrs       []error // returned by StopBuild in order, one per call; after them StopErr applies
	RestartResult  provider.Build
	RestartErr     error
	NoRestart      bool                      // CannotRestart reports it
	NoStop         bool                      // CannotStop reports it
	RestartResults map[string]provider.Build // by build key; RestartResult and RestartErr apply when a key is missing
	RestartErrs    map[string]error
	GetErrs        map[string][]error             // returned by GetBuild for a key in order, one per call; then the build
	LogErrs        []error                        // returned by FetchLog in order, one per call; nil entries succeed
	Progressions   map[string][]provider.Progress // by build key; BuildProgress walks it and repeats the last entry
	ProgressErr    error
	QueueEntries   []provider.QueueEntry
	QueueErr       error
	AgentList      []provider.Agent
	AgentsErr      error
	SetAgentErr    error               // returned by SetAgentEnabled
	AgentSwitched  []string            // "disable NAME" or "enable NAME", per call
	AgentJobMap    map[string][]string // by agent name: the jobs it can run
	AgentJobsErr   error               // returned when AgentJobMap is nil
	AgentJobsCalls int
	AgentJobsAsked [][]string // the agent names each AgentJobs call asked about
	// JobAgentMap is the agent of each job, by job result key; a job left out
	// has none in its log. JobAgentErr is returned when it is nil.
	JobAgentMap   map[string]string
	JobAgentErr   error
	JobAgentCalls []string // the job result keys JobAgent was asked about
	// Recent is what RecentBuilds lists, newest first; RecentErr is returned
	// when it is nil.
	Recent    []provider.Build
	RecentErr error
	// Running is what RunningBuilds lists; RunningErr is returned when it is
	// set. RunningCalls counts the calls.
	Running      provider.RunningBuilds
	RunningErr   error
	RunningCalls int
	// Acts is what Activity lists; ActsErr is returned when it is set, and
	// NoActivity makes Activity answer ErrUnsupported, as a server without the
	// list does. ActsCalls counts the calls.
	Acts       []provider.AgentActivity
	ActsErr    error
	NoActivity bool
	ActsCalls  int
	// DeployStarts is when each deployment began, by result id; DeployCalls
	// lists the ids DeploymentStarted was asked about.
	DeployStarts map[string]time.Time
	DeployCalls  []string
	// NoPlanSearch makes SearchPlans answer ErrUnsupported, as a server
	// without a plan search does. Otherwise it finds, across every project,
	// the plans whose name holds the term, ignoring case.
	NoPlanSearch bool
	// SearchPage, when set, is all SearchPlans answers, saying more may
	// follow, as a server whose search fills its page does.
	SearchPage  []provider.Plan
	SearchCalls []string // the terms SearchPlans was asked for
	// PlanErr, when set, is what GetPlan answers.
	PlanErr   error
	PlanCalls int // how many times GetPlan was called

	mu           sync.Mutex
	Triggered    []provider.TriggerRequest
	Stopped      []string
	Restarted    []string
	getCalls     map[string]int
	listCalls    map[string]int
	progressCall map[string]int
	logCalls     map[string]int
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

func (f *Provider) SearchPlans(_ context.Context, term string) ([]provider.Plan, bool, error) {
	f.mu.Lock()
	f.SearchCalls = append(f.SearchCalls, term)
	f.mu.Unlock()
	if f.NoPlanSearch {
		return nil, false, errs.Bamboof("this Bamboo does not search plans by name").Wrap(errs.ErrUnsupported)
	}
	if f.SearchPage != nil {
		return f.SearchPage, false, nil
	}
	var out []provider.Plan
	for _, plans := range f.Plans {
		for _, p := range plans {
			if strings.Contains(strings.ToLower(p.Name), strings.ToLower(term)) {
				out = append(out, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, true, nil
}

func (f *Provider) GetPlan(_ context.Context, key string) (provider.Plan, error) {
	f.mu.Lock()
	f.PlanCalls++
	f.mu.Unlock()
	if f.PlanErr != nil {
		return provider.Plan{}, f.PlanErr
	}
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
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]provider.Branch(nil), f.Branches[key]...), nil
}

func (f *Provider) ListVariables(_ context.Context, key string) ([]provider.Variable, error) {
	if f.VariablesErr != nil {
		return nil, f.VariablesErr
	}
	return f.Variables[key], nil
}

func (f *Provider) ListBuilds(_ context.Context, key string, o provider.ListOptions) ([]provider.Build, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listCalls == nil {
		f.listCalls = map[string]int{}
	}
	f.listCalls[key]++
	var out []provider.Build
	for _, b := range f.History[key] {
		if o.State != "" && b.State != o.State {
			continue
		}
		if !o.Unfinished && unfinished(b.State) {
			continue
		}
		b.Parent = "" // like Bamboo's result list; GetBuild reports it
		out = append(out, b)
		if o.Limit > 0 && len(out) == o.Limit {
			break
		}
	}
	return out, nil
}

// unfinished is what Bamboo leaves out of a result list unless asked for
// every state: queued and running builds, and builds stopped before they ran.
func unfinished(s provider.State) bool {
	return s == provider.StateQueued || s == provider.StateRunning || s == provider.StateNotBuilt
}

// ListCalls reports how many times ListBuilds was called for key.
func (f *Provider) ListCalls(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls[key]
}

func (f *Provider) GetBuild(_ context.Context, key string) (provider.Build, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getCalls == nil {
		f.getCalls = map[string]int{}
	}
	n := f.getCalls[key]
	f.getCalls[key] = n + 1
	if errs := f.GetErrs[key]; len(errs) > 0 {
		f.GetErrs[key] = errs[1:]
		return provider.Build{}, errs[0]
	}
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

// RestartBuild records the key. With nothing scripted it answers the build
// as queued under the same key, as Bamboo does.
func (f *Provider) RestartBuild(_ context.Context, key string) (provider.Build, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Restarted = append(f.Restarted, key)
	if err, ok := f.RestartErrs[key]; ok {
		return provider.Build{}, err
	}
	if b, ok := f.RestartResults[key]; ok {
		return b, nil
	}
	if f.RestartErr != nil {
		return provider.Build{}, f.RestartErr
	}
	if f.RestartResult.Key != "" {
		return f.RestartResult, nil
	}
	return provider.Build{Key: key, State: provider.StateQueued}, nil
}

func (f *Provider) FetchLog(_ context.Context, key string, o provider.LogOptions) (provider.LogChunk, error) {
	f.mu.Lock()
	if f.logCalls == nil {
		f.logCalls = map[string]int{}
	}
	n := f.logCalls[key]
	f.logCalls[key] = n + 1
	if len(f.LogErrs) > 0 {
		err := f.LogErrs[0]
		f.LogErrs = f.LogErrs[1:]
		if err != nil {
			f.mu.Unlock()
			return provider.LogChunk{}, err
		}
	}
	lines, ok := f.Logs[key]
	if seq := f.LogGrowth[key]; len(seq) > 0 {
		if n >= len(seq) {
			n = len(seq) - 1
		}
		lines, ok = seq[n], true
	}
	f.mu.Unlock()
	if !ok {
		return provider.LogChunk{}, notFound("job", key)
	}
	skipped := f.LogSkipped[key]
	from := min(max(o.Offset-skipped, 0), len(lines))
	return provider.LogChunk{Lines: lines[from:], Next: skipped + len(lines)}, nil
}

// CannotRestart reports NoRestart.
func (f *Provider) CannotRestart(context.Context) bool { return f.NoRestart }

// CannotStop reports NoStop.
func (f *Provider) CannotStop(context.Context) bool { return f.NoStop }

// LogCalls reports how many times FetchLog was called for key.
func (f *Provider) LogCalls(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logCalls[key]
}

func (f *Provider) URL(key string) string { return f.BaseURL + "/browse/" + key }

func (f *Provider) CreateBranchURL(planKey string) string {
	return f.BaseURL + "/chain/admin/newPlanBranch.action?planKey=" + planKey
}

// PlanBranchName stands in for Bamboo's naming rule with its common case:
// a slash becomes "-".
func (f *Provider) PlanBranchName(repositoryBranch string) string {
	return strings.ReplaceAll(repositoryBranch, "/", "-")
}

func (f *Provider) RepositoriesURL(planKey string) string {
	return f.BaseURL + "/branch/admin/config/configureChainBranchRepositories.action?planKey=" + planKey
}

func (f *Provider) Queue(context.Context) ([]provider.QueueEntry, error) {
	return f.QueueEntries, f.QueueErr
}

func (f *Provider) Agents(context.Context) ([]provider.Agent, error) {
	return f.AgentList, f.AgentsErr
}

// SetAgentEnabled records the call and flips the agent in AgentList.
func (f *Provider) SetAgentEnabled(_ context.Context, a provider.Agent, enabled bool) error {
	if f.SetAgentErr != nil {
		return f.SetAgentErr
	}
	verb := "disable "
	if enabled {
		verb = "enable "
	}
	f.AgentSwitched = append(f.AgentSwitched, verb+a.Name)
	for i := range f.AgentList {
		if f.AgentList[i].ID == a.ID {
			f.AgentList[i].Enabled = enabled
		}
	}
	return nil
}

// AgentJobs answers AgentJobMap for the agents asked about, as Bamboo
// answers only the agents it is asked about; an agent the map does not hold
// runs nothing.
func (f *Provider) AgentJobs(_ context.Context, agents []provider.Agent) (map[string][]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AgentJobsCalls++
	asked := make([]string, 0, len(agents))
	for _, a := range agents {
		asked = append(asked, a.Name)
	}
	f.AgentJobsAsked = append(f.AgentJobsAsked, asked)
	if f.AgentJobMap != nil {
		out := map[string][]string{}
		for _, a := range agents {
			out[a.Name] = f.AgentJobMap[a.Name]
		}
		return out, nil
	}
	if f.AgentJobsErr != nil {
		return nil, f.AgentJobsErr
	}
	return nil, errs.Bamboof("no agent jobs").Wrap(errs.ErrUnsupported)
}

// JobAgent says which agent ran a job, from JobAgentMap.
func (f *Provider) JobAgent(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.JobAgentCalls = append(f.JobAgentCalls, key)
	if f.JobAgentMap == nil && f.JobAgentErr != nil {
		return "", f.JobAgentErr
	}
	return f.JobAgentMap[key], nil
}

// RecentBuilds lists the first limit builds of Recent.
func (f *Provider) RecentBuilds(_ context.Context, limit int) ([]provider.Build, error) {
	if f.Recent == nil && f.RecentErr != nil {
		return nil, f.RecentErr
	}
	if limit > 0 && len(f.Recent) > limit {
		return f.Recent[:limit], nil
	}
	return f.Recent, nil
}

// Activity lists Acts.
func (f *Provider) Activity(_ context.Context) ([]provider.AgentActivity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ActsCalls++
	switch {
	case f.NoActivity:
		return nil, errs.Bamboof("no activity list").Wrap(errs.ErrUnsupported)
	case f.ActsErr != nil:
		return nil, f.ActsErr
	}
	return f.Acts, nil
}

// DeploymentStarted answers from DeployStarts.
func (f *Provider) DeploymentStarted(_ context.Context, id string) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DeployCalls = append(f.DeployCalls, id)
	t, ok := f.DeployStarts[id]
	if !ok {
		return time.Time{}, errs.ErrNotFound
	}
	return t, nil
}

// RunningBuilds lists Running.
func (f *Provider) RunningBuilds(_ context.Context) (provider.RunningBuilds, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.RunningCalls++
	if f.RunningErr != nil {
		return provider.RunningBuilds{}, f.RunningErr
	}
	return f.Running, nil
}
