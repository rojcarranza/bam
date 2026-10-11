package bamboo

import (
	"errors"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func countPath(rec *recorded, path string) int {
	n := 0
	for _, r := range rec.all() {
		if r.URL.Path == path {
			n++
		}
	}
	return n
}

func TestListVariablesPluralPath(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"GET /rest/api/latest/plan/PROJ-BUILD/variables": {fixture: "plan_variables.json"}})
	saved := knownCaps(c, Capabilities{})
	vars, err := c.ListVariables(ctx, "PROJ-BUILD")
	require.NoError(t, err)
	assert.Equal(t, []provider.Variable{
		{Name: "cluster_type", Value: "k8s"},
		{Name: "compute_nodes", Value: "2"},
		{Name: "db_password", Value: MaskedValue, Masked: true},
	}, vars)
	require.NotEmpty(t, *saved)
	assert.Equal(t, "variables", (*saved)[len(*saved)-1].PlanVars)
}

func TestListVariablesFallsBackToSingularAndRemembers(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{"GET /rest/api/latest/plan/PROJ-BUILD/variable": {fixture: "plan_variables.json"}})
	saved := knownCaps(c, Capabilities{})
	_, err := c.ListVariables(ctx, "PROJ-BUILD")
	require.NoError(t, err)
	assert.Equal(t, "variable", (*saved)[len(*saved)-1].PlanVars)

	_, err = c.ListVariables(ctx, "PROJ-BUILD")
	require.NoError(t, err)
	assert.Equal(t, 1, countPath(rec, "/rest/api/latest/plan/PROJ-BUILD/variables"), "plural path not retried once learned")
}

func TestListVariablesUnsupported(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{"GET /rest/api/latest/plan/PROJ-BUILD": {fixture: "plan.json"}})
	saved := knownCaps(c, Capabilities{})
	_, err := c.ListVariables(ctx, "PROJ-BUILD")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Equal(t, "none", (*saved)[len(*saved)-1].PlanVars)

	before := len(rec.all())
	_, err = c.ListVariables(ctx, "PROJ-BUILD")
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Equal(t, before, len(rec.all()), "no requests once known unsupported")
}

func TestListVariablesMissingPlan(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{})
	knownCaps(c, Capabilities{})
	_, err := c.ListVariables(ctx, "PROJ-NOPE")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan PROJ-NOPE not found")
	assert.False(t, errors.Is(err, errs.ErrUnsupported))
}

func TestBuildVariables(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"GET /rest/api/latest/result/PROJ-BUILD-482?expand=variables": {fixture: "result_variables.json"}})
	saved := knownCaps(c, Capabilities{})
	vars, err := c.BuildVariables(ctx, "PROJ-BUILD-482")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"cluster_type": "dcos", "cluster_name": "beta"}, vars)
	assert.Equal(t, "yes", (*saved)[len(*saved)-1].BuildVars)
}

func TestBuildVariablesUnsupported(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{"GET /rest/api/latest/result/PROJ-BUILD-482": {body: `{"key":"PROJ-BUILD-482"}`}})
	saved := knownCaps(c, Capabilities{})
	_, err := c.BuildVariables(ctx, "PROJ-BUILD-482")
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Equal(t, "no", (*saved)[len(*saved)-1].BuildVars)
	before := len(rec.all())
	_, _ = c.BuildVariables(ctx, "PROJ-BUILD-482")
	assert.Equal(t, before, len(rec.all()))
}

func TestGetBuildAddsFailedTestsOnlyWhenFailed(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/PROJ-BUILD12-44?expand=" + buildExpand:                     {fixture: "result_detail.json"},
		"GET /rest/api/latest/result/PROJ-BUILD12-44?expand=testResults.failedTests.testResult": {fixture: "failed_tests.json"},
		"GET /rest/api/latest/result/PROJ-BUILD-482":                                            {body: `{"buildResultKey":"PROJ-BUILD-482","buildNumber":482,"state":"Successful","lifeCycleState":"Finished"}`},
	})
	knownCaps(c, Capabilities{})
	b, err := c.GetBuild(ctx, "PROJ-BUILD12-44")
	require.NoError(t, err)
	assert.Equal(t, []string{"app.AuthTest.TestLogin", "app.AuthTest.TestRefresh"}, b.FailedTests)

	_, err = c.GetBuild(ctx, "PROJ-BUILD-482")
	require.NoError(t, err)
	assert.Equal(t, 1, countPath(rec, "/rest/api/latest/result/PROJ-BUILD-482"))
}

