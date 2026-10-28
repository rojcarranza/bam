package cli

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

// setYAML is projectYAML plus a two-stage set "release".
const setYAML = projectYAML + `  publish-specs:
    plan: PROJ-BUILD
sets:
  release:
    stages:
      - [publish-specs]
      - - target: provision-lab
          vars: {cluster_name: alpha}
`

func setHarness(t *testing.T, final provider.State) *harness {
	t.Helper()
	h := newHarness(t)
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte(setYAML), 0o644))
	h.fake.TriggerResults = map[string]provider.Build{}
	h.fake.Sequences = map[string][]provider.Build{}
	for _, plan := range []string{"PROJ-BUILD", "PROJ-PROV12"} {
		key := plan + "-1"
		h.fake.TriggerResults[plan] = provider.Build{Key: key, PlanKey: plan, State: provider.StateQueued}
		h.fake.Sequences[key] = []provider.Build{{Key: key, PlanKey: plan, State: provider.StateRunning},
			{Key: key, PlanKey: plan, State: final, Duration: 30 * time.Second}}
	}
	return h
}

func TestSetRunExitCodes(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	require.Equal(t, 0, h.run("set", "run", "release"), h.stderr.String())
	require.Contains(t, h.stdout.String(), "set succeeded")
	require.Len(t, h.fake.Triggered, 2)

	h = setHarness(t, provider.StateFailed)
	require.Equal(t, 1, h.run("set", "run", "release"), h.stderr.String())
	require.Contains(t, h.stdout.String(), "try: bam logs PROJ-BUILD-1 --failed")
	require.Len(t, h.fake.Triggered, 1)
}

func TestSetRunDryRunTriggersNothingAndWritesNoRecord(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	require.Equal(t, 0, h.run("set", "run", "release", "--dry-run"), h.stderr.String())
	require.Contains(t, h.stdout.String(), "stage 2/2")
	require.Contains(t, h.stdout.String(), "cluster_name = alpha")
	require.Empty(t, h.fake.Triggered)
	_, err := os.Stat(h.env.Paths.Sets)
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestSetRunJSONIsNDJSON(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	require.Equal(t, 0, h.run("set", "run", "release", "--json"), h.stderr.String())
	lines := strings.Split(strings.TrimSpace(h.stdout.String()), "\n")
	for _, l := range lines {
		require.True(t, json.Valid([]byte(l)), l)
	}
	require.Contains(t, lines[len(lines)-1], `"type":"set_done"`)
}

func TestSetUsageAndConfigExitCodes(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	require.Equal(t, 2, h.run("set", "run", "nope"))
	require.Equal(t, 2, h.run("set", "run", "release", "--var", "novalue"))
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte(setYAML+"  broken:\n    stages: [[missing]]\n"), 0o644))
	require.Equal(t, 3, h.run("set", "list"))
}

func TestUnknownSetSuggestsTheClosest(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	for _, args := range [][]string{{"set", "run", "relase"}, {"set", "show", "relase"}} {
		h.stderr.Reset()
		require.Equal(t, 2, h.run(args...), args)
		require.Contains(t, h.stderr.String(), "sets: release", args)
		require.Contains(t, h.stderr.String(), "try: did you mean release? bam set list shows them all", args)
	}
}

func TestSetRunTimeoutExits6(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	h.fake.Sequences["PROJ-BUILD-1"] = []provider.Build{{Key: "PROJ-BUILD-1", State: provider.StateRunning}}
	yaml := strings.Replace(setYAML, "  release:\n", "  release:\n    timeout: 1h\n", 1)
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte(yaml), 0o644))
	require.Equal(t, 6, h.run("set", "run", "release"))
	require.Contains(t, h.stderr.String(), "bam set resume release-")
	require.Empty(t, h.fake.Stopped)
}

func TestSetRunInterruptedExits130(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Equal(t, 130, h.runCtx(ctx, "set", "run", "release"))
	require.Empty(t, h.fake.Triggered)
}

