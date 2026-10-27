package cli

import (
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rerunnable makes PROJ-PROV-9 restart and then end in final: read failed
// by the tree and by Restart, then queued, then final.
func rerunnable(h *harness, final provider.State) {
	child := h.fake.History["PROJ-PROV"][0]
	queued, done := child, child
	queued.State, done.State = provider.StateQueued, final
	done.Stages = nil
	h.fake.Sequences = map[string][]provider.Build{"PROJ-PROV-9": {child, child, queued, done}}
}

func TestRerunFailedWithYesRestartsAndFollows(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	rerunnable(h, provider.StateSuccess)
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed", "--yes"))
	assert.Equal(t, []string{"PROJ-PROV-9"}, h.fake.Restarted)
	assert.Contains(t, h.stderr.String(), "restarted PROJ-PROV-9")
	assert.Contains(t, h.stdout.String(), "PROJ-PROV-9 success")
}

func TestRerunFailedAsksOnATerminal(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	rerunnable(h, provider.StateSuccess)
	h.tty, h.stdin = true, "y\n"
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed"))
	assert.Contains(t, h.stderr.String(), "Rerun the failed jobs of 1 build?")
	assert.Contains(t, h.stderr.String(), "Apply › Terraform")
	assert.Contains(t, h.stderr.String(), "[y/N]")
	assert.Equal(t, []string{"PROJ-PROV-9"}, h.fake.Restarted)
}

func TestRerunFailedNoRestartsNothing(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	h.tty, h.stdin = true, "n\n"
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed"))
	assert.Contains(t, h.stderr.String(), "nothing restarted")
	assert.Empty(t, h.fake.Restarted)
}

func TestRerunFailedEndOfInputRestartsNothing(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	h.tty, h.stdin = true, ""
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed"))
	assert.Contains(t, h.stderr.String(), "nothing restarted")
	assert.Empty(t, h.fake.Restarted)
}

func TestRerunFailedNeedsYesWithoutATerminal(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	assert.Equal(t, 2, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed"))
	assert.Contains(t, h.stderr.String(), "--rerun-failed needs --yes when it cannot ask")
	assert.Empty(t, h.fake.Restarted)
	h.tty = true
	assert.Equal(t, 2, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed", "--json"))
	assert.Empty(t, h.fake.Restarted)
}

func TestRerunFailedRefusesARunningTree(t *testing.T) {
	h := treeHarness(t, provider.StateFailed) // root succeeded, PROJ-PROV-9 failed
	// A running child of another plan: a second PROJ-PROV run would
	// supersede PROJ-PROV-9 and leave no candidate.
	h.fake.History["PROJ-BUILD"] = append(h.fake.History["PROJ-BUILD"], provider.Build{Key: "PROJ-BUILD-483", PlanKey: "PROJ-BUILD",
		Number: 483, State: provider.StateRunning, Reason: "Child of PROJ-BUILD-482"})
	assert.Equal(t, 2, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed", "--yes"))
	assert.Contains(t, h.stderr.String(), "tree is still running")
	assert.Empty(t, h.fake.Restarted)
}

func TestRerunFailedNothingToRerun(t *testing.T) {
	h := treeHarness(t, provider.StateSuccess)
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed", "--yes"))
	assert.Contains(t, h.stderr.String(), "nothing to rerun")
	assert.Empty(t, h.fake.Restarted)
}

func TestRerunFailedNoWatch(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	rerunnable(h, provider.StateSuccess)
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed", "--yes", "--no-watch"))
	assert.Contains(t, h.stderr.String(), "restarted PROJ-PROV-9")
	assert.NotContains(t, h.stdout.String(), "PROJ-PROV-9 success", "nothing is followed")
}

func TestRerunFailedRestartErrorExits5(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	rerunnable(h, provider.StateSuccess)
	h.fake.RestartErrs = map[string]error{"PROJ-PROV-9": errs.Bamboof("Bamboo answered 409")}
	assert.Equal(t, 5, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed", "--yes"))
	assert.Contains(t, h.stderr.String(), "PROJ-PROV-9 was not restarted: Bamboo answered 409")
}

func TestRerunFailedReportsABuildThatWasNotRestarted(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	child := h.fake.History["PROJ-PROV"][0]
	running := child
	running.State = provider.StateRunning
	h.fake.Sequences = map[string][]provider.Build{"PROJ-PROV-9": {child, running, running}}
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed", "--yes", "--no-watch"))
	assert.Contains(t, h.stderr.String(), "PROJ-PROV-9 was not restarted: it is running or has succeeded")
	require.Empty(t, h.fake.Restarted)
}

func TestBuildRerunRestartsOneBuild(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	rerunnable(h, provider.StateSuccess)
	assert.Equal(t, 0, h.run("build", "rerun", "PROJ-PROV-9"))
	assert.Equal(t, []string{"PROJ-PROV-9"}, h.fake.Restarted)
	assert.Contains(t, h.stdout.String(), "restarted PROJ-PROV-9")
	assert.Contains(t, h.stdout.String(), "watch: bam watch PROJ-PROV-9")
}

func TestBuildRerunWatch(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	rerunnable(h, provider.StateSuccess)
	assert.Equal(t, 0, h.run("build", "rerun", "PROJ-PROV-9", "--watch"))
	assert.Contains(t, h.stdout.String(), "PROJ-PROV-9  ✓ success")
}

func TestBuildRerunWatchExitsOneOnAFailedRerun(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	rerunnable(h, provider.StateFailed)
	assert.Equal(t, 1, h.run("build", "rerun", "PROJ-PROV-9", "--watch"))
	assert.Contains(t, h.stdout.String(), "failed: PROJ-PROV-9 "+workOrigin+"/browse/PROJ-PROV-9")
}

func TestBuildRerunWatchTimeout(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	rerunnable(h, provider.StateRunning)
	assert.Equal(t, 6, h.run("build", "rerun", "PROJ-PROV-9", "--watch", "--timeout", "50ms"))
	assert.Contains(t, h.stderr.String(), "timed out after 50ms")
	assert.Contains(t, h.stderr.String(), "bam watch PROJ-PROV-9")
	assert.Empty(t, h.fake.Stopped)
}

func TestBuildRerunRefusesARunningOrSuccessfulBuild(t *testing.T) {
	h := treeHarness(t, provider.StateSuccess)
	assert.Equal(t, 2, h.run("build", "rerun", "PROJ-PROV-9"))
	assert.Contains(t, h.stderr.String(), "PROJ-PROV-9 succeeded; nothing to rerun")
	h = treeHarness(t, provider.StateRunning)
	assert.Equal(t, 2, h.run("build", "rerun", "PROJ-PROV-9"))
	assert.Contains(t, h.stderr.String(), "PROJ-PROV-9 is running; nothing to rerun")
	assert.Empty(t, h.fake.Restarted)
}

// A not-built build has no failed jobs; only a failed or stopped build is
// restarted.
func TestBuildRerunRefusesANotBuiltBuild(t *testing.T) {
	h := treeHarness(t, provider.StateNotBuilt)
	assert.Equal(t, 2, h.run("build", "rerun", "PROJ-PROV-9"))
	assert.Contains(t, h.stderr.String(), "PROJ-PROV-9 is not built; nothing to rerun")
	assert.Empty(t, h.fake.Restarted)
}
