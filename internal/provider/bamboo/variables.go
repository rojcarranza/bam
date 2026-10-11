package bamboo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// MaskedValue is what Bamboo returns in place of a secret variable's value.
const MaskedValue = "********"

type varDTO struct {
	Name  string `json:"name"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (v varDTO) name() string {
	if v.Name != "" {
		return v.Name
	}
	return v.Key
}

// decodeVariables accepts a bare list, or an object holding the list under
// "variables" or "variable", bare or inside a collection envelope.
func decodeVariables(body []byte) ([]varDTO, bool) {
	if items, ok := decodeVarList(body); ok {
		return items, true
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil {
		return nil, false
	}
	for _, k := range []string{"variables", "variable"} {
		if raw, ok := obj[k]; ok {
			if items, ok := decodeVarList(raw); ok {
				return items, true
			}
		}
	}
	return nil, false
}

func decodeVarList(raw json.RawMessage) ([]varDTO, bool) {
	var items []varDTO
	if len(raw) > 0 && raw[0] == '[' && json.Unmarshal(raw, &items) == nil {
		return items, true
	}
	var env envelope
	if len(raw) > 0 && raw[0] == '{' && json.Unmarshal(raw, &env) == nil && env.Items != nil {
		if json.Unmarshal(env.Items, &items) == nil {
			return items, true
		}
	}
	return nil, false
}

// ListVariables returns a plan's declared variables. Bamboo versions differ in
// the path; the working one is learned and cached.
func (c *Client) ListVariables(ctx context.Context, key string) ([]provider.Variable, error) {
	caps := c.fresh(ctx)
	unsupported := errs.Bamboof("plan variables cannot be listed on %s", c.base.Host).Wrap(errs.ErrUnsupported)
	var suffixes []string
	switch caps.PlanVars {
	case "none":
		return nil, unsupported
	case "variables", "variable":
		suffixes = []string{caps.PlanVars}
	default:
		suffixes = []string{"variables", "variable"}
	}
	for _, s := range suffixes {
		body, err := c.do(ctx, request{method: http.MethodGet, path: api + "/plan/" + url.PathEscape(key) + "/" + s})
		if err != nil {
			if isUnsupportedStatus(err) {
				continue
			}
			return nil, err
		}
		items, ok := decodeVariables(body)
		if !ok {
			continue
		}
		suffix := s
		c.learn(ctx, func(cp *Capabilities) { cp.PlanVars = suffix })
		out := make([]provider.Variable, 0, len(items))
		for _, v := range items {
			out = append(out, provider.Variable{Name: v.name(), Value: v.Value, Masked: v.Value == MaskedValue})
		}
		return out, nil
	}
	// Both paths failed: tell a missing plan apart from a missing feature.
	if _, err := c.GetPlan(ctx, key); err != nil {
		return nil, err
	}
	c.learn(ctx, func(cp *Capabilities) { cp.PlanVars = "none" })
	return nil, unsupported
}

// BuildVariables returns the variables a finished build ran with.
func (c *Client) BuildVariables(ctx context.Context, key string) (map[string]string, error) {
	caps := c.fresh(ctx)
	unsupported := errs.Bamboof("build variables cannot be read back on %s", c.base.Host).Wrap(errs.ErrUnsupported)
	if caps.BuildVars == "no" {
		return nil, unsupported
	}
	var obj map[string]json.RawMessage
	if err := c.getJSON(ctx, api+"/result/"+url.PathEscape(key), url.Values{"expand": {"variables"}}, &obj); err != nil {
		return nil, c.notFoundAs(err, "build", key, "")
	}
	raw, present := obj["variables"]
	items, ok := decodeVarList(raw)
	if !present || !ok {
		c.learn(ctx, func(cp *Capabilities) { cp.BuildVars = "no" })
		return nil, unsupported
	}
	if caps.BuildVars == "" {
		c.learn(ctx, func(cp *Capabilities) { cp.BuildVars = "yes" })
	}
	out := make(map[string]string, len(items))
	for _, v := range items {
		out[v.name()] = v.Value
	}
	return out, nil
}

type testDTO struct {
	TestCaseName string `json:"testCaseName"`
	ClassName    string `json:"className"`
	MethodName   string `json:"methodName"`
}

// failedTests returns the failed test names of a failed build, or nil when
// unavailable. The build's result is asked first; Bamboo 12 leaves tests off
// it and reports them on each job's result, so then the failed jobs are
// asked, in stage order. Where the tests were found is learned; "no" only
// when the jobs carry none either.
func (c *Client) failedTests(ctx context.Context, b provider.Build) []string {
	caps := c.fresh(ctx)
	switch caps.FailedTestsFrom {
	case "no":
		return nil
	case "jobs":
		names, _, _ := c.jobFailedTests(ctx, b)
		return names
	}
	names, has, err := c.readFailedTests(ctx, b.Key)
	if err != nil {
		return nil
	}
	if has {
		if caps.FailedTestsFrom == "" {
			c.learn(ctx, func(cp *Capabilities) { cp.FailedTestsFrom = "build" })
		}
		return names
	}
	names, has, readAll := c.jobFailedTests(ctx, b)
	switch {
	case has:
		c.learn(ctx, func(cp *Capabilities) { cp.FailedTestsFrom = "jobs" })
	case readAll && len(b.FailedJobs()) > 0:
		c.learn(ctx, func(cp *Capabilities) { cp.FailedTestsFrom = "no" })
	}
	return names
}

// jobFailedTests reads the failed tests of each failed job of b. has reports
// whether any job result carried testResults; readAll whether every job
// could be read.
func (c *Client) jobFailedTests(ctx context.Context, b provider.Build) (names []string, has, readAll bool) {
	readAll = true
	for _, j := range b.FailedJobs() {
		got, ok, err := c.readFailedTests(ctx, j.Key)
		if err != nil {
			readAll = false
			continue
		}
		has = has || ok
		names = append(names, got...)
	}
	return names, has, readAll
}

// readFailedTests reads the failed tests of one build or job result. has
// reports whether the result carried testResults at all.
func (c *Client) readFailedTests(ctx context.Context, key string) (names []string, has bool, err error) {
	var r struct {
		TestResults *struct {
			FailedTests *struct {
				TestResult []testDTO `json:"testResult"`
			} `json:"failedTests"`
		} `json:"testResults"`
	}
	if err := c.getJSON(ctx, api+"/result/"+url.PathEscape(key), url.Values{"expand": {"testResults.failedTests.testResult"}}, &r); err != nil {
		return nil, false, err
	}
	if r.TestResults == nil {
		return nil, false, nil
	}
	if r.TestResults.FailedTests != nil {
		for _, t := range r.TestResults.FailedTests.TestResult {
			if t.ClassName != "" && t.MethodName != "" {
				names = append(names, t.ClassName+"."+t.MethodName)
			} else {
				names = append(names, t.TestCaseName)
			}
		}
	}
	return names, true, nil
}
