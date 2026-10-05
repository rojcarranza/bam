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
	URL(key string) string
}
