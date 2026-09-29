package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// setService is newService with a two-stage set "release":
// [publish-specs] then [provision-lab with cluster_name and compute_nodes].
func setService(t *testing.T, p *fake.Provider) *Service {
	t.Helper()
	svc := newService(t, p)
	svc.Cfg.Project.Targets["publish-specs"] = config.Target{Plan: "PROJ-BUILD"}
	svc.Cfg.Project.Sets = map[string]config.Set{"release": {Stages: []config.Stage{
		{{Target: "publish-specs"}},
		{{Target: "provision-lab", Vars: config.StringMap{"cluster_name": "alpha", "compute_nodes": "4"}}},
	}}}
	return svc
}

func TestPlanSetResolvesEveryRunWithSetVarsOverDefaults(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(k string) string { return map[string]string{"LAB_DB_PASSWORD": "s3cret"}[k] }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{Vars: []string{"compute_nodes=5"}})
	require.NoError(t, err)
	require.Equal(t, "release", p.Name)
	require.Equal(t, "https://bamboo.example.com", p.Origin)
	require.Len(t, p.Stages, 2)
	require.Equal(t, "PROJ-BUILD", p.Stages[0][0].Ref.PlanKey)
	prov := p.Stages[1][0]
	require.Equal(t, "provision-lab", prov.Target)
	require.Equal(t, "PROJ-PROV12", prov.Ref.PlanKey, "the preset's branch applies")
	name, _ := prov.Vars.Get("cluster_name")
	require.Equal(t, "alpha", name.Value, "set vars beat preset defaults")
	nodes, _ := prov.Vars.Get("compute_nodes")
	require.Equal(t, "5", nodes.Value, "--var beats set vars")
	typ, _ := prov.Vars.Get("cluster_type")
	require.Equal(t, "k8s", typ.Value, "preset defaults still apply")
	require.Equal(t, []string{"compute_nodes"}, p.VarNames)
	require.Equal(t, "k8s", svc.Cfg.Project.Targets["provision-lab"].Defaults["cluster_type"])
	require.NotContains(t, svc.Cfg.Project.Targets["provision-lab"].Defaults, "cluster_name", "the preset itself is untouched")
}

func TestPlanSetCollectsEveryError(t *testing.T) {
	svc := setService(t, fakeBamboo())
	prov := svc.Cfg.Project.Targets["provision-lab"]
	prov.Branch = "no-such-branch"
	svc.Cfg.Project.Targets["provision-lab"] = prov
	svc.Cfg.Project.Targets["build"] = config.Target{Plan: "PROJ-BUILD", Required: []string{"release_tag"}}
	svc.Cfg.Project.Sets["release"] = config.Set{Stages: []config.Stage{
		{{Target: "provision-lab"}},
		{{Target: "build"}},
		{{Target: "publish-specs"}},
	}}
	_, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.Error(t, err)
	require.Equal(t, errs.KindUsage, errs.KindOf(err))
	text := err.Error() + " " + errTry(err)
	require.Contains(t, text, "provision-lab")
	require.Contains(t, text, "no-such-branch")
	require.Contains(t, text, "stage 2 build")
	require.Contains(t, text, "release_tag")
	require.Empty(t, svc.P.(*fake.Provider).Triggered)
}

func TestPlanSetWarnsAboutAnUndeclaredVar(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{Vars: []string{"global_flag=1"}})
	require.NoError(t, err)
	joined := strings.Join(p.Warnings, "\n")
	require.Contains(t, joined, "global_flag")
	require.Contains(t, joined, "publish-specs: global_flag is not a declared variable of PROJ-BUILD")
	require.Contains(t, joined, "provision-lab: global_flag is not a declared variable of PROJ-PROV")
	v, ok := p.Stages[1][0].Vars.Get("global_flag")
	require.True(t, ok, "an undeclared --var is still sent")
	require.Equal(t, "1", v.Value)
}

func TestPlanSetRejectsAnotherServer(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Cfg.Project.Servers["lab"] = config.Server{URL: "https://bamboo.lab.example"}
	tg := svc.Cfg.Project.Targets["publish-specs"]
	tg.Server = "lab"
	svc.Cfg.Project.Targets["publish-specs"] = tg
	_, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.Equal(t, errs.KindConfig, errs.KindOf(err))
	require.Contains(t, err.Error(), "bamboo.lab.example")
}

func TestPlanSetUnknownSet(t *testing.T) {
	_, err := setService(t, fakeBamboo()).PlanSet(context.Background(), "nope", SetOptions{})
	require.Equal(t, errs.KindUsage, errs.KindOf(err))
}

func errTry(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Why + " " + e.Try
	}
	return ""
}

