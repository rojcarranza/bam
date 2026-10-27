package bamboo

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

var _ provider.Provider = (*Client)(nil)

// logMax is how many log entries one read asks for. Bamboo 12 ignores the
// logEntries[start:end] range and start-index, and answers max-result with
// the LAST entries; asking for more than the log holds returns all of it,
// whichever end a server counts from. The offset is then applied here.
var logMax = 100000

// customRevisionReason is Bamboo's triggerReason for a build queued with
// customRevision. Any other reason means the server ignored the parameter.
const customRevisionReason = "Custom revision build"

type queueDTO struct {
	PlanKey        string `json:"planKey"`
	BuildNumber    int    `json:"buildNumber"`
	BuildResultKey string `json:"buildResultKey"`
	TriggerReason  string `json:"triggerReason"`
}

// Trigger queues a build. Variables travel in the form body so their values
// never appear in a URL or an access log. When a revision is asked for and
// the answer's trigger reason shows Bamboo ignored it, Trigger returns the
// queued build together with an error wrapping errs.ErrUnsupported, so the
// caller can stop the build.
func (c *Client) Trigger(ctx context.Context, req provider.TriggerRequest) (provider.Build, error) {
	form := url.Values{}
	secret := map[string]bool{}
	for k, v := range req.Variables {
		key := "bamboo.variable." + k
		form.Set(key, v)
		if req.Secret[k] {
			secret[key] = true
		}
	}
	query := url.Values{"executeAllStages": {"true"}}
	if req.Revision != "" {
		query.Set("customRevision", req.Revision)
	}
	body, err := c.do(ctx, request{
		method: http.MethodPost,
		path:   api + "/queue/" + url.PathEscape(req.PlanKey),
		query:  query,
		form:   form,
		secret: secret,
	})
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return provider.Build{}, c.notFoundAs(err, "plan", req.PlanKey, "bam plan list")
		}
		var e *errs.Error
		if errors.As(err, &e) && e.Kind == errs.KindBamboo {
			e.What = "could not start build of " + req.PlanKey
		}
		return provider.Build{}, err
	}
	var q queueDTO
	if err := json.Unmarshal(body, &q); err != nil || q.BuildResultKey == "" {
		return provider.Build{}, errs.Bamboof("unexpected response when starting %s", req.PlanKey).Wrap(err)
	}
	planKey := q.PlanKey
	if planKey == "" {
		planKey = req.PlanKey
	}
	b := provider.Build{Key: q.BuildResultKey, URL: c.URL(q.BuildResultKey), PlanKey: planKey,
		Number: q.BuildNumber, State: provider.StateQueued, Reason: plainReason(q.TriggerReason)}
	if req.Revision != "" && q.TriggerReason != customRevisionReason {
		return b, errs.Bamboof("Bamboo ignored the revision").Wrap(errs.ErrUnsupported)
	}
	return b, nil
}

// StopBuild stops a queued or running build.
func (c *Client) StopBuild(ctx context.Context, key string) error {
	caps := c.fresh(ctx)
	unsupported := errs.Bamboof("stopping builds is not supported on %s", c.base.Host).Wrap(errs.ErrUnsupported)
	if caps.Stop == "no" {
		return unsupported
	}
	err := c.dequeue(ctx, key)
	switch {
	case err == nil:
		if caps.Stop == "" {
			c.learn(ctx, func(cp *Capabilities) { cp.Stop = "yes" })
		}
		return nil
	case statusOf(err) == http.StatusMethodNotAllowed || statusOf(err) == http.StatusNotImplemented:
		c.learn(ctx, func(cp *Capabilities) { cp.Stop = "no" })
		return unsupported
	case statusOf(err) == http.StatusNotFound:
		// Bamboo Data Center takes a JOB result key here, not the
		// plan-level build key: "Plan PROJ-BUILD is not of type
		// ...ImmutableJob". Stop every job of the build instead.
		stopped, jobErr := c.stopJobs(ctx, key)
		if jobErr != nil {
			return jobErr
		}
		if stopped {
			if caps.Stop == "" {
				c.learn(ctx, func(cp *Capabilities) { cp.Stop = "yes" })
			}
			return nil
		}
		return c.notFoundAs(err, "queued or running build", key, "bam build show "+key)
	default:
		return c.notFoundAs(err, "queued or running build", key, "bam build show "+key)
	}
}

