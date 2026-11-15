package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func trackBuild(key string, jobs ...provider.Job) provider.Build {
	return provider.Build{Key: key, Stages: []provider.Stage{{Name: "Test", Jobs: jobs}}}
}

func trackFake() *fake.Provider {
	f := fakeBamboo()
	f.AgentList = []provider.Agent{
		{ID: 1, Name: "linux-1", Type: "REMOTE", Online: true, Enabled: true, Busy: true},
		{ID: 2, Name: "linux-2", Type: "REMOTE", Online: true, Enabled: true},
	}
	// linux-1 failed 6 of 7 finished jobs; linux-2 failed 1 of 8; one job is running on linux-1.
	f.Sequences = map[string][]provider.Build{}
	f.JobAgentMap = map[string]string{}
	f.Recent = nil
	add := func(i int, agent string, st provider.State) {
		bk := "PROJ-X-" + string(rune('A'+i))
		jk := "PROJ-X-J" + string(rune('A'+i)) + "-1"
		b := trackBuild(bk, provider.Job{Key: jk, Name: "Job", State: st, Duration: time.Minute})
		f.Sequences[bk] = []provider.Build{b}
		f.Recent = append(f.Recent, provider.Build{Key: bk})
		f.JobAgentMap[jk] = agent
	}
	add(0, "linux-1", provider.StateRunning)
	for i := 1; i <= 7; i++ {
		st := provider.StateFailed
		if i == 7 {
			st = provider.StateSuccess
		}
		add(i, "linux-1", st)
	}
	for i := 8; i <= 15; i++ {
		st := provider.StateSuccess
		if i == 8 {
			st = provider.StateFailed
		}
		add(i, "linux-2", st)
	}
	add(16, "", provider.StateSuccess) // a log that names no agent
	add(17, "", provider.StateQueued)  // not started: not looked at
	return f
}

// Intent (issue 233, BAM-34 P5): an agent's record lists what it runs now and
// its latest finished jobs, and compares its failure share with the others'.
func TestAgentRecordCountsJobsPerAgent(t *testing.T) {
	f := trackFake()
	svc := newService(t, f)
	rec, err := svc.AgentRecord(context.Background(), "linux-1", 0)
	require.NoError(t, err)
	assert.Len(t, rec.Now, 1)
	assert.Equal(t, AgentTally{Jobs: 7, Failed: 6}, rec.This)
	assert.Equal(t, AgentTally{Jobs: 8, Failed: 1}, rec.Others)
	assert.Equal(t, 1, rec.OtherAgents)
	assert.Equal(t, 1, rec.Unknown, "the job whose log names no agent is counted apart")
	assert.Equal(t, 17, rec.Jobs)
	assert.True(t, rec.Slow)
	assert.Len(t, rec.Recent, 7)
	assert.Equal(t, "PROJ-X-B", rec.Recent[0].BuildKey, "newest first")

	good, err := svc.AgentRecord(context.Background(), "linux-2", 0)
	require.NoError(t, err)
	assert.False(t, good.Slow)
	assert.Equal(t, AgentTally{Jobs: 8, Failed: 1}, good.This)
}

func TestAgentRecordKeepsAtMostTwentyJobs(t *testing.T) {
	f := trackFake()
	for i := 0; i < 30; i++ {
		bk := "PROJ-Y-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		jk := bk + "-J1"
		f.Sequences[bk] = []provider.Build{trackBuild(bk, provider.Job{Key: jk, State: provider.StateSuccess})}
		f.Recent = append(f.Recent, provider.Build{Key: bk})
		f.JobAgentMap[jk] = "linux-2"
	}
	rec, err := newService(t, f).AgentRecord(context.Background(), "linux-2", 100)
	require.NoError(t, err)
	assert.Len(t, rec.Recent, TrackJobs)
	assert.Equal(t, 38, rec.This.Jobs, "the tally counts every job, the list keeps 20")
}

// A finished job's agent never changes, so it is read once and remembered;
// a running job's is read again.
func TestFinishedJobAgentsAreRemembered(t *testing.T) {
	f := trackFake()
	svc := newService(t, f)
	svc.JobAgents = &JobAgentStore{Dir: t.TempDir()}
	_, err := svc.AgentRecord(context.Background(), "linux-1", 0)
	require.NoError(t, err)
	first := len(f.JobAgentCalls)
	assert.Equal(t, 17, first)
	f.JobAgentCalls = nil
	_, err = svc.AgentRecord(context.Background(), "linux-1", 0)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"PROJ-X-JA-1", "PROJ-X-JQ-1"}, f.JobAgentCalls,
		"only the running job and the job whose log named nobody are read again")
}

func TestAgentRecordWhenLogsCannotBeRead(t *testing.T) {
	f := trackFake()
	f.JobAgentMap = nil
	f.JobAgentErr = errs.Authf("403 from bamboo.example.com: no")
	_, err := newService(t, f).AgentRecord(context.Background(), "linux-1", 0)
	require.Error(t, err)
	assert.Equal(t, errs.KindAuth, errs.KindOf(err))
}

// A build bam could not read is not counted as looked through.
func TestAgentRecordCountsOnlyTheBuildsItRead(t *testing.T) {
	f := trackFake()
	f.Recent = append(f.Recent, provider.Build{Key: "PROJ-X-GONE"})
	f.GetErrs = map[string][]error{"PROJ-X-GONE": {errs.Bamboof("Bamboo server error 500 from bamboo.example.com")}}
	rec, err := newService(t, f).AgentRecord(context.Background(), "linux-1", 0)
	require.NoError(t, err)
	assert.Equal(t, 18, rec.Builds)
}

func TestAgentRecordUnknownAgentAndRefusedList(t *testing.T) {
	f := trackFake()
	_, err := newService(t, f).AgentRecord(context.Background(), "nope", 0)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	f.AgentsErr = errors.New("refused")
	_, err = newService(t, f).AgentRecord(context.Background(), "linux-1", 0)
	assert.Error(t, err)
}

func TestFillJobAgentsNamesTheBuildsAgents(t *testing.T) {
	f := trackFake()
	svc := newService(t, f)
	b := trackBuild("PROJ-Z-1",
		provider.Job{Key: "PROJ-X-JB-1", State: provider.StateSuccess},
		provider.Job{Key: "PROJ-X-JI-1", State: provider.StateSuccess},
		provider.Job{Key: "PROJ-X-JR-1", State: provider.StateQueued})
	svc.FillJobAgents(context.Background(), &b)
	assert.Equal(t, "linux-1, linux-2", b.Agent)
	assert.Equal(t, "linux-1", b.Stages[0].Jobs[0].Agent)
	assert.Empty(t, b.Stages[0].Jobs[2].Agent, "a queued job has no agent yet")

	f.JobAgentMap, f.JobAgentErr = nil, errs.ErrUnsupported
	b2 := trackBuild("PROJ-Z-2", provider.Job{Key: "PROJ-X-JB-1", State: provider.StateSuccess})
	svc2 := newService(t, f)
	svc2.FillJobAgents(context.Background(), &b2)
	assert.Empty(t, b2.Agent, "a server that cannot say leaves the field empty without failing")
}
