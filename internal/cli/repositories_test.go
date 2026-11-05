package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BAW-7 (direction B). The preset deploy-tst runs the plan branch develop of
// PROJ-PROV (PROJ-PROV12) and wants infra-scripts on feature/cli-tools.
const repoPresetYAML = projectYAML + `  deploy-tst:
    plan: PROJ-PROV
    branch: develop
    defaults: {cluster_name: a}
    repositories:
      infra-scripts: "feature/cli-tools"
`

const tabURL = workOrigin + "/branch/admin/config/configureChainBranchRepositories.action?planKey=PROJ-PROV12"

// repoRun is a harness whose plan branch last built infra-scripts on
// lastBuilt (and infra-base on develop), in a finished job.
func repoRun(t *testing.T, lastBuilt string) *harness {
	t.Helper()
	h := newHarness(t)
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte(repoPresetYAML), 0o644))
	b := h.fake.History["PROJ-PROV12"][0]
	b.Stages = []provider.Stage{{Name: "Apply", Jobs: []provider.Job{{Key: "PROJ-PROV12-TF-8", Name: "Terraform",
		Branches: map[string]string{"infra-base": "develop", "infra-scripts": lastBuilt}}}}}
	h.fake.History["PROJ-PROV12"][0] = b
	h.fake.TriggerResult = queuedBuild("PROJ-PROV12-9")
	return h
}

func TestRunWithTheWantedBranchTriggersWithoutAQuestion(t *testing.T) {
	h := repoRun(t, "feature/cli-tools")
	require.Equal(t, 0, h.run("run", "deploy-tst"), h.stderr.String())
	assert.Empty(t, h.opened)
	require.Len(t, h.fake.Triggered, 1)
	assert.Equal(t, "PROJ-PROV12", h.fake.Triggered[0].PlanKey)
	assert.NotContains(t, h.stdout.String(), "Repositories tab")
}

func TestRunWithAnotherBranchOpensTheTabAndTriggersAfterEnter(t *testing.T) {
	h := repoRun(t, "develop")
	h.tty, h.stdin = true, "\n"
	require.Equal(t, 0, h.run("run", "deploy-tst"), h.stderr.String())
	assert.Equal(t, []string{tabURL}, h.opened)
	assert.Contains(t, h.stdout.String(), "infra-scripts  develop → feature/cli-tools")
	assert.Contains(t, h.stdout.String(), "Repositories tab: ")
	assert.Contains(t, h.stdout.String(), tabURL)
	assert.NotContains(t, h.stdout.String(), "infra-base", "only the repositories the preset names")
	assert.Contains(t, h.stderr.String(), "press Enter")
	require.Len(t, h.fake.Triggered, 1)
}

func TestRunWithAnotherBranchStopsWithoutATerminal(t *testing.T) {
	for _, extra := range [][]string{nil, {"--json"}} {
		h := repoRun(t, "develop")
		require.Equal(t, 2, h.run(append([]string{"run", "deploy-tst"}, extra...)...), extra)
		assert.Empty(t, h.fake.Triggered)
		assert.Empty(t, h.opened)
		assert.Contains(t, h.stderr.String(), tabURL, "the error carries the link")
	}
}

