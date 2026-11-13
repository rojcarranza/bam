package view

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/require"
)

var statsFrom = time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)

func fullStats() app.PlanStats {
	return app.PlanStats{PlanKey: "PROJ-DEPLOY", Builds: 20, First: 381, Last: 400,
		From: statsFrom, To: statsFrom.Add(6 * 24 * time.Hour),
		Passed: 16, Failed: 4, Other: map[provider.State]int{provider.StateStopped: 1},
		Durations: &app.DurationStats{Median: 250 * time.Second, P90: 362 * time.Second, Max: 580 * time.Second, MaxBuild: 388, Of: 16},
		Streak:    &app.Streak{State: provider.StateSuccess, Count: 3},
		FailingJobs: []app.JobFailures{{Name: "Integration tests", Failures: 3, LastBuild: 397},
			{Name: "Deploy", Failures: 1, LastBuild: 384}}}
}

func TestPlanStatsViewFull(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, PlanStatsView(o, fullStats()))
	out := buf.String()
	require.Contains(t, out, "PROJ-DEPLOY  last 20 builds (#381–#400, 6 days)\n")
	require.Regexp(t, `Success\s+80%\s+\(16 ✓\s+4 ✗\s+1 stopped\)\s+streak ✓ 3`, out)
	require.Regexp(t, `Duration\s+median 4m10s\s+p90 6m02s\s+max 9m40s \(#388\)\s+of 16 successful`, out)
	require.Regexp(t, `Integration tests\s+3 of 4 failures\s+last #397`, out)
	require.Regexp(t, `Deploy\s+1 of 4 failures\s+last #384`, out)
}

func TestPlanStatsViewBranchAndEmptyParts(t *testing.T) {
	s := fullStats()
	s.Branch, s.PlanKey = "develop", "PROJ-DEPLOY12"
	s.Durations, s.FailingJobs, s.Failed, s.Passed = nil, nil, 0, 0
	o, buf := testOut(false)
	require.NoError(t, PlanStatsView(o, s))
	out := buf.String()
	require.Contains(t, out, "branch develop (PROJ-DEPLOY12)")
	require.Contains(t, out, "no successful builds")
	require.Regexp(t, `Failing jobs\s+none`, out)
}

func TestPlanStatsViewOneSuccessfulBuild(t *testing.T) {
	s := fullStats()
	s.Durations = &app.DurationStats{Median: 131 * time.Second, P90: 131 * time.Second, Max: 131 * time.Second, MaxBuild: 482, Of: 1}
	o, buf := testOut(false)
	require.NoError(t, PlanStatsView(o, s))
	require.Contains(t, buf.String(), "Duration  2m11s (#482)   of 1 successful\n")
}

func TestPlanStatsViewOneBuildHeader(t *testing.T) {
	s := fullStats()
	s.Builds, s.First, s.Last, s.To = 1, 482, 482, statsFrom
	o, buf := testOut(false)
	require.NoError(t, PlanStatsView(o, s))
	require.Contains(t, buf.String(), "PROJ-DEPLOY  last build (#482)\n")
}

func TestPlanStatsNoFinishedBuilds(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, PlanStatsView(o, app.PlanStats{PlanKey: "PROJ-DEPLOY", Builds: 20, Other: map[provider.State]int{}}))
	require.Equal(t, "no finished builds of PROJ-DEPLOY in the last 20\n", buf.String())

	for builds, want := range map[int]string{0: "PROJ-DEPLOY has no builds\n", 1: "the last build of PROJ-DEPLOY has not finished\n"} {
		o, buf := testOut(false)
		require.NoError(t, PlanStatsView(o, app.PlanStats{PlanKey: "PROJ-DEPLOY", Builds: builds, Other: map[provider.State]int{}}))
		require.Equal(t, want, buf.String())
	}

	data, err := json.Marshal(PlanStatsJSON(app.PlanStats{PlanKey: "PROJ-DEPLOY", Other: map[provider.State]int{}}))
	require.NoError(t, err)
	require.JSONEq(t, withoutTimeFields(t, data), `{"plan_key":"PROJ-DEPLOY","branch":"","builds":0,"first":0,"last":0,"from":null,"to":null,
	  "passed":0,"failed":0,"other":{},"success_rate":null,"duration":null,"streak":null,"failing_jobs":[],"warnings":[]}`)
}

