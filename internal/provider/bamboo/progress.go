package bamboo

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// BuildProgress reports how far a running build has got. A queued or finished
// build, or one the server has no average for, yields a zero Progress and no
// error. Bamboo answers 404 "not building" for a result it is not building
// at that moment, so only another 404, before the endpoint has ever
// answered, means the status path is missing.
func (c *Client) BuildProgress(ctx context.Context, key string) (provider.Progress, error) {
	caps := c.fresh(ctx)
	if caps.Progress == "no" {
		return provider.Progress{}, errs.Bamboof("this Bamboo does not report build progress").
			WithTry("watch shows elapsed time only").Wrap(errs.ErrUnsupported)
	}
	var s statusDTO
	if err := c.getJSON(ctx, api+"/result/status/"+url.PathEscape(key), nil, &s); err != nil {
		if notBuilding(err) || caps.Progress == "yes" && statusOf(err) == http.StatusNotFound {
			if caps.Progress == "" {
				c.learn(ctx, func(cp *Capabilities) { cp.Progress = "yes" })
			}
			return provider.Progress{}, nil
		}
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

// notBuilding reports Bamboo's 404 for a result it is not building now.
func notBuilding(err error) bool {
	var h *httpError
	return errors.As(err, &h) && h.Status == http.StatusNotFound && strings.HasSuffix(h.Message, "not building.")
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
