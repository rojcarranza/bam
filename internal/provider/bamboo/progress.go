package bamboo

import (
	"context"
	"net/url"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// BuildProgress reports how far a running build has got. A queued or finished
// build, or one the server has no average for, yields a zero Progress and no
// error. Callers ask only about builds they have just read, so a 404 here
// means the status path is missing, not that the build is.
func (c *Client) BuildProgress(ctx context.Context, key string) (provider.Progress, error) {
	caps := c.fresh(ctx)
	if caps.Progress == "no" {
		return provider.Progress{}, errs.Bamboof("this Bamboo does not report build progress").
			WithTry("watch shows elapsed time only").Wrap(errs.ErrUnsupported)
	}
	var s statusDTO
	if err := c.getJSON(ctx, api+"/result/status/"+url.PathEscape(key), nil, &s); err != nil {
		if isUnsupportedStatus(err) {
			c.learn(ctx, func(cp *Capabilities) { cp.Progress = "no" })
			return provider.Progress{}, errs.Bamboof("this Bamboo does not report build progress").
				WithTry("watch shows elapsed time only").Wrap(errs.ErrUnsupported)
		}
		return provider.Progress{}, err
	}
	if caps.Progress == "" {
		c.learn(ctx, func(cp *Capabilities) { cp.Progress = "yes" })
	}
	return mapProgress(s), nil
}

func mapProgress(s statusDTO) provider.Progress {
	if s.Finished || s.Progress == nil || !s.Progress.IsValid || s.Progress.AverageBuildDuration <= 0 {
		return provider.Progress{}
	}
	p := provider.Progress{
		Valid:   true,
		Average: time.Duration(s.Progress.AverageBuildDuration) * time.Millisecond,
		Elapsed: time.Duration(s.Progress.BuildTime) * time.Millisecond,
		Stage:   s.CurrentStage,
		Source:  "bamboo",
	}
	if p.Elapsed < p.Average {
		p.Remaining = p.Average - p.Elapsed
	}
	pct := s.Progress.PercentageCompleted
	if pct == 0 && p.Average > 0 {
		pct = float64(p.Elapsed) / float64(p.Average)
	}
	p.Percent = min(max(pct, 0), 1)
	return p
}
