package app

import (
	"context"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// restartPoll and restartWait bound how long Restart waits for Bamboo to
// requeue a restarted build.
const (
	restartPoll = 2 * time.Second
	restartWait = 2 * time.Minute
)

// Restart asks Bamboo to run a finished build's failed jobs again and waits
// until Bamboo has requeued it: until then the build still reads as its old
// result, and a Watch started on it would end at once. A build that is still
// running, or that succeeded, is not restarted (Bamboo would refuse, and
// there is nothing to retry): restarted is false and the caller just watches
// it. It is not a trigger, so the repository's last build is left alone.
func (s *Service) Restart(ctx context.Context, key string) (b provider.Build, restarted bool, err error) {
	if err := ctx.Err(); err != nil {
		return provider.Build{}, false, err
	}
	before, err := s.P.GetBuild(ctx, key)
	if err != nil {
		return provider.Build{}, false, err
	}
	if !before.State.Finished() || before.State == provider.StateSuccess {
		return before, false, nil
	}
	if _, err := s.P.RestartBuild(ctx, key); err != nil {
		return provider.Build{}, false, err
	}
	for waited := time.Duration(0); ; waited += restartPoll {
		b, err := s.P.GetBuild(ctx, key)
		if err != nil {
			return provider.Build{}, false, err
		}
		if !b.State.Finished() || !b.FinishedAt.Equal(before.FinishedAt) {
			return b, true, nil
		}
		if waited >= restartWait {
			return provider.Build{}, false, errs.Bamboof("Bamboo did not restart %s", key).
				WithWhy("the build still shows its earlier result").WithTry("bam open " + key)
		}
		select {
		case <-ctx.Done():
			return provider.Build{}, false, ctx.Err()
		case <-s.Clock.After(restartPoll):
		}
	}
}

// CannotRestart reports whether the provider is known to refuse restarts, so
// hints that suggest a rerun would only fail.
func (s *Service) CannotRestart(ctx context.Context) bool {
	rc, ok := s.P.(provider.RestartChecker)
	return ok && rc.CannotRestart(ctx)
}

// CannotStop reports whether the provider is known to refuse stopping builds.
func (s *Service) CannotStop(ctx context.Context) bool {
	sc, ok := s.P.(provider.StopChecker)
	return ok && sc.CannotStop(ctx)
}