func drain(ch <-chan SetEvent) []SetEvent {
	var out []SetEvent
	for e := range ch {
		out = append(out, e)
	}
	return out
}

// kinds is the events without run_event, as type:stage:target.
func kinds(evs []SetEvent) []string {
	var out []string
	for _, e := range evs {
		if e.Type != SetRunEvent {
			out = append(out, fmt.Sprintf("%s:%d:%s", e.Type, e.Stage, e.Target))
		}
	}
	return out
}

// trainBamboo answers a trigger of each plan with <plan>-1, which runs once
// and then settles in its final state.
func trainBamboo(results map[string]provider.State) *fake.Provider {
	p := fakeBamboo()
	p.TriggerResults = map[string]provider.Build{}
	p.Sequences = map[string][]provider.Build{}
	for plan, final := range results {
		key := plan + "-1"
		p.TriggerResults[plan] = provider.Build{Key: key, State: provider.StateQueued}
		p.Sequences[key] = []provider.Build{{Key: key, State: provider.StateRunning}, {Key: key, State: final}}
	}
	return p
}

// forever makes plan's build run and never finish.
func forever(p *fake.Provider, plan string) {
	key := plan + "-1"
	p.TriggerResults[plan] = provider.Build{Key: key, State: provider.StateQueued}
	p.Sequences[key] = []provider.Build{{Key: key, State: provider.StateRunning}}
}

func runSet(t *testing.T, svc *Service) (string, []SetEvent) {
	t.Helper()
	svc.Getenv = func(string) string { return "x" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.NoError(t, err)
	id, ch, err := svc.RunSet(context.Background(), p, SetLockOptions{})
	require.NoError(t, err)
	return id, drain(ch)
}

func TestRunSetHappyPath(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	id, evs := runSet(t, svc)
	require.Equal(t, []string{
		"stage_start:0:", "run_triggered:0:publish-specs", "run_done:0:publish-specs", "stage_done:0:",
		"stage_start:1:", "run_triggered:1:provision-lab", "run_done:1:provision-lab", "stage_done:1:",
		"set_done:1:",
	}, kinds(evs))
	for _, e := range evs {
		require.Equal(t, id, e.ID)
		require.Equal(t, "release", e.Set)
	}
	require.Equal(t, provider.StateSuccess, evs[len(evs)-1].State)
	rec, err := svc.Sets.Load(id)
	require.NoError(t, err)
	require.Equal(t, SetSuccessful, rec.Status)
	require.Equal(t, "PROJ-PROV12-1", rec.Stages[1].Runs[0].BuildKey)
	require.Equal(t, provider.StateSuccess, rec.Stages[1].Runs[0].State)
	require.NotNil(t, rec.FinishedAt)
	_, err = os.Stat(filepath.Join(svc.Sets.Dir, "release.lock"))
	require.ErrorIs(t, err, fs.ErrNotExist, "the lock is released")
}

func TestRunSetFailedStageStopsTheTrainAndCancelsNothing(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateFailed, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	id, evs := runSet(t, svc)
	require.Len(t, fp.Triggered, 1, "stage 2 never triggers")
	require.Empty(t, fp.Stopped)
	require.Equal(t, SetDone, evs[len(evs)-1].Type)
	require.Equal(t, provider.StateFailed, evs[len(evs)-1].State)
	rec, _ := svc.Sets.Load(id)
	require.Equal(t, SetFailed, rec.Status)
}

func TestRunSetParallelStageWaitsForEveryRun(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateFailed, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Cfg.Project.Sets["release"] = config.Set{Stages: []config.Stage{
		{{Target: "publish-specs"}, {Target: "provision-lab", Vars: config.StringMap{"cluster_name": "a"}}},
		{{Target: "build"}},
	}}
	_, evs := runSet(t, svc)
	var done []string
	stageDone := 0
	for _, e := range evs {
		switch e.Type {
		case SetRunDone:
			done = append(done, e.Target)
		case SetStageDone:
			stageDone++
		}
	}
	require.ElementsMatch(t, []string{"publish-specs", "provision-lab"}, done, "both runs finish")
	require.Equal(t, 1, stageDone, "the stage ends once")
	require.Len(t, fp.Triggered, 2)
	require.Empty(t, fp.Stopped)
}

func TestRunSetTriggerErrorFailsThatRunOnly(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-PROV12": provider.StateSuccess})
	fp.TriggerErrs = map[string]error{"PROJ-BUILD": errs.Bamboof("queue full")}
	svc := setService(t, fp)
	svc.Cfg.Project.Sets["release"] = config.Set{Stages: []config.Stage{
		{{Target: "publish-specs"}, {Target: "provision-lab", Vars: config.StringMap{"cluster_name": "a"}}},
	}}
	id, evs := runSet(t, svc)
	var failed SetEvent
	for _, e := range evs {
		if e.Type == SetRunDone && e.Target == "publish-specs" {
			failed = e
		}
	}
	require.Error(t, failed.Err)
	require.Equal(t, provider.StateFailed, failed.State)
	require.Contains(t, kinds(evs), "run_done:0:provision-lab", "the rest of the stage goes on")
	require.Equal(t, provider.StateFailed, evs[len(evs)-1].State)
	rec, _ := svc.Sets.Load(id)
	require.Equal(t, "queue full", rec.Stages[0].Runs[0].Error)
}

func TestRunSetRecordHoldsNoVariableValues(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "envsecret-9f2" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{Vars: []string{"compute_nodes=77731"}})
	require.NoError(t, err)
	id, ch, err := svc.RunSet(context.Background(), p, SetLockOptions{})
	require.NoError(t, err)
	drain(ch)
	data, err := os.ReadFile(filepath.Join(svc.Sets.Dir, id+".json"))
	require.NoError(t, err)
	for _, v := range []string{"77731", "alpha", "envsecret-9f2", "k8s"} {
		require.NotContains(t, string(data), v)
	}
	require.Contains(t, string(data), `"compute_nodes"`, "the name is kept for resume")
}

