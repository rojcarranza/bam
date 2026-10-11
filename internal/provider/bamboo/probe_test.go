package bamboo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProbeHappyPath(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/info":                                                            {fixture: "info.json"},
		"GET /rest/api/latest/plan/PROJ-BUILD/variables":                                       {fixture: "plan_variables.json"},
		"GET /rest/api/latest/result/PROJ-BUILD":                                               {fixture: "results.json"},
		"GET /rest/api/latest/result/PROJ-BUILD-482?expand=variables":                          {fixture: "result_variables.json"},
		"GET /rest/api/latest/result/PROJ-BUILD-482?expand=" + buildExpand:                     {fixture: "result_detail.json"},
		"GET /rest/api/latest/result/PROJ-BUILD-481?expand=testResults.failedTests.testResult": {fixture: "failed_tests.json"},
		"GET /rest/api/latest/result/PROJ-BUILD12-JOB1-44?expand=logEntries":                   {fixture: "log_entries.json"},
		"GET /rest/api/latest/result/status/PROJ-BUILD-482":                                    {fixture: "status_running.json"},
	})
	results := c.Probe(ctx, "PROJ-BUILD")
	byName := map[string]ProbeResult{}
	var names []string
	for _, r := range results {
		byName[r.Name] = r
		names = append(names, r.Name)
	}
	assert.Equal(t, []string{"server version", "plan variables", "build variables", "logs", "duration estimate", "failed tests", "stop builds", "rerun builds"}, names)
	assert.Equal(t, ProbeOK, byName["server version"].Status)
	assert.Equal(t, "9.6.2", byName["server version"].Detail)
	assert.Equal(t, ProbeOK, byName["plan variables"].Status)
	assert.Equal(t, ProbeOK, byName["build variables"].Status)
	assert.Equal(t, ProbeOK, byName["logs"].Status)
	assert.Equal(t, ProbeOK, byName["duration estimate"].Status)
	assert.Equal(t, ProbeOK, byName["failed tests"].Status)
	assert.Equal(t, ProbeSkipped, byName["stop builds"].Status)
	assert.Equal(t, ProbeSkipped, byName["rerun builds"].Status)
}

func TestProbeReportsUnsupportedAndErrors(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/info":              {fixture: "info.json"},
		"GET /rest/api/latest/plan/PROJ-BUILD":   {fixture: "plan.json"},
		"GET /rest/api/latest/result/PROJ-BUILD": {body: `{"results":{"size":0,"result":[]}}`},
	})
	results := c.Probe(ctx, "PROJ-BUILD")
	status := map[string]ProbeStatus{}
	for _, r := range results {
		status[r.Name] = r.Status
	}
	assert.Equal(t, ProbeUnsupported, status["plan variables"])
	assert.Equal(t, ProbeSkipped, status["build variables"], "no builds to read from")
	assert.Equal(t, ProbeSkipped, status["logs"])
	assert.Equal(t, ProbeSkipped, status["failed tests"])
}

func TestProbeReportsBuildListingFailuresAsErrors(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/info":              {fixture: "info.json"},
		"GET /rest/api/latest/plan/PROJ-BUILD":   {fixture: "plan.json"},
		"GET /rest/api/latest/result/PROJ-BUILD": {status: 500, body: `{"message":"boom"}`},
	})
	results := c.Probe(ctx, "PROJ-BUILD")
	status := map[string]ProbeStatus{}
	for _, r := range results {
		status[r.Name] = r.Status
	}
	assert.Equal(t, ProbeError, status["build variables"], "an outage is not a missing capability")
	assert.Equal(t, ProbeError, status["logs"])
	assert.Equal(t, ProbeError, status["failed tests"])
}

func TestProbeReportsAFailedBuildReadAsAnError(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/info":                                        {fixture: "info.json"},
		"GET /rest/api/latest/plan/PROJ-BUILD/variables":                   {fixture: "plan_variables.json"},
		"GET /rest/api/latest/result/PROJ-BUILD":                           {fixture: "results.json"},
		"GET /rest/api/latest/result/PROJ-BUILD-482?expand=variables":      {fixture: "result_variables.json"},
		"GET /rest/api/latest/result/PROJ-BUILD-482?expand=" + buildExpand: {status: 500, body: `{"message":"boom"}`},
	})
	results := c.Probe(ctx, "PROJ-BUILD")
	for _, r := range results {
		if r.Name == "logs" {
			assert.Equal(t, ProbeError, r.Status, "a failed build read is not a build without jobs")
		}
	}
}

func TestProbeReportsDurationEstimate(t *testing.T) {
	base := map[string]*route{
		"GET /rest/api/latest/info":                                                            {fixture: "info.json"},
		"GET /rest/api/latest/plan/PROJ-BUILD/variables":                                       {fixture: "plan_variables.json"},
		"GET /rest/api/latest/result/PROJ-BUILD":                                               {fixture: "results.json"},
		"GET /rest/api/latest/result/PROJ-BUILD-482?expand=variables":                          {fixture: "result_variables.json"},
		"GET /rest/api/latest/result/PROJ-BUILD-482?expand=" + buildExpand:                     {fixture: "result_detail.json"},
		"GET /rest/api/latest/result/PROJ-BUILD-481?expand=testResults.failedTests.testResult": {fixture: "failed_tests.json"},
		"GET /rest/api/latest/result/PROJ-BUILD12-JOB1-44?expand=logEntries":                   {fixture: "log_entries.json"},
	}
	supported := map[string]*route{"GET /rest/api/latest/result/status/PROJ-BUILD-482": {fixture: "status_running.json"}}
	missing := map[string]*route{"GET /rest/api/latest/result/status/PROJ-BUILD-482": {status: 404, body: `{"message":"no"}`}}

	for name, tc := range map[string]struct {
		extra  map[string]*route
		status ProbeStatus
		detail string
	}{
		"supported":   {supported, ProbeOK, "read from PROJ-BUILD-482"},
		"unsupported": {missing, ProbeUnsupported, "watch shows elapsed time only"},
	} {
		t.Run(name, func(t *testing.T) {
			routes := map[string]*route{}
			for k, v := range base {
				routes[k] = v
			}
			for k, v := range tc.extra {
				routes[k] = v
			}
			c, _ := newTestServer(t, routes)

			var got ProbeResult
			var names []string
			for _, r := range c.Probe(ctx, "PROJ-BUILD") {
				names = append(names, r.Name)
				if r.Name == "duration estimate" {
					got = r
				}
			}

			assert.Contains(t, names, "duration estimate")
			assert.Equal(t, tc.status, got.Status)
			assert.Equal(t, tc.detail, got.Detail)
		})
	}
}

func TestProbeSkipsDurationEstimateWithoutBuilds(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/info":              {fixture: "info.json"},
		"GET /rest/api/latest/plan/PROJ-BUILD":   {fixture: "plan.json"},
		"GET /rest/api/latest/result/PROJ-BUILD": {body: `{"results":{"size":0,"result":[]}}`},
	})
	for _, r := range c.Probe(ctx, "PROJ-BUILD") {
		if r.Name == "duration estimate" {
			assert.Equal(t, ProbeSkipped, r.Status)
			return
		}
	}
	t.Fatal("no duration estimate row")
}
