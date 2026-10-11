package bamboo

import (
	"context"
	"errors"
	"fmt"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

type ProbeStatus string

const (
	ProbeOK          ProbeStatus = "ok"
	ProbeUnsupported ProbeStatus = "unsupported"
	ProbeSkipped     ProbeStatus = "skipped"
	ProbeError       ProbeStatus = "error"
)

// ProbeResult is one line of bam doctor's capability report.
type ProbeResult struct {
	Name   string
	Status ProbeStatus
	Detail string
	Err    error
}

// Probe re-checks every optional capability against planKey and its latest
// builds, and rewrites the cache. Build stop and restart are never probed:
// they have side effects, so they are learned the first time bam build
// cancel and bam set rerun --failed run.
func (c *Client) Probe(ctx context.Context, planKey string) []ProbeResult {
	c.mu.Lock()
	stop, rerun := c.caps.Stop, c.caps.Rerun
	c.caps = Capabilities{Stop: stop, Rerun: rerun}
	c.versionChecked = true
	c.mu.Unlock()

	var out []ProbeResult
	add := func(name string, err error, okDetail, unsupportedDetail string) {
		switch {
		case err == nil:
			out = append(out, ProbeResult{Name: name, Status: ProbeOK, Detail: okDetail})
		case errors.Is(err, errs.ErrUnsupported):
			out = append(out, ProbeResult{Name: name, Status: ProbeUnsupported, Detail: unsupportedDetail})
		default:
			out = append(out, ProbeResult{Name: name, Status: ProbeError, Detail: err.Error(), Err: err})
		}
	}
	skip := func(name, why string) { out = append(out, ProbeResult{Name: name, Status: ProbeSkipped, Detail: why}) }

	info, err := c.ServerInfo(ctx)
	add("server version", err, info.Version, "")

	vars, err := c.ListVariables(ctx, planKey)
	add("plan variables", err, fmt.Sprintf("%d declared on %s", len(vars), planKey),
		"names come from past builds and target defaults")

	builds, err := c.ListBuilds(ctx, planKey, provider.ListOptions{Limit: 1})
	switch {
	case err != nil:
		// An outage or a permission error is a failed diagnosis, not a
		// capability the server lacks; doctor must exit nonzero.
		add("build variables", err, "", "")
		add("logs", err, "", "")
		add("duration estimate", err, "", "")
	case len(builds) == 0:
		why := planKey + " has no builds"
		skip("build variables", why)
		skip("logs", why)
		skip("duration estimate", why)
	default:
		latest := builds[0].Key
		_, err = c.BuildVariables(ctx, latest)
		add("build variables", err, "read from "+latest, "--from works only for builds bam triggered")

		b, err := c.GetBuild(ctx, latest)
		var job string
		for _, s := range b.Stages {
			if len(s.Jobs) > 0 {
				job = s.Jobs[0].Key
				break
			}
		}
		switch {
		case err != nil:
			add("logs", err, "", "")
		case job == "":
			skip("logs", latest+" has no jobs")
		default:
			_, err = c.FetchLog(ctx, job, provider.LogOptions{})
			add("logs", err, "via "+c.Capabilities().Log, "")
		}

		_, err = c.BuildProgress(ctx, latest)
		add("duration estimate", err, "read from "+latest, "watch shows elapsed time only")
	}

	failed, err := c.ListBuilds(ctx, planKey, provider.ListOptions{Limit: 1, State: provider.StateFailed})
	switch {
	case err != nil:
		add("failed tests", err, "", "")
	case len(failed) == 0:
		skip("failed tests", "no failed build of "+planKey+" to read")
	default:
		// GetBuild reads the failed tests, and gives the jobs to ask when
		// the build's own result carries none.
		if _, err := c.GetBuild(ctx, failed[0].Key); err != nil {
			add("failed tests", err, "", "")
			break
		}
		switch c.Capabilities().FailedTestsFrom {
		case "build":
			add("failed tests", nil, "read from "+failed[0].Key, "")
		case "jobs":
			add("failed tests", nil, "read from "+failed[0].Key+" (per job)", "")
		case "no":
			add("failed tests", errs.ErrUnsupported, "", "build show lists failed jobs only")
		default:
			skip("failed tests", "could not read "+failed[0].Key)
		}
	}

	switch stop {
	case "yes":
		add("stop builds", nil, "confirmed by an earlier cancel", "")
	case "no":
		add("stop builds", errs.ErrUnsupported, "", "bam build cancel is unavailable")
	default:
		skip("stop builds", "learned the first time bam build cancel runs")
	}
	switch rerun {
	case "yes":
		add("rerun builds", nil, "confirmed by an earlier rerun", "")
	case "no":
		add("rerun builds", errs.ErrUnsupported, "", "bam set rerun --failed is unavailable")
	default:
		skip("rerun builds", "learned the first time bam set rerun --failed runs")
	}
	return out
}
