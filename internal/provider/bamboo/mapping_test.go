package bamboo

import (
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapState(t *testing.T) {
	cases := []struct {
		life, state string
		want        provider.State
	}{
		{"Queued", "Unknown", provider.StateQueued},
		{"Pending", "Unknown", provider.StateQueued},
		{"InProgress", "Unknown", provider.StateRunning},
		{"Finished", "Successful", provider.StateSuccess},
		{"Finished", "Failed", provider.StateFailed},
		{"Finished", "Unknown", provider.StateStopped},
		{"NotBuilt", "Unknown", provider.StateNotBuilt},
		{"Skipped", "", provider.StateSkipped},
		{"Mystery", "Successful", provider.StateUnknown},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, mapState(tc.life, tc.state), tc.life+"/"+tc.state)
	}
}

func TestPlainReason(t *testing.T) {
	assert.Equal(t, "Manual run by J Doe", plainReason(`Manual run by <a href="https://bamboo.example.com/browse/user/jdoe">J Doe</a>`))
	assert.Equal(t, "Changes by a1b2c3d <jdoe@example.com>", plainReason("Changes by a1b2c3d &lt;jdoe@example.com&gt;"))
	assert.Equal(t, "Scheduled", plainReason("  Scheduled \n"))
}

func TestParseTime(t *testing.T) {
	want := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	assert.True(t, want.Equal(parseTime("2026-09-12T11:00:00.000+02:00")))
	assert.True(t, want.Equal(parseTime("2026-09-12T09:00:00.000Z")))
	assert.True(t, want.Equal(parseTime("2026-09-12T09:00:00Z")))
	assert.True(t, parseTime("").IsZero())
}

// A build stopped through the queue endpoint comes back as NotBuilt with
// state Unknown, but it did start, so it is "stopped", not "not built".
func TestStoppedBuildMapsToStopped(t *testing.T) {
	r := resultDTO{Key: "PROJ-BUILD-45", LifeCycleState: "NotBuilt", State: "Unknown",
		BuildStartedTime: "2026-09-17T06:17:46.478Z", NotRunYet: false}

	assert.Equal(t, provider.StateStopped, stateOf(r))
}

func TestNeverStartedBuildStaysNotBuilt(t *testing.T) {
	r := resultDTO{Key: "PROJ-BUILD-JOB2-45", LifeCycleState: "NotBuilt", State: "Unknown", NotRunYet: true}

	assert.Equal(t, provider.StateNotBuilt, stateOf(r))
}

// A job of a stage that never ran also carries a start time in Bamboo and
// has no notRunYet field, so the stopped heuristic must not reach jobs.
func TestNotBuiltJobStaysNotBuilt(t *testing.T) {
	j := resultDTO{BuildResultKey: "PROJ-BUILD-JOB2-45", LifeCycleState: "NotBuilt", State: "Unknown",
		BuildStartedTime: "2026-09-17T06:20:25.703Z"}

	assert.Equal(t, provider.StateNotBuilt, jobState(j))
}

func TestStoppedBuildKeepsItsJobsNotBuilt(t *testing.T) {
	r := resultDTO{Key: "PROJ-BUILD-45", LifeCycleState: "NotBuilt", State: "Unknown",
		BuildStartedTime: "2026-09-17T06:20:24.315Z"}
	r.Stages.Stage = []stageDTO{{Name: "Test", LifeCycleState: "NotBuilt", State: "Unknown"}}
	r.Stages.Stage[0].Results.Result = []resultDTO{{BuildResultKey: "PROJ-BUILD-JOB1-45",
		LifeCycleState: "NotBuilt", State: "Unknown", BuildStartedTime: "2026-09-17T06:20:25.703Z"}}

	c, _ := newTestServer(t, map[string]*route{})
	b := c.mapBuild(r)

	assert.Equal(t, provider.StateStopped, b.State, "the build was stopped")
	require.Len(t, b.Stages, 1)
	require.Len(t, b.Stages[0].Jobs, 1)
	assert.Equal(t, provider.StateNotBuilt, b.Stages[0].Jobs[0].State)
}