func TestSetListFiltersByText(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	require.Equal(t, 0, h.run("set", "list", "REL"), h.stderr.String())
	require.Contains(t, h.stdout.String(), "release  2       2")

	h.stdout.Reset()
	require.Equal(t, 0, h.run("set", "list", "Provision"), h.stderr.String())
	require.Contains(t, h.stdout.String(), "release", "matches a preset in the set")

	h.stdout.Reset()
	require.Equal(t, 0, h.run("set", "list", "zzz"))
	require.Empty(t, h.stdout.String())
	require.Contains(t, h.stderr.String(), `no sets match "zzz"`)

	h.stdout.Reset()
	require.Equal(t, 0, h.run("--json", "set", "list", "zzz"))
	require.Equal(t, "[]\n", h.stdout.String())
}

func TestSetListShowStatusResume(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	require.Equal(t, 0, h.run("set", "list"), h.stderr.String())
	require.Contains(t, h.stdout.String(), "release  2       2")
	require.Equal(t, 0, h.run("set", "show", "release"), h.stderr.String())
	require.Contains(t, h.stdout.String(), "stage 2  provision-lab")
	require.Contains(t, h.stdout.String(), "cluster_name")

	// An interrupted run: stage 1 triggered, stage 2 not.
	store := &app.SetStore{Dir: h.env.Paths.Sets}
	id := app.NewSetID("release", fixedNow)
	require.NoError(t, store.Save(app.SetRecord{Version: 1, ID: id, Set: "release", Origin: workOrigin, Repo: h.root,
		StartedAt: fixedNow, Status: app.SetInterrupted, VarNames: []string{},
		Stages: []app.StageRecord{
			{Runs: []app.RunRecord{{Target: "publish-specs", PlanKey: "PROJ-BUILD", BuildKey: "PROJ-BUILD-1"}}},
			{Runs: []app.RunRecord{{Target: "provision-lab", PlanKey: "PROJ-PROV12"}}},
		}}))
	require.Equal(t, 0, h.run("set", "status"), h.stderr.String())
	require.Contains(t, h.stdout.String(), id+"  interrupted")
	require.Contains(t, h.stdout.String(), "try: bam set resume "+id)
	require.Equal(t, 0, h.run("set", "status", "--json"), h.stderr.String())
	require.True(t, json.Valid(h.stdout.Bytes()))

	require.Equal(t, 0, h.run("set", "resume"), h.stderr.String())
	require.Len(t, h.fake.Triggered, 1, "only stage 2 triggers")
	require.Equal(t, "PROJ-PROV12", h.fake.Triggered[0].PlanKey)
	require.Equal(t, 0, h.run("set", "status", "release"), h.stderr.String())
	require.Contains(t, h.stdout.String(), id+"  successful")

	require.Equal(t, 2, h.run("set", "resume"), "nothing left to resume")
}

func TestSetStatusWithNoRuns(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	require.Equal(t, 2, h.run("set", "status"))
	require.Contains(t, h.stderr.String(), "bam set run")
}

func TestSetCompletion(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	h.run("__complete", "set", "run", "")
	require.Contains(t, h.stdout.String(), "release")
}

// failedSetRecord saves a failed run of release: stage 1 done, stage 2's
// provision-lab failed as PROJ-PROV12-1.
func failedSetRecord(t *testing.T, h *harness) string {
	t.Helper()
	store := &app.SetStore{Dir: h.env.Paths.Sets}
	id := app.NewSetID("release", fixedNow)
	require.NoError(t, store.Save(app.SetRecord{Version: 1, ID: id, Set: "release", Origin: workOrigin, Repo: h.root,
		StartedAt: fixedNow, Status: app.SetFailed, VarNames: []string{},
		Stages: []app.StageRecord{
			{Runs: []app.RunRecord{{Target: "publish-specs", PlanKey: "PROJ-BUILD", BuildKey: "PROJ-BUILD-1", State: provider.StateSuccess}}},
			{Runs: []app.RunRecord{{Target: "provision-lab", PlanKey: "PROJ-PROV12", BuildKey: "PROJ-PROV12-1", State: provider.StateFailed}}},
		}}))
	return id
}