func (c *Client) dequeue(ctx context.Context, key string) error {
	_, err := c.do(ctx, request{method: http.MethodDelete, path: api + "/queue/" + url.PathEscape(key)})
	return err
}

// RestartBuild asks Bamboo to run a finished build's failed jobs again, in
// the same build (PUT queue/{key}). Whether the server can is learned the
// first time, like stopping a build.
func (c *Client) RestartBuild(ctx context.Context, key string) (provider.Build, error) {
	caps := c.fresh(ctx)
	unsupported := errs.Bamboof("this Bamboo cannot restart a build over REST").
		WithTry("bam open " + key + ", then rerun it from Bamboo's web UI").Wrap(errs.ErrUnsupported)
	if caps.Rerun == "no" {
		return provider.Build{}, unsupported
	}
	body, err := c.do(ctx, request{
		method: http.MethodPut,
		path:   api + "/queue/" + url.PathEscape(key),
		query:  url.Values{"executeAllStages": {"true"}},
	})
	switch {
	case err == nil:
	case statusOf(err) == http.StatusMethodNotAllowed || statusOf(err) == http.StatusNotImplemented:
		c.learn(ctx, func(cp *Capabilities) { cp.Rerun = "no" })
		return provider.Build{}, unsupported
	case errors.Is(err, errs.ErrNotFound):
		return provider.Build{}, c.notFoundAs(err, "build", key, "bam build show "+key)
	default:
		var e *errs.Error
		if errors.As(err, &e) && e.Kind == errs.KindBamboo {
			e.What = "Bamboo would not restart " + key
		}
		return provider.Build{}, err
	}
	// Any 2xx means Bamboo took the restart; Service.Restart's poll is what
	// confirms it. The answer's shape is not relied on: a body without a
	// build key still names the same build.
	var q queueDTO
	if json.Unmarshal(body, &q) != nil || q.BuildResultKey == "" {
		q = queueDTO{BuildResultKey: key}
	}
	if caps.Rerun == "" {
		c.learn(ctx, func(cp *Capabilities) { cp.Rerun = "yes" })
	}
	planKey := q.PlanKey
	if planKey == "" {
		planKey = key[:strings.LastIndex(key, "-")]
	}
	return provider.Build{Key: q.BuildResultKey, URL: c.URL(q.BuildResultKey), PlanKey: planKey,
		Number: q.BuildNumber, State: provider.StateQueued, Reason: plainReason(q.TriggerReason)}, nil
}

// stopJobs removes every unfinished job of a build from the queue. It
// reports whether at least one job was stopped.
func (c *Client) stopJobs(ctx context.Context, key string) (bool, error) {
	var r resultDTO
	if err := c.getJSON(ctx, api+"/result/"+url.PathEscape(key), url.Values{"expand": {buildExpand}}, &r); err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return false, nil // let the caller report the original 404
		}
		return false, c.notFoundAs(err, "build", key, "")
	}
	stopped := false
	for _, stage := range r.Stages.Stage {
		for _, job := range stage.Results.Result {
			switch jobState(job) {
			case provider.StateQueued, provider.StateRunning:
			default:
				continue
			}
			if err := c.dequeue(ctx, resultKey(job)); err != nil {
				if statusOf(err) == http.StatusNotFound {
					continue // the job finished in the meantime
				}
				return stopped, c.notFoundAs(err, "job", resultKey(job), "")
			}
			stopped = true
		}
	}
	return stopped, nil
}

