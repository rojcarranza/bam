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

// saveRecord stores a record of the set "release" for svc's repository.
func saveRecord(t *testing.T, svc *Service, rec SetRecord) SetRecord {
	t.Helper()
	rec.Version, rec.Set, rec.Repo = 1, "release", svc.Cfg.RepoRoot
	if rec.Origin == "" {
		rec.Origin = svc.Origin
	}
	if rec.Status == "" {
		rec.Status = SetInterrupted
	}
	rec.ID = NewSetID("release", svc.Clock.Now())
	rec.StartedAt = svc.Clock.Now()
	if rec.VarNames == nil {
		rec.VarNames = []string{}
	}
	require.NoError(t, svc.Sets.Save(rec))
	return rec
}

func twoStages(firstKey string) []StageRecord {
	return []StageRecord{
		{Runs: []RunRecord{{Target: "publish-specs", PlanKey: "PROJ-BUILD", BuildKey: firstKey}}},
		{Runs: []RunRecord{{Target: "provision-lab", PlanKey: "PROJ-PROV12"}}},
	}
}

func TestResumeWatchesKeyedRunsAndTriggersTheRest(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Stages: twoStages("PROJ-BUILD-1")})
	ch, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Len(t, fp.Triggered, 1, "only the run that never triggered")
	require.Equal(t, "PROJ-PROV12", fp.Triggered[0].PlanKey)
	require.Equal(t, []string{
		"stage_start:0:", "run_done:0:publish-specs", "stage_done:0:",
		"stage_start:1:", "run_triggered:1:provision-lab", "run_done:1:provision-lab", "stage_done:1:",
		"set_done:1:",
	}, kinds(evs))
	for _, e := range evs {
		require.Equal(t, rec.ID, e.ID, "resume keeps the id")
	}
	got, _ := svc.Sets.Load(rec.ID)
	require.Equal(t, SetSuccessful, got.Status)
}

func TestResumeSkipsStagesThatSucceeded(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	stages := twoStages("PROJ-BUILD-1")
	stages[0].Runs[0].State = provider.StateSuccess
	rec := saveRecord(t, svc, SetRecord{Status: SetTimedOut, Stages: stages})
	ch, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Equal(t, "stage_start:1:", kinds(evs)[0])
	require.Equal(t, provider.StateSuccess, evs[len(evs)-1].State)
}

func TestResumeWhenTheStageFinishedWhileAway(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-PROV12": provider.StateSuccess})
	fp.Sequences["PROJ-BUILD-1"] = []provider.Build{{Key: "PROJ-BUILD-1", State: provider.StateSuccess}}
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Stages: twoStages("PROJ-BUILD-1")})
	ch, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Contains(t, kinds(evs), "run_done:0:publish-specs")
	require.Contains(t, kinds(evs), "run_triggered:1:provision-lab")
	require.Equal(t, SetDone, evs[len(evs)-1].Type)
}

func TestResumeRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		rec  SetRecord
		kind errs.Kind
		want string
	}{
		"failed":       {SetRecord{Status: SetFailed, Stages: twoStages("")}, errs.KindUsage, "bam set rerun --failed"},
		"successful":   {SetRecord{Status: SetSuccessful, Stages: twoStages("")}, errs.KindUsage, "nothing to resume"},
		"missing var":  {SetRecord{Stages: twoStages(""), VarNames: []string{"env"}}, errs.KindUsage, "--var env="},
		"changed set":  {SetRecord{Stages: twoStages("")[:1]}, errs.KindConfig, "changed since"},
		"other origin": {SetRecord{Stages: twoStages(""), Origin: "https://bamboo.lab.example"}, errs.KindConfig, "bamboo.lab.example"},
	} {
		t.Run(name, func(t *testing.T) {
			fp := trainBamboo(nil)
			svc := setService(t, fp)
			svc.Getenv = func(string) string { return "x" }
			rec := saveRecord(t, svc, tc.rec)
			_, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
			require.Error(t, err)
			require.Equal(t, tc.kind, errs.KindOf(err), "%v", err)
			require.Contains(t, err.Error()+" "+errTry(err), tc.want)
			require.Empty(t, fp.Triggered)
		})
	}
}

