// Package app holds bam's use cases. It returns values and emits events; it
// never prints, never reads stdin, and never imports cobra, so the commands
// and the terminal UI share it.
package app

import (
	"context"
	"errors"
	"time"

	"github.com/rojcarranza/bam/internal/config"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
)

// Clock is injected so polling is testable without sleeping.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// SystemClock is the real clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time                         { return time.Now() }
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Service runs use cases against one server.
type Service struct {
	P provider.Provider
	// ReadOnly makes the Service refuse starting, resuming or rerunning a set
	// before it takes a lock or writes a record; P refuses the calls themselves.
	ReadOnly bool
	// Cfg is where the configuration comes from; read it through Config. It
	// reloads when a file changed, so a set or preset added while the UI is open
	// is seen by every use case, not only by the panels.
	Cfg    *config.Source
	Server config.ResolvedServer
	Origin string // credential.Origin(Server.URL)
	Clock  Clock
	State  *StateStore
	Sets   *SetStore
	Trees  *TreeStore // finished trees seen before; nil keeps none
	// AgentJobs keeps the jobs each agent can run between runs; nil keeps
	// them for this process only.
	AgentJobs *AgentJobStore
	// JobAgents keeps which agent ran each finished job between runs; nil
	// reads every log again.
	JobAgents *JobAgentStore
	Getenv    func(string) string
	// Checkout returns the branch checked out in the working directory; nil
	// when bam has no repository to ask. "@" in a branch means this branch.
	Checkout func() (string, error)
	// Head returns the checked-out commit and whether a remote branch has it;
	// nil when bam has no repository to ask.
	Head func() (sha string, pushed bool, err error)

	estimates estimateCache // medians of plan histories, for servers without progress
	scans     scanCache     // the newest tree scan of each plan, for TreeOptions.Shared
	why       whyCache      // agent answers behind provider.Why
	pinTrees  pinTreeCache  // the trees under the board's pins
}

// Config is the current configuration: the files are read again when one
// changed since the last call. When a reload fails, such as a file saved
// halfway through an edit, it is the last good configuration. Nil with no
// source.
func (s *Service) Config() *config.Config {
	if s.Cfg == nil {
		return nil
	}
	cfg, _ := s.Cfg.Current()
	return cfg
}

// configNow is Config for a run about to be sent to Bamboo. It always
// checks the files, even inside the source's interval. When the
// files changed and no longer parse, it returns that error instead of acting on
// the last good copy: a run must not go out with values the person just edited
// away.
func (s *Service) configNow() (*config.Config, error) {
	if s.Cfg == nil {
		return nil, nil
	}
	return s.Cfg.Fresh()
}

// ProjectKeys is the projects that scope navigation on this server
// (config.ProjectKeys), or nil when none is configured. The terminal UI calls
// it so that it never reads the configuration itself.
func (s *Service) ProjectKeys() []string {
	cfg := s.Config()
	if cfg == nil {
		return nil
	}
	return cfg.ProjectKeys(s.Server)
}

// DefaultProject is the project the terminal UI opens on for this server
// (config.DefaultProject), or "" for every project.
func (s *Service) DefaultProject() string {
	cfg := s.Config()
	if cfg == nil {
		return ""
	}
	return cfg.DefaultProject(s.Server)
}

// Build fetches a build. When the last build recorded for this repository no
// longer exists on the server, the record is removed and the error says so.
func (s *Service) Build(ctx context.Context, key string) (provider.Build, error) {
	b, err := s.P.GetBuild(ctx, key)
	if err != nil && errors.Is(err, errs.ErrNotFound) {
		root := s.Config().RepoRoot
		if rec, ok, _ := s.State.Last(root); ok && rec.BuildKey == key && rec.Origin == s.Origin {
			_ = s.State.Forget(root)
			return b, errs.Bamboof("the last build %s no longer exists on %s", key, s.Server.Alias).
				WithWhy("it was removed on the server, so bam forgot it").Wrap(errs.ErrNotFound)
		}
	}
	return b, err
}

// BuildWithQueue is Build with a queued build's place in the server's queue
// added, for the commands that show it. A server that cannot list its queue,
// or a build not in the list, leaves it unset: the position is decoration,
// never a reason to fail a read.
func (s *Service) BuildWithQueue(ctx context.Context, key string) (provider.Build, error) {
	b, err := s.Build(ctx, key)
	if err != nil || b.State != provider.StateQueued {
		return b, err
	}
	if q, qerr := s.P.Queue(ctx); qerr == nil {
		b.QueuePosition, b.QueueLength = queuePlace(q, b.Key)
	}
	b.Why = s.WhyWaits(ctx, b, b.QueuePosition-1)
	return b, nil
}

// queuePlace is key's place in q, counted from 1, and q's length; both 0
// when key is not queued.
func queuePlace(q []provider.QueueEntry, key string) (pos, length int) {
	for i, e := range q {
		if e.Key == key {
			return i + 1, len(q)
		}
	}
	return 0, 0
}

// markQueued sets the queue place of every queued build under root, root
// included, with one read of the queue, and says why each waits. A queue the
// server refuses leaves the place unset, as in BuildWithQueue.
func (s *Service) markQueued(ctx context.Context, root *TreeNode) {
	var queued []*TreeNode
	root.Walk(func(n *TreeNode, _ int) {
		if !n.NotRun && !n.Loop && n.Build.State == provider.StateQueued {
			queued = append(queued, n)
		}
	})
	if len(queued) == 0 {
		return
	}
	q, err := s.P.Queue(ctx)
	if err != nil {
		q = nil
	}
	for _, n := range queued {
		n.Build.QueuePosition, n.Build.QueueLength = queuePlace(q, n.Build.Key)
		n.Build.Why = s.WhyWaits(ctx, n.Build, n.Build.QueuePosition-1)
	}
}

// Queue lists the builds waiting on the server, first in line first.
func (s *Service) Queue(ctx context.Context) ([]provider.QueueEntry, error) {
	return s.P.Queue(ctx)
}

// Agents lists the server's build agents, offline ones included.
func (s *Service) Agents(ctx context.Context) ([]provider.Agent, error) {
	return s.P.Agents(ctx)
}

// Progress returns how far a running build has got: Bamboo's estimate, or,
// when the server reports no progress, one from the plan's recent
// successful builds. A build that is not running, or one with no estimate
// to be had, yields a zero Progress and no error; only a real transport
// failure is returned.
func (s *Service) Progress(ctx context.Context, b provider.Build) (provider.Progress, error) {
	p, err := s.P.BuildProgress(ctx, b.Key)
	if err != nil {
		if errors.Is(err, errs.ErrUnsupported) {
			return s.historyProgress(ctx, b), nil
		}
		return provider.Progress{}, err
	}
	return p, nil
}
