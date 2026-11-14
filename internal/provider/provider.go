package provider

import "context"

// Provider is everything bam needs from a CI server. Optional operations
// return an error wrapping errs.ErrUnsupported when the server cannot do them.
type Provider interface {
	CurrentUser(ctx context.Context) (User, error)
	ServerInfo(ctx context.Context) (ServerInfo, error)
	ListProjects(ctx context.Context) ([]Project, error)
	ListPlans(ctx context.Context, project string) ([]Plan, error)
	GetPlan(ctx context.Context, planKey string) (Plan, error)
	ListBranches(ctx context.Context, planKey string) ([]Branch, error)
	ListVariables(ctx context.Context, planKey string) ([]Variable, error)
	ListBuilds(ctx context.Context, planKey string, o ListOptions) ([]Build, error)
	GetBuild(ctx context.Context, buildKey string) (Build, error)
	BuildProgress(ctx context.Context, buildKey string) (Progress, error)
	BuildVariables(ctx context.Context, buildKey string) (map[string]string, error)
	Trigger(ctx context.Context, req TriggerRequest) (Build, error)
	StopBuild(ctx context.Context, buildKey string) error
	// RestartBuild asks Bamboo to run a finished build's failed jobs again,
	// in the same build. It returns the queued build.
	RestartBuild(ctx context.Context, buildKey string) (Build, error)
	FetchLog(ctx context.Context, jobResultKey string, o LogOptions) (LogChunk, error)
	// Queue lists the builds waiting for an agent, first in line first.
	Queue(ctx context.Context) ([]QueueEntry, error)
	// Agents lists the server's build agents.
	Agents(ctx context.Context) ([]Agent, error)
	// AgentJobs says, for each of agents by name, the jobs (plan keys such
	// as PROJ-PLAN-JOB1) its capabilities let it run, disabled and offline
	// agents included. agents is the list Agents answered, or the part of it
	// the caller has no recent answer for; an agent left out of the answer
	// no longer exists. It needs an administrator; a refusal wraps
	// errs.ErrUnsupported.
	AgentJobs(ctx context.Context, agents []Agent) (map[string][]string, error)
	URL(key string) string
	// RepositoriesURL is the page where a person sets which branch each
	// repository of the plan (branch) builds.
	RepositoriesURL(planKey string) string
	// CreateBranchURL is the page where a person creates a plan branch of the
	// plan by hand.
	CreateBranchURL(planKey string) string
}

// RestartChecker is an optional Provider extension. CannotRestart reports
// true when the server is already known to refuse RestartBuild, so callers
// can leave out hints that would only fail.
type RestartChecker interface {
	CannotRestart(ctx context.Context) bool
}

// AgentListChecker is an optional Provider extension. CannotListAgents
// reports true when the server is already known to refuse the agent list,
// so a caller does not ask again only to be refused.
type AgentListChecker interface {
	CannotListAgents(ctx context.Context) bool
}

// AgentSwitcher is an optional Provider extension: it takes an agent out of
// rotation or puts it back. SetAgentEnabled needs an administrator; a refusal
// is an auth error, and a server without the call answers an error wrapping
// errs.ErrUnsupported. A busy agent finishes its build first.
type AgentSwitcher interface {
	SetAgentEnabled(ctx context.Context, agent Agent, enabled bool) error
}

// PlanSearcher is an optional Provider extension: it finds plans by name on
// the server, across every project. SearchPlans answers an error wrapping
// errs.ErrUnsupported when the server has no plan search, and the caller
// reads project plan lists instead. Each Plan carries Key, Name and
// ProjectKey; LastBuild is not filled. complete is false when the server
// says more plans may match than it answered.
type PlanSearcher interface {
	SearchPlans(ctx context.Context, term string) (plans []Plan, complete bool, err error)
}

// StopChecker is RestartChecker for StopBuild.
type StopChecker interface {
	CannotStop(ctx context.Context) bool
}