// restartsTo makes PROJ-PROV12-1 restart and end in final.
func restartsTo(h *harness, final provider.State) {
	b := func(s provider.State) provider.Build {
		return provider.Build{Key: "PROJ-PROV12-1", PlanKey: "PROJ-PROV12", State: s, Duration: 30 * time.Second}
	}
	h.fake.Sequences["PROJ-PROV12-1"] = []provider.Build{b(provider.StateFailed), b(provider.StateQueued), b(final)}
}

func TestSetRerunFailed(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	id := failedSetRecord(t, h)
	restartsTo(h, provider.StateSuccess)
	require.Equal(t, 0, h.run("set", "rerun", "--failed"), h.stderr.String())
	require.Equal(t, []string{"PROJ-PROV12-1"}, h.fake.Restarted)
	require.Empty(t, h.fake.Triggered)
	require.Contains(t, h.stdout.String(), "provision-lab PROJ-PROV12-1 rerun 1")
	require.Contains(t, h.stdout.String(), "set succeeded")

	require.Equal(t, 0, h.run("set", "status", id, "--json"), h.stderr.String())
	require.Contains(t, h.stdout.String(), `"reruns": 1`)

	require.Equal(t, 2, h.run("set", "rerun", "--failed", id))
	require.Contains(t, h.stderr.String(), "nothing to rerun")
}

func TestSetRerunStillFailingExits1(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	failedSetRecord(t, h)
	restartsTo(h, provider.StateFailed)
	require.Equal(t, 1, h.run("set", "rerun", "--failed", "--json"), h.stderr.String())
	require.Contains(t, h.stdout.String(), `"type":"run_triggered"`)
	require.Contains(t, h.stdout.String(), `"rerun":1`)
}

func TestSetRerunNeedsFailed(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	require.Equal(t, 2, h.run("set", "rerun"))
	require.Contains(t, h.stderr.String(), "rerun needs --failed")
	require.Empty(t, h.connectOpts, "no network before the check")
}

func TestSetRerunWithNoFailedRun(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	require.Equal(t, 2, h.run("set", "rerun", "--failed", "release"))
	require.Contains(t, h.stderr.String(), "no failed run of release to rerun")
	require.Contains(t, h.stderr.String(), "bam set status release")
}

const matrixYAML = projectYAML + `  publish-specs:
    plan: PROJ-BUILD
sets:
  regions:
    stages:
      - - target: provision-lab
          matrix:
            cluster_name: [alpha, beta]
`

func TestSetMatrixRunsEveryEntry(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte(matrixYAML), 0o644))
	require.Equal(t, 0, h.run("set", "show", "regions"), h.stderr.String())
	require.Contains(t, h.stdout.String(), "provision-lab[cluster_name=beta]")
	require.Equal(t, 0, h.run("set", "run", "regions"), h.stderr.String())
	require.Len(t, h.fake.Triggered, 2)
	require.Contains(t, h.stdout.String(), "provision-lab[cluster_name=alpha]")
	require.Equal(t, 0, h.run("set", "status", "--json"), h.stderr.String())
	require.Contains(t, h.stdout.String(), `"name": "provision-lab[cluster_name=beta]"`)
}