func TestDecodeVarListShapes(t *testing.T) {
	for _, body := range []string{
		`[{"name":"a","value":"1"}]`,
		`{"variables":[{"name":"a","value":"1"}]}`,
		`{"variables":{"size":1,"variable":[{"key":"a","value":"1"}]}}`,
	} {
		got, ok := decodeVariables([]byte(body))
		require.True(t, ok, body)
		require.Len(t, got, 1, body)
		assert.Equal(t, "a", got[0].name(), body)
	}
	_, ok := decodeVariables([]byte(`{"key":"X"}`))
	assert.False(t, ok)
}

// failedTestRoutes is a failed build whose result carries no testResults,
// as on Bamboo 12; its failed job PROJ-BUILD12-INT-44 answers with job.
func failedTestRoutes(job *route) map[string]*route {
	return map[string]*route{
		"GET /rest/api/latest/result/PROJ-BUILD12-44?expand=" + buildExpand:                         {fixture: "result_detail.json"},
		"GET /rest/api/latest/result/PROJ-BUILD12-44?expand=testResults.failedTests.testResult":     {body: `{"key":"PROJ-BUILD12-44","buildResultKey":"PROJ-BUILD12-44"}`},
		"GET /rest/api/latest/result/PROJ-BUILD12-INT-44?expand=testResults.failedTests.testResult": job,
	}
}

func TestGetBuildReadsFailedTestsFromFailedJobsWhenTheBuildHasNone(t *testing.T) {
	c, rec := newTestServer(t, failedTestRoutes(&route{fixture: "job_failed_tests.json"}))
	saved := knownCaps(c, Capabilities{})
	b, err := c.GetBuild(ctx, "PROJ-BUILD12-44")
	require.NoError(t, err)
	assert.Equal(t, []string{"app.ApiTest.parses_config", "app.ApiTest.talks_to_bamboo"}, b.FailedTests)
	assert.Equal(t, "jobs", (*saved)[len(*saved)-1].FailedTestsFrom)
	assert.Zero(t, countPath(rec, "/rest/api/latest/result/PROJ-BUILD12-UNIT-44"), "a job that passed is not asked")
}

func TestFailedTestsLearnsNoOnlyWhenTheJobsHaveNoneEither(t *testing.T) {
	c, _ := newTestServer(t, failedTestRoutes(&route{body: `{"key":"PROJ-BUILD12-INT-44"}`}))
	saved := knownCaps(c, Capabilities{})
	b, err := c.GetBuild(ctx, "PROJ-BUILD12-44")
	require.NoError(t, err)
	assert.Empty(t, b.FailedTests)
	assert.Equal(t, "no", (*saved)[len(*saved)-1].FailedTestsFrom)
}

func TestFailedTestsDoesNotLearnFromAJobThatCannotBeRead(t *testing.T) {
	c, _ := newTestServer(t, failedTestRoutes(&route{status: 500, body: `{"message":"boom"}`}))
	saved := knownCaps(c, Capabilities{})
	_, err := c.GetBuild(ctx, "PROJ-BUILD12-44")
	require.NoError(t, err)
	assert.Empty(t, *saved, "an outage is not a missing capability")
}

func TestFailedTestsOnceLearnedPerJobSkipsTheBuildRequest(t *testing.T) {
	c, rec := newTestServer(t, failedTestRoutes(&route{fixture: "job_failed_tests.json"}))
	knownCaps(c, Capabilities{FailedTestsFrom: "jobs"})
	b, err := c.GetBuild(ctx, "PROJ-BUILD12-44")
	require.NoError(t, err)
	assert.Len(t, b.FailedTests, 2)
	for _, r := range rec.all() {
		if r.URL.Path == "/rest/api/latest/result/PROJ-BUILD12-44" {
			assert.NotContains(t, r.URL.Query().Get("expand"), "testResults")
		}
	}
}
