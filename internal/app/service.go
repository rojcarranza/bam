// Package app holds bam's use cases. It returns values and emits events; it
// never prints, never reads stdin, and never imports cobra, so the commands
// and the terminal UI share it.
package app

import (
	"context"
	"errors"
	"time"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
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
	P      provider.Provider
	Cfg    *config.Config
	Server config.ResolvedServer
	Origin string // credential.Origin(Server.URL)
	Clock  Clock
	State  *StateStore
	Sets   *SetStore
	Trees  *TreeStore // finished trees seen before; nil keeps none
	Getenv func(string) string

	estimates estimateCache // medians of plan histories, for servers without progress
	scans     scanCache     // the newest tree scan of each plan, for TreeOptions.Shared
}

// ProjectKeys is the projects that scope navigation on this server
// (config.ProjectKeys), or nil when none is configured. The terminal UI calls
// it so that it never reads the configuration itself.
func (s *Service) ProjectKeys() []string {
	if s.Cfg == nil {
		return nil
	}
	return s.Cfg.ProjectKeys(s.Server)
}

// DefaultProject is the project the terminal UI opens on for this server
// (config.DefaultProject), or "" for every project.
func (s *Service) DefaultProject() string {
	if s.Cfg == nil {
		return ""
	}
	return s.Cfg.DefaultProject(s.Server)
}

// Build fetches a build. When the last build recorded for this repository no
// longer exists on the server, the record is removed and the error says so.
func (s *Service) Build(ctx context.Context, key string) (provider.Build, error) {
	b, err := s.P.GetBuild(ctx, key)
	if err != nil && errors.Is(err, errs.ErrNotFound) {
		if rec, ok, _ := s.State.Last(s.Cfg.RepoRoot); ok && rec.BuildKey == key && rec.Origin == s.Origin {
			_ = s.State.Forget(s.Cfg.RepoRoot)
			return b, errs.Bamboof("the last build %s no longer exists on %s", key, s.Server.Alias).
				WithWhy("it was removed on the server, so bam forgot it").Wrap(errs.ErrNotFound)
		}
	}
	return b, err
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