// FetchLog returns log lines of a job result from o.Offset on.
func (c *Client) FetchLog(ctx context.Context, jobKey string, o provider.LogOptions) (provider.LogChunk, error) {
	caps := c.fresh(ctx)
	if caps.Log != "download" {
		chunk, err := c.logEntries(ctx, jobKey, o.Offset)
		if err == nil {
			if caps.Log == "" {
				c.learn(ctx, func(cp *Capabilities) { cp.Log = "entries" })
			}
			return chunk, nil
		}
		if !isUnsupportedStatus(err) && !errors.Is(err, errs.ErrUnsupported) {
			return provider.LogChunk{}, err
		}
	}
	chunk, err := c.logDownload(ctx, jobKey, o.Offset)
	if err != nil {
		return provider.LogChunk{}, c.notFoundAs(err, "job", jobKey, "")
	}
	if caps.Log == "" {
		c.learn(ctx, func(cp *Capabilities) { cp.Log = "download" })
	}
	return chunk, nil
}

func (c *Client) logEntries(ctx context.Context, jobKey string, offset int) (provider.LogChunk, error) {
	lines, size, err := c.readLogEntries(ctx, jobKey, logMax)
	if err != nil {
		return provider.LogChunk{}, err
	}
	// base is the line number of the first entry read. A log longer than one
	// read comes back as one end of it; one entry more tells which end, so
	// offsets stay right and a follow neither repeats nor stalls. The lines
	// before a last-N page cannot be read from the entries. The download is
	// not used here: it counts agent lines the entries leave out, so an
	// offset from one is wrong in the other.
	base := 0
	if size > len(lines) && len(lines) > 0 {
		last, _, err := c.readLogEntries(ctx, jobKey, 1)
		if err != nil {
			return provider.LogChunk{}, err
		}
		if len(last) == 1 && last[0] == lines[len(lines)-1] {
			base = size - len(lines)
		}
	}
	from := min(max(offset, base), base+len(lines)) - base
	return provider.LogChunk{Lines: lines[from:], Next: base + len(lines)}, nil
}

// readLogEntries reads up to n log entries of a job result, and how many
// the log holds.
func (c *Client) readLogEntries(ctx context.Context, jobKey string, n int) ([]string, int, error) {
	var r struct {
		LogEntries *envelope `json:"logEntries"`
	}
	q := url.Values{"expand": {"logEntries"}, "max-result": {strconv.Itoa(n)}}
	if err := c.getJSON(ctx, api+"/result/"+url.PathEscape(jobKey), q, &r); err != nil {
		return nil, 0, err
	}
	if r.LogEntries == nil {
		return nil, 0, errs.ErrUnsupported
	}
	var entries []struct {
		Log      string `json:"log"`
		Unstyled string `json:"unstyledLog"`
	}
	if len(r.LogEntries.Items) > 0 {
		if err := json.Unmarshal(r.LogEntries.Items, &entries); err != nil {
			return nil, 0, err
		}
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		line := e.Unstyled
		if line == "" {
			line = html.UnescapeString(e.Log)
		}
		lines = append(lines, line)
	}
	return lines, r.LogEntries.Size, nil
}

// logDownload reads the raw log artifact. Its lines look like
// "type<TAB>date<TAB>message"; only the message is kept.
func (c *Client) logDownload(ctx context.Context, jobResultKey string, offset int) (provider.LogChunk, error) {
	i := strings.LastIndex(jobResultKey, "-")
	if i <= 0 {
		return provider.LogChunk{}, errs.Usagef("%q is not a job result key", jobResultKey)
	}
	jobKey := jobResultKey[:i]
	body, err := c.do(ctx, request{method: http.MethodGet, path: "/download/" + jobKey + "/build_logs/" + jobResultKey + ".log", raw: true})
	if err != nil {
		return provider.LogChunk{}, err
	}
	all := strings.Split(strings.TrimRight(string(body), "\r\n"), "\n")
	if len(all) == 1 && all[0] == "" {
		all = nil
	}
	for n, l := range all {
		l = strings.TrimRight(l, "\r")
		if parts := strings.SplitN(l, "\t", 3); len(parts) == 3 {
			l = parts[2]
		}
		all[n] = l
	}
	if offset > len(all) {
		offset = len(all)
	}
	return provider.LogChunk{Lines: all[offset:], Next: len(all)}, nil
}
