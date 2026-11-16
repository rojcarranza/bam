// Package readonly wraps a provider.Provider so that every call that would
// change something in Bamboo is refused before it leaves bam. Reads pass
// through untouched, and so do the optional provider extensions.
package readonly

import (
	"context"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
)

// Writes names the Provider methods that change something in Bamboo. A test
// compares it with the Provider interface, so a new method cannot reach the
// server unclassified: it is either listed here or in Reads.
var Writes = []string{"Trigger", "StopBuild", "RestartBuild"}

// Reads names the Provider methods that only read.
var Reads = []string{"CurrentUser", "ServerInfo", "ListProjects", "ListPlans", "GetPlan", "ListBranches",
	"ListVariables", "ListBuilds", "GetBuild", "BuildProgress", "BuildVariables", "FetchLog", "Queue",
	"Agents", "AgentJobs", "URL", "RepositoriesURL", "CreateBranchURL", "PlanBranchName"}

// Refusal is the error a refused command returns: a usage error, exit code 2.
func Refusal(what string) *errs.Error {
	return errs.Usagef("read-only mode: refused to %s", what).
		WithWhy("bam is set read-only, so it will not change anything in Bamboo").
		WithTry("run without --read-only, and unset BAM_READ_ONLY and read_only in the bam config, to allow it")
}

// Wrap returns p with every write refused.
func Wrap(p provider.Provider) *Guard { return &Guard{Provider: p} }

// Guard is a Provider whose writes are refused. It is exported so a type that
// embeds it keeps the optional extensions below.
type Guard struct{ provider.Provider }

func (g *Guard) Trigger(context.Context, provider.TriggerRequest) (provider.Build, error) {
	return provider.Build{}, Refusal("run a plan")
}

func (g *Guard) StopBuild(context.Context, string) error { return Refusal("stop a build") }

func (g *Guard) RestartBuild(context.Context, string) (provider.Build, error) {
	return provider.Build{}, Refusal("rerun a build")
}

// SetAgentEnabled is the optional provider.AgentSwitcher: it takes an agent
// out of rotation or back in, which changes Bamboo.
func (g *Guard) SetAgentEnabled(context.Context, provider.Agent, bool) error {
	return Refusal("switch an agent")
}

// The optional extensions Service looks for with a type assertion are
// forwarded, so wrapping does not change what bam believes the server can do.

func (g *Guard) SearchPlans(ctx context.Context, term string) ([]provider.Plan, bool, error) {
	if ps, ok := g.Provider.(provider.PlanSearcher); ok {
		return ps.SearchPlans(ctx, term)
	}
	return nil, false, errs.ErrUnsupported
}

func (g *Guard) CannotRestart(ctx context.Context) bool {
	rc, ok := g.Provider.(provider.RestartChecker)
	return ok && rc.CannotRestart(ctx)
}

func (g *Guard) CannotStop(ctx context.Context) bool {
	sc, ok := g.Provider.(provider.StopChecker)
	return ok && sc.CannotStop(ctx)
}

func (g *Guard) CannotListAgents(ctx context.Context) bool {
	ac, ok := g.Provider.(provider.AgentListChecker)
	return ok && ac.CannotListAgents(ctx)
}

// JobAgent and RecentBuilds only read, so they pass through.

func (g *Guard) JobAgent(ctx context.Context, jobResultKey string) (string, error) {
	if r, ok := g.Provider.(provider.JobAgentReader); ok {
		return r.JobAgent(ctx, jobResultKey)
	}
	return "", errs.ErrUnsupported
}

func (g *Guard) RecentBuilds(ctx context.Context, limit int) ([]provider.Build, error) {
	if r, ok := g.Provider.(provider.RecentBuilder); ok {
		return r.RecentBuilds(ctx, limit)
	}
	return nil, errs.ErrUnsupported
}
