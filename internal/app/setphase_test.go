package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-53 machine 3): one RunPhase per run, derived from the record,
// so records written before the restart flag existed read the same way.
func TestRunPhaseOfEveryRecordShape(t *testing.T) {
	for name, tc := range map[string]struct {
		rec  RunRecord
		want RunPhase
	}{
		"nothing yet":        {RunRecord{}, RunPending},
		"build, end unknown": {RunRecord{BuildKey: "PROJ-A-1"}, RunStarted},
		"build ended":        {RunRecord{BuildKey: "PROJ-A-1", State: provider.StateFailed}, RunEnded},
		"trigger failed":     {RunRecord{State: provider.StateFailed, Error: "queue full"}, RunStartFailed},
		"watch failed":       {RunRecord{BuildKey: "PROJ-A-1", State: provider.StateFailed, Error: "timed out"}, RunWatchFailed},
		"restart due":        {RunRecord{BuildKey: "PROJ-A-1", Restart: true}, RunRestartDue},
	} {
		t.Run(name, func(t *testing.T) { require.Equal(t, tc.want, tc.rec.Phase()) })
	}
}

func TestRecordWithoutRestartFieldStillLoads(t *testing.T) {
	old := `{"target":"provision-lab","plan_key":"PROJ-PROV12","build_key":"PROJ-PROV12-1","state":"failed","reruns":0}`
	var r RunRecord
	require.NoError(t, json.Unmarshal([]byte(old), &r))
	require.False(t, r.Restart)
	require.Equal(t, RunEnded, r.Phase())
	out, err := json.Marshal(RunRecord{Target: "x"})
	require.NoError(t, err)
	require.NotContains(t, string(out), "restart", "the field is left out until it is due")
}

// cancelOnRestart cancels the run's context when Bamboo is asked to restart:
// Ctrl-C during bam set rerun --failed.
type cancelOnRestart struct {
	*fake.Provider
	cancel context.CancelFunc
}

func (c *cancelOnRestart) RestartBuild(ctx context.Context, key string) (provider.Build, error) {
	c.cancel()
	return provider.Build{}, ctx.Err()
}

// Defect: the restart list lived in memory, so Ctrl-C during rerun --failed
// lost it and resume watched the old failed build.
func TestInterruptedRerunKeepsTheRestartForResume(t *testing.T) {
	fp := trainBamboo(nil)
	restartable(fp, "PROJ-PROV12-1", provider.StateSuccess)
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: failedRecord()})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.P = &cancelOnRestart{Provider: fp, cancel: cancel}
	ch, err := svc.RerunSet(ctx, rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	drain(ch)
	got, err := svc.Sets.Load(rec.ID)
	require.NoError(t, err)
	require.Equal(t, SetInterrupted, got.Status)
	run := got.Stages[1].Runs[0]
	require.Equal(t, RunRestartDue, run.Phase(), "the restart is in the record")
	require.Equal(t, 0, run.Reruns, "an interrupted restart is not counted")

	svc.P = fp
	// The build is still failed on Bamboo. The fake's call counter went on
	// from the interrupted attempt, so the sequence starts one call later.
	fp.Sequences["PROJ-PROV12-1"] = append([]provider.Build{{Key: "PROJ-PROV12-1", State: provider.StateFailed}}, fp.Sequences["PROJ-PROV12-1"]...)
	ch, err = svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Equal(t, []string{"PROJ-PROV12-1"}, fp.Restarted, "resume restarts the failed build")
	require.Equal(t, provider.StateSuccess, evs[len(evs)-1].State)
	got, _ = svc.Sets.Load(rec.ID)
	require.Equal(t, SetSuccessful, got.Status)
	require.Equal(t, 1, got.Stages[1].Runs[0].Reruns)
	require.False(t, got.Stages[1].Runs[0].Restart)
}

// cancelOnTrigger cancels the run's context when Bamboo is asked to trigger:
// Ctrl-C while a trigger is in flight.
type cancelOnTrigger struct {
	*fake.Provider
	cancel context.CancelFunc
}

func (c *cancelOnTrigger) Trigger(ctx context.Context, req provider.TriggerRequest) (provider.Build, error) {
	c.cancel()
	return provider.Build{}, ctx.Err()
}

// Defect: Ctrl-C during a trigger was recorded as a failed trigger, so resume
// skipped the run and ended the set failed.
func TestInterruptDuringTriggerIsNotAFailedTrigger(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.P = &cancelOnTrigger{Provider: fp, cancel: cancel}
	id, ch, err := svc.RunSet(ctx, p, SetLockOptions{})
	require.NoError(t, err)
	drain(ch)
	got, err := svc.Sets.Load(id)
	require.NoError(t, err)
	require.Equal(t, SetInterrupted, got.Status)
	require.Equal(t, RunPending, got.Stages[0].Runs[0].Phase())
	require.Empty(t, got.Stages[0].Runs[0].Error)

	svc.P = fp
	ch, err = svc.ResumeSet(context.Background(), id, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Equal(t, provider.StateSuccess, evs[len(evs)-1].State, "resume triggers the run and the set succeeds")
	got, _ = svc.Sets.Load(id)
	require.Equal(t, SetSuccessful, got.Status)
}

// cancelAfterRestart accepts the restart, then cancels while Restart polls
// for the new build: Ctrl-C after Bamboo took the request.
type cancelAfterRestart struct {
	*fake.Provider
	cancel context.CancelFunc
}

func (c *cancelAfterRestart) RestartBuild(ctx context.Context, key string) (provider.Build, error) {
	b, err := c.Provider.RestartBuild(ctx, key)
	c.cancel()
	return b, err
}

// An interrupt after Bamboo accepted the restart keeps it counted and not due,
// so resume watches the restarted build and never restarts it a second time.
func TestInterruptAfterBambooTookTheRestartKeepsItCounted(t *testing.T) {
	fp := trainBamboo(nil)
	restartable(fp, "PROJ-PROV12-1", provider.StateSuccess)
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: failedRecord()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.P = &cancelAfterRestart{Provider: fp, cancel: cancel}
	ch, err := svc.RerunSet(ctx, rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	drain(ch)
	got, _ := svc.Sets.Load(rec.ID)
	run := got.Stages[1].Runs[0]
	require.Equal(t, []string{"PROJ-PROV12-1"}, fp.Restarted)
	require.Equal(t, 1, run.Reruns, "Bamboo restarted it, so it counts")
	require.Equal(t, RunStarted, run.Phase(), "no second restart is due")
}