func TestPlanStatsJSON(t *testing.T) {
	data, err := json.Marshal(PlanStatsJSON(fullStats()))
	require.NoError(t, err)
	require.JSONEq(t, withoutTimeFields(t, data), `{"plan_key":"PROJ-DEPLOY","branch":"","builds":20,"first":381,"last":400,
	  "from":"2026-09-30T08:00:00Z","to":"2026-10-06T08:00:00Z",
	  "passed":16,"failed":4,"other":{"stopped":1},"success_rate":0.8,
	  "duration":{"median_ms":250000,"p90_ms":362000,"max_ms":580000,"max_build":388,"of":16},
	  "streak":{"state":"success","count":3},
	  "failing_jobs":[{"name":"Integration tests","failures":3,"last_build":397},{"name":"Deploy","failures":1,"last_build":384}],
	  "warnings":[]}`)
}

// withoutTimeFields drops the fields added for the queue figures, so the
// rest can be compared with the document as it was before them: no field
// that was there is renamed, removed or changed.
func withoutTimeFields(t *testing.T, data []byte) string {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	for _, k := range []string{"wait", "wait_earlier", "slowest_stages", "slowest_jobs", "unread_builds"} {
		require.Contains(t, m, k)
		delete(m, k)
	}
	out, err := json.Marshal(m)
	require.NoError(t, err)
	return string(out)
}

func timeStats() app.PlanStats {
	s := fullStats()
	s.Wait = &app.WaitStats{Median: 4 * time.Second, P90: 38 * time.Second, Share: 0.12, Jobs: 40}
	s.Earlier = &app.WaitStats{Median: 2 * time.Second, P90: 20 * time.Second, Share: 0.07, Jobs: 38}
	s.SlowStages = []app.Slowest{{Name: "Test", Median: 5 * time.Minute, Runs: 16, Earlier: 4 * time.Minute}}
	s.SlowJobs = []app.Slowest{{Name: "Integration tests", Median: 4 * time.Minute, Runs: 12}}
	return s
}

func TestPlanStatsViewTimeFigures(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, PlanStatsView(o, timeStats()))
	out := buf.String()
	require.Contains(t, out, "Waiting   median 4s  p90 38s  12% of job time spent waiting for an agent   earlier: median 2s  p90 20s  7%\n")
	require.Contains(t, out, "Slowest stages\n  Test")
	require.Contains(t, out, "median 5m")
	require.Contains(t, out, "earlier 4m")
	require.Contains(t, out, "Slowest jobs\n  Integration tests")
}

func TestPlanStatsViewWithoutReadableJobs(t *testing.T) {
	s := fullStats()
	s.UnreadBuilds = 3
	o, buf := testOut(false)
	require.NoError(t, PlanStatsView(o, s))
	require.NotContains(t, buf.String(), "Waiting")
	require.NotContains(t, buf.String(), "Slowest")
	require.Contains(t, buf.String(), "3 of 20 finished builds could not be read")
}

func TestPlanStatsJSONTimeFigures(t *testing.T) {
	data, err := json.Marshal(PlanStatsJSON(timeStats()))
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &m))
	require.JSONEq(t, `{"median_ms":4000,"p90_ms":38000,"share":0.12,"jobs":40}`, string(m["wait"]))
	require.JSONEq(t, `{"median_ms":2000,"p90_ms":20000,"share":0.07,"jobs":38}`, string(m["wait_earlier"]))
	require.JSONEq(t, `[{"name":"Test","median_ms":300000,"runs":16,"earlier_median_ms":240000}]`, string(m["slowest_stages"]))
	require.JSONEq(t, `[{"name":"Integration tests","median_ms":240000,"runs":12,"earlier_median_ms":0}]`, string(m["slowest_jobs"]))
}

// On a branch the header names the plan, the branch, then the branch plan's
// key — not the branch key twice.
func TestPlanStatsBranchHeader(t *testing.T) {
	s := fullStats()
	s.MasterKey, s.PlanKey, s.Branch = "PROJ-DEPLOY", "PROJ-DEPLOY12", "develop"
	o, buf := testOut(false)
	require.NoError(t, PlanStatsView(o, s))
	require.Contains(t, buf.String(), "PROJ-DEPLOY  branch develop (PROJ-DEPLOY12)  last 20 builds (#381–#400, 6 days)\n")
}