// On a terminal, resuming a matrix run draws one row per recorded run name,
// not one per preset left pending next to them.
func TestSetResumeOfAMatrixDrawsOneRowPerRun(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte(matrixYAML), 0o644))
	h.tty = true
	h.vars["NO_COLOR"], h.vars["TERM"] = "1", "dumb"
	h.fake.TriggerResults = map[string]provider.Build{}
	h.fake.Sequences["PROJ-PROV12-1"] = []provider.Build{{Key: "PROJ-PROV12-1", PlanKey: "PROJ-PROV12", State: provider.StateSuccess}}
	h.fake.TriggerResult = provider.Build{Key: "PROJ-PROV12-1", PlanKey: "PROJ-PROV12", State: provider.StateQueued}
	store := &app.SetStore{Dir: h.env.Paths.Sets}
	require.NoError(t, store.Save(app.SetRecord{Version: 1, ID: app.NewSetID("regions", fixedNow), Set: "regions", Origin: workOrigin,
		Repo: h.root, StartedAt: fixedNow, Status: app.SetInterrupted, VarNames: []string{},
		Stages: []app.StageRecord{{Runs: []app.RunRecord{
			{Target: "provision-lab", Name: "provision-lab[cluster_name=alpha]", PlanKey: "PROJ-PROV12"},
			{Target: "provision-lab", Name: "provision-lab[cluster_name=beta]", PlanKey: "PROJ-PROV12"},
		}}}}))
	require.Equal(t, 0, h.run("set", "resume"), h.stderr.String())
	frames := strings.Split(h.stdout.String(), "\x1b[J")
	last := frames[len(frames)-1]
	require.Contains(t, last, "provision-lab[cluster_name=alpha]")
	require.NotRegexp(t, `provision-lab\s+–\s+pending`, last)
}

const hotfixFile = "stages:\n  - [publish-specs]\n  - [{target: provision-lab, vars: {cluster_name: alpha}}]\n"

func trainPath(t *testing.T, h *harness) string {
	t.Helper()
	p := filepath.Join(h.root, "hotfix.yaml")
	require.NoError(t, os.WriteFile(p, []byte(hotfixFile), 0o644))
	return p
}

func TestSetRunFromAFile(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	p := trainPath(t, h)
	require.Equal(t, 0, h.run("set", "run", "-f", p, "--dry-run"), h.stderr.String())
	require.Contains(t, h.stdout.String(), "hotfix")
	require.Empty(t, h.fake.Triggered)

	require.Equal(t, 0, h.run("set", "run", "-f", p), h.stderr.String())
	require.Len(t, h.fake.Triggered, 2)
	require.Equal(t, 0, h.run("set", "status", "hotfix", "--json"), h.stderr.String())
	require.Contains(t, h.stdout.String(), `"file": "`+p+`"`)
	require.Equal(t, 0, h.run("set", "status", "hotfix"), h.stderr.String())
	require.Contains(t, h.stdout.String(), "file "+p)
}

func TestSetRunNameAndFileTogether(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	p := trainPath(t, h)
	require.Equal(t, 2, h.run("set", "run", "release", "-f", p))
	require.Contains(t, h.stderr.String(), "a set name or -f, not both")
	require.Equal(t, 2, h.run("set", "run"))
	require.Contains(t, h.stderr.String(), "a set name or -f")
	require.Empty(t, h.connectOpts, "no network before the check")
}

func TestSetResumeOfAFileSetWithFile(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	p := trainPath(t, h)
	store := &app.SetStore{Dir: h.env.Paths.Sets}
	id := app.NewSetID("hotfix", fixedNow)
	require.NoError(t, store.Save(app.SetRecord{Version: 1, ID: id, Set: "hotfix", File: filepath.Join(h.root, "gone.yaml"),
		Origin: workOrigin, Repo: h.root, StartedAt: fixedNow, Status: app.SetInterrupted, VarNames: []string{},
		Stages: []app.StageRecord{
			{Runs: []app.RunRecord{{Target: "publish-specs", PlanKey: "PROJ-BUILD", BuildKey: "PROJ-BUILD-1"}}},
			{Runs: []app.RunRecord{{Target: "provision-lab", PlanKey: "PROJ-PROV12"}}},
		}}))
	require.Equal(t, 3, h.run("set", "resume", id))
	require.Contains(t, h.stderr.String(), "is gone")
	require.Equal(t, 0, h.run("set", "resume", id, "--file", p), h.stderr.String())
	require.Len(t, h.fake.Triggered, 1)
}

