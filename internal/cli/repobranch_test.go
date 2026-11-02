package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #130: bam run --repo-branch sets a repository's branch on the run
// and carries it into the child plans, through plan branches. The parent's
// last build PROJ-PARENT-41 triggered PROJ-CHILD-7; both build repo-a as
// their default repository.
func repoHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.fake.Plans["PROJ"] = append(h.fake.Plans["PROJ"],
		provider.Plan{Key: "PROJ-PARENT", Name: "Parent", ProjectKey: "PROJ", URL: workOrigin + "/browse/PROJ-PARENT"},
		provider.Plan{Key: "PROJ-CHILD", Name: "Child", ProjectKey: "PROJ", URL: workOrigin + "/browse/PROJ-CHILD"})
	built := func(key, plan string, n int, reason, commit string, ago time.Duration) provider.Build {
		return provider.Build{Key: key, URL: workOrigin + "/browse/" + key, PlanKey: plan, Number: n, State: provider.StateSuccess,
			Reason: reason, StartedAt: fixedNow.Add(-ago), Duration: time.Minute,
			Revision: commit, Revisions: []provider.Revision{{Repository: "repo-a", Revision: commit}}}
	}
	h.fake.History["PROJ-PARENT"] = []provider.Build{built("PROJ-PARENT-41", "PROJ-PARENT", 41, "Manual run by jdoe", "9b30b9a", 2*time.Hour)}
	h.fake.History["PROJ-CHILD"] = []provider.Build{built("PROJ-CHILD-7", "PROJ-CHILD", 7, "Child of PROJ-PARENT-41", "4c2d1e0", 110*time.Minute)}
	h.fake.TriggerResult = queuedBuild("PROJ-PARENT1-1")
	return h
}

var repoFlags = []string{"--repo-branch", "repo-a=feature/PROJ-101", "--repo-branch", "PROJ-CHILD:repo-a=feature/PROJ-101-extra"}

func TestRunRepoBranchDryRunCreatesAndTriggersNothing(t *testing.T) {
	h := repoHarness(t)
	require.Equal(t, 0, h.run(append([]string{"run", "PROJ-PARENT", "--dry-run"}, repoFlags...)...), h.stderr.String())
	assert.Equal(t, ""+
		"Plan     PROJ-PARENT  (branch feature-PROJ-101)\n"+
		"Branches feature-PROJ-101 on 2 plans\n"+
		"  PROJ-PARENT  repo-a  feature/PROJ-101        create\n"+
		"  PROJ-CHILD   repo-a  feature/PROJ-101-extra  create\n"+
		"Vars     none\n", h.stdout.String())
	assert.Contains(t, h.stderr.String(), "dry run: nothing triggered")
	assert.Empty(t, h.fake.Created)
	assert.Empty(t, h.fake.Triggered)

	require.Equal(t, 0, h.run(append([]string{"run", "PROJ-PARENT", "--dry-run", "--json"}, repoFlags...)...), h.stderr.String())
	var doc struct {
		Branch       string           `json:"branch"`
		RepoBranches []map[string]any `json:"repo_branches"`
	}
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "feature-PROJ-101", doc.Branch)
	require.Len(t, doc.RepoBranches, 2)
	assert.Equal(t, map[string]any{"plan_key": "PROJ-CHILD", "branch_key": "", "branch": "feature-PROJ-101",
		"repository": "repo-a", "vcs_branch": "feature/PROJ-101-extra", "existing": false}, doc.RepoBranches[1])
}

func TestRunRepoBranchCreatesTheBranchesThenRunsTheParentsBranch(t *testing.T) {
	h := repoHarness(t)
	require.Equal(t, 0, h.run(append([]string{"run", "PROJ-PARENT"}, repoFlags...)...), h.stderr.String())
	assert.Equal(t, []fake.CreatedBranch{
		{PlanKey: "PROJ-CHILD", Name: "feature-PROJ-101", VCSBranch: "feature/PROJ-101-extra"},
		{PlanKey: "PROJ-PARENT", Name: "feature-PROJ-101", VCSBranch: "feature/PROJ-101"},
	}, h.fake.Created, "the child's branch exists before the parent's can trigger it")
	require.Len(t, h.fake.Triggered, 1)
	assert.Equal(t, "PROJ-PARENT1", h.fake.Triggered[0].PlanKey)
	out := h.stdout.String()
	assert.Contains(t, out, "✓ Created  PROJ-CHILD1    plan branch feature-PROJ-101 of PROJ-CHILD\n")
	assert.Contains(t, out, "✓ Created  PROJ-PARENT1   plan branch feature-PROJ-101 of PROJ-PARENT\n")
	assert.Contains(t, out, "bam changed no plan, so nothing needs restoring; the plan branches stay for the next run.")
	assert.Contains(t, out, "A child plan starts on feature-PROJ-101 only when its parent plan's dependencies are enabled for branches.")
	assert.Contains(t, out, "✓ Queued   PROJ-PARENT1-1")

	// The next run reuses them and creates nothing.
	h.fake.Created = nil
	require.Equal(t, 0, h.run(append([]string{"run", "PROJ-PARENT", "--json"}, repoFlags...)...), h.stderr.String())
	assert.Empty(t, h.fake.Created)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &raw))
	assert.Equal(t, "PROJ-PARENT1", raw["plan_key"])
	assert.Contains(t, raw["warnings"], "plan branch feature-PROJ-101 already exists on PROJ-PARENT1, PROJ-CHILD1; bam reuses it as it is and cannot check which branch it builds; to build another branch there, delete it in Bamboo first")
}

func TestRunRepoBranchFlagBeatsThePreset(t *testing.T) {
	h := repoHarness(t)
	preset := projectYAML + `  feature-101:
    plan: PROJ-PARENT
    repo_branches:
      repo-a: feature/old
      PROJ-CHILD:repo-a: feature/PROJ-101-extra
`
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte(preset), 0o644))
	require.Equal(t, 0, h.run("run", "feature-101", "--repo-branch", "repo-a=feature/PROJ-101", "--dry-run"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "  PROJ-PARENT  repo-a  feature/PROJ-101        create\n")
	assert.Contains(t, h.stdout.String(), "  PROJ-CHILD   repo-a  feature/PROJ-101-extra  create\n")

	require.Equal(t, 0, h.run("run", "feature-101", "--dry-run"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "Plan     PROJ-PARENT  (branch feature-old)\n", "without the flag the preset's entry for every plan names the branch")
}

func TestRunRepoBranchUsageErrors(t *testing.T) {
	h := repoHarness(t)
	assert.Equal(t, 2, h.run("run", "PROJ-PARENT", "--repo-branch", "repo-a"))
	assert.Contains(t, h.stderr.String(), `--repo-branch "repo-a" is not REPO=BRANCH or PLAN:REPO=BRANCH`)
	assert.Equal(t, 2, h.run("run", "PROJ-PARENT", "--repo-branch", "repo-a=x", "--branch", "develop"))
	assert.Contains(t, h.stderr.String(), "--repo-branch cannot be combined with --branch")
	assert.Empty(t, h.connectOpts, "checked before connecting to Bamboo")

	assert.Equal(t, 2, h.run("run", "PROJ-PARENT", "--repo-branch", "repo-z=feature/x"))
	assert.Contains(t, h.stderr.String(), "no plan of this run builds repo-z")
	assert.Empty(t, h.fake.Created)
	assert.Empty(t, h.fake.Triggered)
}