func TestResumeWhileRunningIsRefusedByTheLock(t *testing.T) {
	fp := trainBamboo(nil)
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Status: SetRunning, Stages: twoStages("")})
	held, err := svc.Sets.Lock([]string{rec.ID}, false, svc.Clock.Now())
	require.NoError(t, err)
	defer held.Release()
	_, err = svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.Equal(t, errs.KindUsage, errs.KindOf(err))
	require.Contains(t, err.Error(), "in progress")
}

func TestSetStatusFetchesLiveBuilds(t *testing.T) {
	fp := trainBamboo(nil)
	forever(fp, "PROJ-BUILD")
	svc := setService(t, fp)
	rec := saveRecord(t, svc, SetRecord{Stages: twoStages("PROJ-BUILD-1")})
	got, builds, err := svc.SetStatus(context.Background(), rec.ID)
	require.NoError(t, err)
	require.Equal(t, rec.ID, got.ID)
	require.Equal(t, provider.StateRunning, builds["PROJ-BUILD-1"].State)
	require.Len(t, builds, 1)
	require.Empty(t, fp.Triggered)
}

func TestDescribeSetMasksSecrets(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Cfg.Project.Sets["release"].Stages[1][0].Vars["db_password"] = "${LAB_DB_PASSWORD}"
	info, err := DescribeSet(svc.Cfg, func(string) string { return "" }, "release")
	require.NoError(t, err)
	require.Equal(t, "release", info.Name)
	require.Equal(t, "publish-specs", info.Stages[0][0].Target)
	require.Equal(t, "provision-lab", info.Stages[1][0].Target)
	var names []string
	for _, v := range info.Stages[1][0].Vars {
		names = append(names, v.Name)
		if v.Name == "db_password" {
			require.True(t, v.Secret)
			require.Equal(t, "LAB_DB_PASSWORD", v.EnvRef)
			require.Equal(t, "${LAB_DB_PASSWORD} (unset)", v.Display())
		}
	}
	require.Equal(t, []string{"cluster_name", "compute_nodes", "db_password"}, names, "sorted by name")
	all, err := DescribeSets(svc.Cfg, func(string) string { return "" })
	require.NoError(t, err)
	require.Len(t, all, 1)
	_, err = DescribeSet(svc.Cfg, func(string) string { return "" }, "nope")
	require.Equal(t, errs.KindUsage, errs.KindOf(err))
}

// gatedProvider holds GetBuild of one build until gate closes, so a test can
// act between a trigger and that build's end.
type gatedProvider struct {
	*fake.Provider
	key  string
	gate chan struct{}
}

func (g gatedProvider) GetBuild(ctx context.Context, key string) (provider.Build, error) {
	if key == g.key {
		<-g.gate
	}
	return g.Provider.GetBuild(ctx, key)
}

func TestRunSetRecordFailureMidStageEndsTheSet(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	fp := trainBamboo(map[string]provider.State{"PROJ-PROV12": provider.StateSuccess})
	forever(fp, "PROJ-BUILD")
	gate := make(chan struct{})
	svc := setService(t, fp)
	svc.P = gatedProvider{Provider: fp, key: "PROJ-PROV12-1", gate: gate}
	svc.Getenv = func(string) string { return "x" }
	svc.Cfg.Project.Sets["release"] = config.Set{Stages: []config.Stage{
		{{Target: "publish-specs"}, {Target: "provision-lab", Vars: config.StringMap{"cluster_name": "a"}}},
	}}
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.NoError(t, err)
	_, ch, err := svc.RunSet(context.Background(), p, SetLockOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chmod(svc.Sets.Dir, 0o700) })
	var last SetEvent
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range ch {
			if e.Type == SetRunTriggered && e.Target == "provision-lab" {
				require.NoError(t, os.Chmod(svc.Sets.Dir, 0o500)) // the next save fails
				close(gate)
			}
			last = e
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the set never ended: a watcher of the stage kept it open")
	}
	require.Equal(t, SetError, last.Type)
	require.Empty(t, fp.Stopped)
}

// failedRecord is release with stage 1 done and stage 2's run failed as
// PROJ-PROV12-1.
func failedRecord() []StageRecord {
	return []StageRecord{
		{Runs: []RunRecord{{Target: "publish-specs", PlanKey: "PROJ-BUILD", BuildKey: "PROJ-BUILD-1", State: provider.StateSuccess}}},
		{Runs: []RunRecord{{Target: "provision-lab", PlanKey: "PROJ-PROV12", BuildKey: "PROJ-PROV12-1", State: provider.StateFailed}}},
	}
}