func TestRunSetThatCannotRecordTriggersNothing(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	svc.Sets.Dir = filepath.Join(file, "sets") // a directory under a file cannot be made
	_, _, err = svc.RunSet(context.Background(), p, SetLockOptions{})
	require.Error(t, err)
	require.Empty(t, fp.Triggered)
}

func TestRunSetHeldLockTriggersNothing(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	held, err := svc.Sets.Lock([]string{"release"}, false, svc.Clock.Now())
	require.NoError(t, err)
	defer held.Release()
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.NoError(t, err)
	_, _, err = svc.RunSet(context.Background(), p, SetLockOptions{})
	require.Equal(t, errs.KindUsage, errs.KindOf(err))
	require.Empty(t, fp.Triggered)
}

func TestRunSetPresetTimeoutFailsTheRunAndStopsNothing(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-PROV12": provider.StateSuccess})
	forever(fp, "PROJ-BUILD")
	svc := setService(t, fp)
	tg := svc.Cfg.Project.Targets["publish-specs"]
	tg.Timeout = config.Duration(time.Minute)
	svc.Cfg.Project.Targets["publish-specs"] = tg
	id, evs := runSet(t, svc)
	require.Empty(t, fp.Stopped)
	require.Len(t, fp.Triggered, 1)
	require.Equal(t, SetDone, evs[len(evs)-1].Type)
	require.Equal(t, provider.StateFailed, evs[len(evs)-1].State)
	rec, _ := svc.Sets.Load(id)
	require.Contains(t, rec.Stages[0].Runs[0].Error, "timed out")
}

func TestRunSetTimeoutEndsWithATimeoutError(t *testing.T) {
	fp := trainBamboo(nil)
	forever(fp, "PROJ-BUILD")
	svc := setService(t, fp)
	s := svc.Cfg.Project.Sets["release"]
	s.Timeout = config.Duration(time.Hour)
	svc.Cfg.Project.Sets["release"] = s
	id, evs := runSet(t, svc)
	last := evs[len(evs)-1]
	require.Equal(t, SetError, last.Type)
	require.Equal(t, errs.KindTimeout, errs.KindOf(last.Err))
	require.Empty(t, fp.Stopped)
	rec, _ := svc.Sets.Load(id)
	require.Equal(t, SetTimedOut, rec.Status)
}

func TestRunSetInterruptLeavesAResumableRecord(t *testing.T) {
	fp := trainBamboo(nil)
	forever(fp, "PROJ-BUILD")
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id, ch, err := svc.RunSet(ctx, p, SetLockOptions{})
	require.NoError(t, err)
	for e := range ch {
		if e.Type == SetRunTriggered {
			cancel()
		}
		require.NotEqual(t, SetDone, e.Type)
	}
	rec, err := svc.Sets.Load(id)
	require.NoError(t, err)
	require.Equal(t, SetInterrupted, rec.Status)
	require.Equal(t, "PROJ-BUILD-1", rec.Stages[0].Runs[0].BuildKey)
	require.Empty(t, rec.Stages[0].Runs[0].State, "the build's end is unknown")
	require.Empty(t, fp.Stopped)
	_, err = os.Stat(filepath.Join(svc.Sets.Dir, "release.lock"))
	require.ErrorIs(t, err, fs.ErrNotExist, "the lock is released")
}
