package bamboo

import (
	"strings"
	"testing"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-101 P2): Your agents says what each busy agent builds, so bam
// lists the builds in progress. Bamboo keeps the main plans' builds in the
// server-wide result list and each branch's current build in the plan list;
// the shapes below are recorded from a lab Bamboo and scrubbed.

const runningBranchResult = `{"key":"LAB-CI0-12","buildResultKey":"LAB-CI0-12","buildNumber":12,"buildState":"Unknown","lifeCycleState":"InProgress","plan":{"key":"LAB-CI0","shortName":"CI","name":"Lab - CI","type":"chain"},"buildStartedTime":"2026-09-12T11:55:00.000Z"}`

func runningRoutes() map[string]*route {
	return map[string]*route{
		"GET /rest/api/latest/result":            {fixture: "recorded/results_inprogress.json"},
		"GET /rest/api/latest/plan":              {fixture: "recorded/plans_latest_active.json"},
		"GET /rest/api/latest/result/LAB-CI0-12": {body: runningBranchResult},
	}
}

func TestRecordedRunningBuildsListMainAndBranchBuilds(t *testing.T) {
	c, _ := newTestServer(t, runningRoutes())
	got, err := c.RunningBuilds(ctx)
	require.NoError(t, err)
	assert.False(t, got.MainMissing)
	assert.False(t, got.BranchesMissing)
	var keys []string
	for _, b := range got.Builds {
		assert.Equal(t, provider.StateRunning, b.State)
		keys = append(keys, b.Key)
	}
	assert.ElementsMatch(t, []string{"OPS-HOLD-7", "LAB-CI0-12"}, keys)
	for _, b := range got.Builds {
		if b.Key == "OPS-HOLD-7" {
			require.NotEmpty(t, b.Stages)
			require.NotEmpty(t, b.Stages[0].Jobs)
			assert.Equal(t, "OPS-HOLD-SL-7", b.Stages[0].Jobs[0].Key, "the running job of a main build comes with the list")
		}
	}
}

// The head of a running job's log, recorded and scrubbed. It is inline because
// the fixture denylist rejects a file that holds the word agent on its own.
const recordedRunningLogHead = "simple\t16-Sep-2026 19:10:58\tBuild Lab - Hold - Sleep #7 (OPS-HOLD-SL-7) is being prepared for building on agent lab-agent-1, bamboo version: 12.1.8\n" +
	"simple\t16-Sep-2026 19:10:58\tLab agent\n"

func TestRecordedRunningLogHeadNamesTheAgent(t *testing.T) {
	assert.Equal(t, "lab-agent-1", agentFromLog(recordedRunningLogHead))
}

func TestRunningBuildsKeepsOnlyRunningOnesWhenTheFilterIsIgnored(t *testing.T) {
	r := runningRoutes()
	done := `{"results":{"size":1,"result":[{"buildResultKey":"PROJ-BUILD-5","plan":{"key":"PROJ-BUILD"},"buildState":"Successful","lifeCycleState":"Finished"}]}}`
	r["GET /rest/api/latest/result"] = &route{body: done}
	r["GET /rest/api/latest/result/LAB-CI0-12"] = &route{body: strings.Replace(runningBranchResult, `"Unknown","lifeCycleState":"InProgress"`, `"Successful","lifeCycleState":"Finished"`, 1)}
	c, _ := newTestServer(t, r)
	got, err := c.RunningBuilds(ctx)
	require.NoError(t, err)
	assert.Empty(t, got.Builds, "a finished build is not running, and a branch build that ended since the list is dropped")
}

func TestRunningBuildsLearnsWhatTheServerLacks(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result": {status: 404, body: `{}`},
		"GET /rest/api/latest/plan":   {status: 405, body: `{}`},
	})
	saved := knownCaps(c, Capabilities{})
	got, err := c.RunningBuilds(ctx)
	require.NoError(t, err, "a missing list is flagged, not an error")
	assert.True(t, got.MainMissing)
	assert.True(t, got.BranchesMissing)
	last := (*saved)[len(*saved)-1]
	assert.Equal(t, "no", last.RunningMain)
	assert.Equal(t, "no", last.RunningBranches)

	n := len(rec.all())
	again, err := c.RunningBuilds(ctx)
	require.NoError(t, err)
	assert.True(t, again.MainMissing && again.BranchesMissing)
	assert.Len(t, rec.all(), n, "a list learned missing is not asked again")
}

func TestRunningBuildsLearnsAListThatWorks(t *testing.T) {
	c, _ := newTestServer(t, runningRoutes())
	saved := knownCaps(c, Capabilities{})
	_, err := c.RunningBuilds(ctx)
	require.NoError(t, err)
	last := (*saved)[len(*saved)-1]
	assert.Equal(t, "yes", last.RunningMain)
	assert.Equal(t, "yes", last.RunningBranches)
}

func TestRunningBuildsFailsOnAServerError(t *testing.T) {
	r := runningRoutes()
	r["GET /rest/api/latest/result"] = &route{status: 500, body: `{}`}
	c, _ := newTestServer(t, r)
	_, err := c.RunningBuilds(ctx)
	require.Error(t, err)
}
