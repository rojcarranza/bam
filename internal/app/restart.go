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
// result, and a Watch started on it would end at once. It is not a trigger,
// so the repository's last build is left alone.
func (s *Service) Restart(ctx context.Context, key string) (provider.Build, error) {
	if err := ctx.Err(); err != nil {
		return provider.Build{}, err
	}
	before, err := s.P.GetBuild(ctx, key)
	if err != nil {
		return provider.Build{}, err
	}
	if _, err := s.P.RestartBuild(ctx, key); err != nil {
		return provider.Build{}, err
	}
	for waited := time.Duration(0); ; waited += restartPoll {
		b, err := s.P.GetBuild(ctx, key)
		if err != nil {
			return provider.Build{}, err
		}
		if !b.State.Finished() || !b.FinishedAt.Equal(before.FinishedAt) {
			return b, nil
		}
		if waited >= restartWait {
			return provider.Build{}, errs.Bamboof("Bamboo did not restart %s", key).
				WithWhy("the build still shows its earlier result").WithTry("bam open " + key)
		}
		select {
		case <-ctx.Done():
			return provider.Build{}, ctx.Err()
		case <-s.Clock.After(restartPoll):
		}
	}
}