func TestRunDryRunPrintsTheBranchesAndOpensNothing(t *testing.T) {
	h := repoRun(t, "develop")
	h.tty = true
	require.Equal(t, 0, h.run("run", "deploy-tst", "--dry-run"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "develop → feature/cli-tools")
	assert.Empty(t, h.opened)
	assert.Empty(t, h.fake.Triggered)

	require.Equal(t, 0, h.run("run", "deploy-tst", "--dry-run", "--json"), h.stderr.String())
	var doc struct {
		RepoBranches []any             `json:"repo_branches"`
		Repositories map[string]string `json:"repositories"`
	}
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Empty(t, doc.RepoBranches)
	assert.Equal(t, map[string]string{"infra-scripts": "feature/cli-tools"}, doc.Repositories)
}

// BAW-7 answer 5: --repository beats the preset's entry, so a plan branch
// that already builds the preset's branch is asked to switch.
func TestRunRepositoryFlagBeatsThePresetsEntry(t *testing.T) {
	h := repoRun(t, "feature/cli-tools")
	h.tty, h.stdin = true, "\n"
	require.Equal(t, 0, h.run("run", "deploy-tst", "--repository", "infra-scripts=feature/x"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "feature/cli-tools → feature/x")
	assert.Equal(t, []string{tabURL}, h.opened)
	require.Len(t, h.fake.Triggered, 1)
}

func TestRunUnknownRepositoryFailsBeforeTriggering(t *testing.T) {
	h := repoRun(t, "develop")
	h.tty = true
	require.Equal(t, 2, h.run("run", "deploy-tst", "--repository", "unknown-repo=feature/x"))
	assert.Contains(t, h.stderr.String(), "infra-base, infra-scripts")
	assert.Empty(t, h.fake.Triggered)
	assert.Empty(t, h.opened)
}

func TestRunRepositoryFlagNeedsAPlanBranch(t *testing.T) {
	h := repoRun(t, "develop")
	require.Equal(t, 2, h.run("run", "PROJ-BUILD", "--repository", "app=main"))
	assert.Contains(t, h.stderr.String(), "repositories need a plan branch")
	assert.Empty(t, h.fake.Triggered)
}

// BAW-7 answer 4: --repo-branch is gone, with no special error.
func TestRunRepoBranchFlagIsUnknown(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, 2, h.run("run", "PROJ-BUILD", "--repo-branch", "repo-a=x"))
	assert.Contains(t, h.stderr.String(), "unknown flag: --repo-branch")
}

// BAW-7 q7: after the first job finished bam confirms the branch it built and
// warns on a difference, keeping the exit code of the build.
func TestRunWatchConfirmsOrWarnsAboutTheBuiltBranch(t *testing.T) {
	for name, built := range map[string]string{"confirms": "feature/cli-tools", "warns": "develop"} {
		t.Run(name, func(t *testing.T) {
			h := repoRun(t, "feature/cli-tools")
			mk := func(branch string) provider.Build {
				b := provider.Build{Key: "PROJ-PROV12-9", PlanKey: "PROJ-PROV12", Number: 9, State: provider.StateSuccess}
				b.Stages = []provider.Stage{{Name: "Apply", State: provider.StateSuccess, Jobs: []provider.Job{{Key: "PROJ-PROV12-TF-9", State: provider.StateSuccess,
					Branches: map[string]string{"infra-scripts": branch}}}}}
				return b
			}
			// The check before the trigger reads build 9 first (not yet there
			// in the history, so build 8); the watch and the confirmation see it done.
			h.fake.Sequences = map[string][]provider.Build{"PROJ-PROV12-9": {mk(built)}}
			require.Equal(t, 0, h.run("run", "deploy-tst", "--watch"), h.stderr.String())
			if built == "develop" {
				assert.Contains(t, h.stderr.String(), "warning: infra-scripts built develop, not feature/cli-tools")
			} else {
				assert.Contains(t, h.stderr.String(), "infra-scripts built feature/cli-tools")
			}
		})
	}
}

func TestPresetShowListsRepositories(t *testing.T) {
	h := repoRun(t, "develop")
	require.Equal(t, 0, h.run("preset", "show", "deploy-tst"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "Repositories\n  infra-scripts  → feature/cli-tools")

	require.Equal(t, 0, h.run("preset", "show", "deploy-tst", "--json"), h.stderr.String())
	var doc struct {
		Repositories map[string]string `json:"repositories"`
		RepoBranches map[string]string `json:"repo_branches"`
	}
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, map[string]string{"infra-scripts": "feature/cli-tools"}, doc.Repositories)
	assert.NotNil(t, doc.RepoBranches)
	assert.Empty(t, doc.RepoBranches)
}

// BAW-7 answer 8: after the plan branch built feature/cli-tools, preset
// add writes it, and the preset it wrote runs with no question.
func TestPresetAddWritesTheBranchesTheRepositoriesBuildNow(t *testing.T) {
	h := repoRun(t, "feature/cli-tools")
	require.Equal(t, 0, h.run("preset", "add", "x", "--plan", "PROJ-PROV", "--branch", "develop", "--print"), h.stderr.String())
	out := h.stdout.String()
	assert.Contains(t, out, "    branch: \"develop\"")
	assert.Contains(t, out, "    repositories:\n")
	assert.Contains(t, out, "      infra-scripts: \"feature/cli-tools\"")
	assert.Contains(t, out, "      infra-base: \"develop\"")

	require.Equal(t, 0, h.run("preset", "add", "y", "--plan", "PROJ-PROV", "--print"), h.stderr.String())
	assert.NotContains(t, h.stdout.String(), "repositories", "without --branch no repositories are written")
}

// A set checks every preset before its first stage: a preset on another
// branch stops the set, with nothing triggered, when bam cannot ask.
func TestSetRunChecksRepositoriesBeforeTheFirstStage(t *testing.T) {
	h := repoRun(t, "develop")
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte(repoPresetYAML+`  publish-specs:
    plan: PROJ-BUILD
sets:
  release:
    stages:
      - [publish-specs]
      - [deploy-tst]
`), 0o644))
	require.Equal(t, 2, h.run("set", "run", "release"), h.stderr.String())
	assert.Empty(t, h.fake.Triggered, "the first stage did not start either")
	assert.Contains(t, h.stderr.String(), tabURL)
}