// restartable lets key restart: Restart reads the old failed result, then
// the build is queued, then it ends in final.
func restartable(p *fake.Provider, key string, final provider.State) {
	p.Sequences[key] = []provider.Build{
		{Key: key, State: provider.StateFailed},
		{Key: key, State: provider.StateQueued},
		{Key: key, State: final},
	}
}

func TestRerunRestartsTheFailedRunAndFinishesTheTrain(t *testing.T) {
	fp := trainBamboo(nil)
	restartable(fp, "PROJ-PROV12-1", provider.StateSuccess)
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: failedRecord()})
	ch, err := svc.RerunSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Equal(t, []string{"PROJ-PROV12-1"}, fp.Restarted)
	require.Empty(t, fp.Triggered, "a restart is not a trigger, and stage 1 is left alone")
	require.Equal(t, []string{
		"stage_start:1:", "run_triggered:1:provision-lab", "run_done:1:provision-lab", "stage_done:1:", "set_done:1:",
	}, kinds(evs))
	for _, e := range evs {
		require.Equal(t, rec.ID, e.ID, "rerun keeps the id")
		if e.Type == SetRunTriggered {
			require.Equal(t, 1, e.Rerun)
		}
	}
	got, _ := svc.Sets.Load(rec.ID)
	require.Equal(t, SetSuccessful, got.Status)
	require.Equal(t, 1, got.Stages[1].Runs[0].Reruns)
	require.Equal(t, provider.StateSuccess, got.Stages[1].Runs[0].State)
	require.Equal(t, "PROJ-PROV12-1", got.Stages[1].Runs[0].BuildKey, "the same build")
	require.Equal(t, provider.StateSuccess, got.Stages[0].Runs[0].State)
}

func TestRerunContinuesIntoTheStagesThatNeverRan(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-PROV12": provider.StateSuccess})
	restartable(fp, "PROJ-BUILD-1", provider.StateSuccess)
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	stages := twoStages("PROJ-BUILD-1")
	stages[0].Runs[0].State = provider.StateFailed
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: stages})
	ch, err := svc.RerunSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Equal(t, []string{"PROJ-BUILD-1"}, fp.Restarted)
	require.Len(t, fp.Triggered, 1)
	require.Equal(t, "PROJ-PROV12", fp.Triggered[0].PlanKey)
	require.Equal(t, provider.StateSuccess, evs[len(evs)-1].State)
}

func TestRerunTriggersARunThatNeverTriggered(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	stages := failedRecord()
	stages[1].Runs[0].BuildKey, stages[1].Runs[0].Error = "", "queue full"
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: stages})
	ch, err := svc.RerunSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	drain(ch)
	require.Empty(t, fp.Restarted)
	require.Len(t, fp.Triggered, 1)
	got, _ := svc.Sets.Load(rec.ID)
	require.Equal(t, SetSuccessful, got.Status)
	require.Equal(t, 0, got.Stages[1].Runs[0].Reruns, "a fresh trigger is not a rerun")
	require.Equal(t, "", got.Stages[1].Runs[0].Error)
}

// restartSpy notes how many reruns the record held when Bamboo was asked.
type restartSpy struct {
	*fake.Provider
	load func() SetRecord
	seen []int
}

func (r *restartSpy) RestartBuild(ctx context.Context, key string) (provider.Build, error) {
	r.seen = append(r.seen, r.load().Stages[1].Runs[0].Reruns)
	return r.Provider.RestartBuild(ctx, key)
}

func TestRerunSavesTheCountBeforeRestarting(t *testing.T) {
	fp := trainBamboo(nil)
	restartable(fp, "PROJ-PROV12-1", provider.StateSuccess)
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: failedRecord()})
	spy := &restartSpy{Provider: fp, load: func() SetRecord { r, _ := svc.Sets.Load(rec.ID); return r }}
	svc.P = spy
	ch, err := svc.RerunSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	drain(ch)
	require.Equal(t, []int{1}, spy.seen)
}