func TestSetStatusOfAnUnknownSetSuggestsTheClosest(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	for _, args := range [][]string{{"set", "status", "relase"}, {"set", "resume", "relase"}, {"set", "rerun", "--failed", "relase"}} {
		require.Equal(t, 2, h.run(args...), args)
		require.Equal(t, "error: unknown set \"relase\"\n  sets: release\n  try: did you mean release? bam set list shows them all\n", h.stderr.String(), args)
	}
	// A configured set that never ran keeps its own message.
	require.Equal(t, 2, h.run("set", "status", "release"))
	require.Contains(t, h.stderr.String(), `no set runs in this repository for "release"`)
}

func TestSetRunVarCompletesThePresetVariables(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	h.run("__complete", "set", "run", "release", "--var", "")
	require.Equal(t, "cluster_name=\ncluster_type=\n:6\n", h.stdout.String())
	h.run("__complete", "set", "run", "release", "--var", "cluster_type=")
	require.Equal(t, "cluster_type=k8s\ncluster_type=dcos\n:4\n", h.stdout.String())
	h.run("__complete", "set", "run", "nope", "--var", "")
	require.Equal(t, ":4\n", h.stdout.String())
}

func TestSetResumeAndRerunVarCompleteTheRecordedPresetVariables(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	id := failedSetRecord(t, h)
	for _, cmd := range []string{"resume", "rerun"} {
		h.run("__complete", "set", cmd, "--var", "")
		require.Equal(t, "cluster_name=\ncluster_type=\n:6\n", h.stdout.String(), cmd)
		h.run("__complete", "set", cmd, id, "--var", "cluster_type=")
		require.Equal(t, "cluster_type=k8s\ncluster_type=dcos\n:4\n", h.stdout.String(), cmd)
		h.run("__complete", "set", cmd, "nope", "--var", "")
		require.Equal(t, ":4\n", h.stdout.String(), cmd)
	}
}

func TestSetHistoryListsThisRepositorysRuns(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	require.Equal(t, 0, h.run("set", "history"))
	require.Contains(t, h.stderr.String(), "no set runs in this repository")
	require.Equal(t, 2, h.run("set", "history", "relase"))
	require.Contains(t, h.stderr.String(), "did you mean release?")

	id := failedSetRecord(t, h)
	other := app.SetRecord{Version: 1, ID: app.NewSetID("release", fixedNow.Add(-time.Hour)), Set: "release", Origin: workOrigin,
		Repo: "/elsewhere", StartedAt: fixedNow.Add(-time.Hour), Status: app.SetSuccessful}
	require.NoError(t, (&app.SetStore{Dir: h.env.Paths.Sets}).Save(other))
	require.Equal(t, 0, h.run("set", "history", "release"))
	require.Regexp(t, id+`\s+failed\s+\S+\s+–\s+1/2 ✓`, h.stdout.String())
	require.NotContains(t, h.stdout.String(), other.ID, "another repository's run")

	require.Equal(t, 0, h.run("set", "history", "--json"))
	var docs []map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &docs))
	require.Len(t, docs, 1)
	require.Equal(t, id, docs[0]["id"])
	require.Equal(t, "failed", docs[0]["status"])

	newer := app.SetRecord{Version: 1, ID: app.NewSetID("release", fixedNow.Add(time.Hour)), Set: "release", Origin: workOrigin,
		Repo: h.root, StartedAt: fixedNow.Add(time.Hour), Status: app.SetSuccessful}
	require.NoError(t, (&app.SetStore{Dir: h.env.Paths.Sets}).Save(newer))
	require.Equal(t, 0, h.run("set", "history", "-n", "1"))
	require.Contains(t, h.stdout.String(), newer.ID)
	require.NotContains(t, h.stdout.String(), id, "older run past --limit")
	require.Equal(t, 2, h.run("set", "history", "--limit", "0"))
	require.Contains(t, h.stderr.String(), "--limit takes 1 or more")
}
