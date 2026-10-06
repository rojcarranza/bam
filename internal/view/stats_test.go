package view

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
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

func TestPlanStatsNoFinishedBuilds(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, PlanStatsView(o, app.PlanStats{PlanKey: "PROJ-DEPLOY", Builds: 20, Other: map[provider.State]int{}}))
	require.Equal(t, "no finished builds of PROJ-DEPLOY in the last 20\n", buf.String())

	data, err := json.Marshal(PlanStatsJSON(app.PlanStats{PlanKey: "PROJ-DEPLOY", Other: map[provider.State]int{}}))
	require.NoError(t, err)
	require.JSONEq(t, `{"plan_key":"PROJ-DEPLOY","branch":"","builds":0,"first":0,"last":0,"from":null,"to":null,
	  "passed":0,"failed":0,"other":{},"success_rate":null,"duration":null,"streak":null,"failing_jobs":[],"warnings":[]}`, string(data))
}

func TestPlanStatsJSON(t *testing.T) {
	data, err := json.Marshal(PlanStatsJSON(fullStats()))
	require.NoError(t, err)
	require.JSONEq(t, `{"plan_key":"PROJ-DEPLOY","branch":"","builds":20,"first":381,"last":400,
	  "from":"2026-09-30T08:00:00Z","to":"2026-10-06T08:00:00Z",
	  "passed":16,"failed":4,"other":{"stopped":1},"success_rate":0.8,
	  "duration":{"median_ms":250000,"p90_ms":362000,"max_ms":580000,"max_build":388,"of":16},
	  "streak":{"state":"success","count":3},
	  "failing_jobs":[{"name":"Integration tests","failures":3,"last_build":397},{"name":"Deploy","failures":1,"last_build":384}],
	  "warnings":[]}`, string(data))
}