func TestRerunThatCannotRestartFailsTheRun(t *testing.T) {
	fp := trainBamboo(nil)
	fp.Sequences["PROJ-PROV12-1"] = []provider.Build{{Key: "PROJ-PROV12-1", State: provider.StateFailed}}
	fp.RestartErr = errs.Bamboof("this Bamboo cannot restart a build over REST").Wrap(errs.ErrUnsupported)
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: failedRecord()})
	ch, err := svc.RerunSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	last := evs[len(evs)-1]
	require.Equal(t, SetDone, last.Type)
	require.Equal(t, provider.StateFailed, last.State)
	got, _ := svc.Sets.Load(rec.ID)
	require.Equal(t, SetFailed, got.Status)
	require.Contains(t, got.Stages[1].Runs[0].Error, "cannot restart")
	require.Equal(t, 1, got.Stages[1].Runs[0].Reruns)
	require.Equal(t, "PROJ-PROV12-1", got.Stages[1].Runs[0].BuildKey)
}

func TestRerunRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		rec  SetRecord
		kind errs.Kind
		want string
	}{
		"successful":  {SetRecord{Status: SetSuccessful, Stages: failedRecord()}, errs.KindUsage, "nothing to rerun"},
		"interrupted": {SetRecord{Status: SetInterrupted, Stages: failedRecord()}, errs.KindUsage, "bam set resume"},
		"timed out":   {SetRecord{Status: SetTimedOut, Stages: failedRecord()}, errs.KindUsage, "was timed out, not failed"},
		"missing var": {SetRecord{Status: SetFailed, Stages: failedRecord(), VarNames: []string{"env"}}, errs.KindUsage, "--var env="},
		"changed set": {SetRecord{Status: SetFailed, Stages: failedRecord()[:1]}, errs.KindConfig, "changed since"},
	} {
		t.Run(name, func(t *testing.T) {
			fp := trainBamboo(nil)
			svc := setService(t, fp)
			svc.Getenv = func(string) string { return "x" }
			rec := saveRecord(t, svc, tc.rec)
			_, err := svc.RerunSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
			require.Error(t, err)
			require.Equal(t, tc.kind, errs.KindOf(err), "%v", err)
			require.Contains(t, err.Error()+" "+errTry(err), tc.want)
			require.Empty(t, fp.Restarted)
			require.Empty(t, fp.Triggered)
		})
	}
}

// A failed run whose build actually succeeded (its watch failed, say) is
// taken as it is: no restart, no rerun counted, and the train goes on.
func TestRerunAdoptsABuildThatSucceededAfterAll(t *testing.T) {
	fp := trainBamboo(nil)
	fp.Sequences["PROJ-PROV12-1"] = []provider.Build{{Key: "PROJ-PROV12-1", State: provider.StateSuccess}}
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	stages := failedRecord()
	stages[1].Runs[0].Error = "polling failed"
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: stages})
	ch, err := svc.RerunSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Empty(t, fp.Restarted)
	require.NotContains(t, kinds(evs), "run_triggered:1:provision-lab")
	got, _ := svc.Sets.Load(rec.ID)
	require.Equal(t, SetSuccessful, got.Status)
	require.Equal(t, 0, got.Stages[1].Runs[0].Reruns)
}

// matrixService is setService with release's second stage expanded over
// cluster_name: alpha and beta.
func matrixService(t *testing.T, p *fake.Provider) *Service {
	svc := setService(t, p)
	svc.Cfg.Project.Sets["release"] = config.Set{Stages: []config.Stage{
		{{Target: "publish-specs"}},
		{{Target: "provision-lab", Vars: config.StringMap{"compute_nodes": "4"},
			Matrix: &config.Matrix{Sets: []config.StringMap{{"cluster_name": "alpha"}, {"cluster_name": "beta"}}}}},
	}}
	return svc
}

func TestPlanSetExpandsAMatrix(t *testing.T) {
	svc := matrixService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.NoError(t, err)
	require.Len(t, p.Stages[1], 2)
	require.Equal(t, "provision-lab[cluster_name=alpha]", p.Stages[1][0].Name)
	require.Equal(t, "provision-lab", p.Stages[1][0].Target)
	require.Equal(t, "publish-specs", p.Stages[0][0].Name, "a run without a matrix is named after its preset")
	v, _ := p.Stages[1][1].Vars.Get("cluster_name")
	require.Equal(t, "beta", v.Value)
}

func TestPlanSetMatrixPrecedence(t *testing.T) {
	svc := matrixService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{Vars: []string{"cluster_name=gamma"}})
	require.NoError(t, err)
	for _, r := range p.Stages[1] {
		v, _ := r.Vars.Get("cluster_name")
		require.Equal(t, "gamma", v.Value, "--var beats the matrix")
		n, _ := r.Vars.Get("compute_nodes")
		require.Equal(t, "4", n.Value, "the set run's vars still apply")
	}
}

func TestRunSetMatrixRecordsAndReportsEveryRun(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := matrixService(t, fp)
	id, evs := runSet(t, svc)
	require.Len(t, fp.Triggered, 3)
	rec, _ := svc.Sets.Load(id)
	require.Equal(t, "provision-lab[cluster_name=alpha]", rec.Stages[1].Runs[0].Name)
	require.Equal(t, "provision-lab[cluster_name=beta]", rec.Stages[1].Runs[1].Name)
	var runs []string
	for _, e := range evs {
		if e.Type == SetRunTriggered {
			runs = append(runs, e.Run)
		}
	}
	require.Equal(t, []string{"publish-specs", "provision-lab[cluster_name=alpha]", "provision-lab[cluster_name=beta]"}, runs)
}

func TestRerunRestartsOnlyTheFailedMatrixRun(t *testing.T) {
	fp := trainBamboo(nil)
	restartable(fp, "PROJ-PROV12-2", provider.StateSuccess)
	svc := matrixService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: []StageRecord{
		{Runs: []RunRecord{{Target: "publish-specs", Name: "publish-specs", PlanKey: "PROJ-BUILD", BuildKey: "PROJ-BUILD-1", State: provider.StateSuccess}}},
		{Runs: []RunRecord{
			{Target: "provision-lab", Name: "provision-lab[cluster_name=alpha]", PlanKey: "PROJ-PROV12", BuildKey: "PROJ-PROV12-1", State: provider.StateSuccess},
			{Target: "provision-lab", Name: "provision-lab[cluster_name=beta]", PlanKey: "PROJ-PROV12", BuildKey: "PROJ-PROV12-2", State: provider.StateFailed},
		}},
	}})
	ch, err := svc.RerunSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	drain(ch)
	require.Equal(t, []string{"PROJ-PROV12-2"}, fp.Restarted)
	got, _ := svc.Sets.Load(rec.ID)
	require.Equal(t, SetSuccessful, got.Status)
}

func TestResumeAnOldRecordWithoutNames(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Stages: twoStages("PROJ-BUILD-1")})
	require.Empty(t, rec.Stages[0].Runs[0].Name, "written before run names existed")
	ch, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Equal(t, provider.StateSuccess, evs[len(evs)-1].State)
}

func TestResumeRefusesAChangedMatrix(t *testing.T) {
	svc := matrixService(t, trainBamboo(nil))
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Stages: []StageRecord{
		{Runs: []RunRecord{{Target: "publish-specs", PlanKey: "PROJ-BUILD", BuildKey: "PROJ-BUILD-1"}}},
		{Runs: []RunRecord{
			{Target: "provision-lab", Name: "provision-lab[cluster_name=alpha]", PlanKey: "PROJ-PROV12"},
			{Target: "provision-lab", Name: "provision-lab[cluster_name=gamma]", PlanKey: "PROJ-PROV12"},
		}},
	}})
	_, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.Equal(t, errs.KindConfig, errs.KindOf(err))
	require.Contains(t, err.Error(), "changed since")
}

func TestDescribeSetShowsTheMatrixAndItsRuns(t *testing.T) {
	svc := matrixService(t, fakeBamboo())
	info, err := DescribeSet(svc.Cfg, func(string) string { return "" }, "release")
	require.NoError(t, err)
	run := info.Stages[1][0]
	require.NotNil(t, run.Matrix)
	require.Equal(t, "2 variable sets", run.MatrixLine)
	require.Empty(t, info.Stages[0][0].MatrixLine)
	require.Equal(t, []string{"provision-lab[cluster_name=alpha]", "provision-lab[cluster_name=beta]"}, run.Runs)
	require.Equal(t, []string{"publish-specs"}, info.Stages[0][0].Runs)
}
