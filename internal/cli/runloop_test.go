package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func queuedBuild(key string) provider.Build {
	return provider.Build{Key: key, URL: workOrigin + "/browse/" + key, State: provider.StateQueued}
}

func TestRunDryRunTriggersNothing(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("run", "provision-lab", "--var", "cluster_name=a", "--dry-run"))
	assert.Contains(t, h.stdout.String(), "Plan     PROJ-PROV  (branch develop, PROJ-PROV12)")
	assert.Contains(t, h.stdout.String(), "cluster_name=a (flag)")
	assert.Contains(t, h.stderr.String(), "dry run: nothing triggered")
	assert.Empty(t, h.fake.Triggered)
}

func TestRunValidationStopsBeforeTrigger(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("run", "provision-lab"))
	assert.Contains(t, h.stderr.String(), "cluster_name is required by preset provision-lab")
	assert.Equal(t, 2, h.run("run", "provision-lab", "--var", "cluster_name=a", "--var", "cluster_type=nomad"))
	assert.Empty(t, h.fake.Triggered)
}

func TestRunWithoutWatch(t *testing.T) {
	h := newHarness(t)
	h.fake.TriggerResult = queuedBuild("PROJ-PROV12-9")
	assert.Equal(t, 0, h.run("run", "provision-lab", "--var", "cluster_name=a", "--from", "last"))
	assert.Contains(t, h.stdout.String(), "✓ Queued   PROJ-PROV12-9")
	assert.Contains(t, h.stdout.String(), "watch: bam watch PROJ-PROV12-9")
	assert.Contains(t, h.stderr.String(), "variables from PROJ-PROV12-8")
	require.Len(t, h.fake.Triggered, 1)
	assert.Equal(t, "PROJ-PROV12", h.fake.Triggered[0].PlanKey)
	assert.Equal(t, "dcos", h.fake.Triggered[0].Variables["cluster_type"], "--from last supplied it")

	assert.Equal(t, 0, h.run("url", "--last"))
	assert.Equal(t, workOrigin+"/browse/PROJ-PROV12-9\n", h.stdout.String())

	for _, cmd := range [][]string{{"url", "--last", "PROJ-BUILD"}, {"build", "show", "--last", "PROJ-BUILD"}} {
		assert.Equal(t, 2, h.run(cmd...), "a key next to --last is never ignored")
		assert.Equal(t, "error: --last and PROJ-BUILD both name a build\n  try: drop PROJ-BUILD for the last build bam triggered here, or drop --last\n", h.stderr.String())
	}
}

func TestRunFromAPageLink(t *testing.T) {
	h := newHarness(t)
	h.fake.TriggerResult = queuedBuild("PROJ-PROV12-9")
	assert.Equal(t, 0, h.run("run", "provision-lab", "--var", "cluster_name=a", "--from", workOrigin+"/browse/PROJ-PROV12-8"), h.stderr.String())
	assert.Contains(t, h.stderr.String(), "variables from PROJ-PROV12-8")

	assert.Equal(t, 2, h.run("run", "provision-lab", "--var", "cluster_name=a", "--from", "https://bamboo.lab.example/browse/PROJ-PROV12-8"))
	assert.Contains(t, h.stderr.String(), "--from PROJ-PROV12-8 is on another server than PROJ-PROV12")
	assert.Len(t, h.fake.Triggered, 1, "a link to another server never triggers")
}

func TestRunJSONWithoutWatch(t *testing.T) {
	h := newHarness(t)
	h.fake.TriggerResult = queuedBuild("PROJ-BUILD-483")
	assert.Equal(t, 0, h.run("build", "run", "build", "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "PROJ-BUILD-483", doc["key"])
}

func TestRunWatchExitCodes(t *testing.T) {
	for _, tc := range []struct {
		final provider.State
		code  int
		text  string
	}{
		{provider.StateSuccess, 0, "PROJ-BUILD-483  ✓ success"},
		{provider.StateFailed, 1, "failed: PROJ-BUILD-483"},
	} {
		h := newHarness(t)
		h.fake.TriggerResult = queuedBuild("PROJ-BUILD-483")
		h.fake.Sequences = map[string][]provider.Build{"PROJ-BUILD-483": {
			{Key: "PROJ-BUILD-483", State: provider.StateRunning},
			{Key: "PROJ-BUILD-483", State: tc.final},
		}}
		assert.Equal(t, tc.code, h.run("run", "build", "--watch"), tc.final)
		assert.Contains(t, h.stdout.String(), "PROJ-BUILD-483 running")
		assert.Contains(t, h.stdout.String(), tc.text)
		assert.Empty(t, h.stderr.String())
	}
}

func TestRunWatchFollowsTheChildBuilds(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	h.fake.TriggerResult = queuedBuild("PROJ-BUILD-482")
	assert.Equal(t, 1, h.run("run", "build", "--watch"), "a failed child fails the run though the parent succeeded")
	assert.Contains(t, h.stdout.String(), "└─ PROJ-PROV-9")
	assert.Contains(t, h.stdout.String(), "failed: PROJ-PROV-9 "+workOrigin+"/browse/PROJ-PROV-9")
}

func TestWatchNDJSON(t *testing.T) {
	h := newHarness(t)
	h.fake.Sequences = map[string][]provider.Build{"PROJ-BUILD-483": {
		{Key: "PROJ-BUILD-483", State: provider.StateRunning},
		{Key: "PROJ-BUILD-483", State: provider.StateSuccess},
	}}
	assert.Equal(t, 0, h.run("watch", "PROJ-BUILD-483", "--json"))
	lines := strings.Split(strings.TrimSpace(h.stdout.String()), "\n")
	var last map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &last))
	assert.Equal(t, "done", last["type"])
}

func TestWatchTimeout(t *testing.T) {
	h := newHarness(t)
	h.fake.Sequences = map[string][]provider.Build{"PROJ-BUILD-483": {{Key: "PROJ-BUILD-483", State: provider.StateRunning}}}
	assert.Equal(t, 6, h.run("watch", "PROJ-BUILD-483", "--timeout", "50ms"))
	assert.Contains(t, h.stderr.String(), "timed out after 50ms")
	assert.Contains(t, h.stderr.String(), "bam watch PROJ-BUILD-483")
	assert.Empty(t, h.fake.Stopped)
}

func TestWatchInterruptLeavesBuildRunning(t *testing.T) {
	h := newHarness(t)
	h.fake.Sequences = map[string][]provider.Build{"PROJ-BUILD-483": {{Key: "PROJ-BUILD-483", State: provider.StateRunning}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Equal(t, 130, h.runCtx(ctx, "watch", "PROJ-BUILD-483"))
	assert.Contains(t, h.stderr.String(), "still running: bam watch PROJ-BUILD-483")
	assert.Empty(t, h.fake.Stopped)
}

func TestLogs(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("logs", "PROJ-PROV12-8", "--failed"))
	assert.Equal(t, "terraform apply\nError: quota exceeded\n", h.stdout.String())

	assert.Equal(t, 0, h.run("logs", "PROJ-PROV12-8", "--tail", "1"))
	assert.Equal(t, "Error: quota exceeded\n", h.stdout.String())

	assert.Equal(t, 0, h.run("logs", "PROJ-BUILD-482", "--failed"))
	assert.Equal(t, "no failed jobs in PROJ-BUILD-482\n", h.stderr.String())

	// An unfinished build may still fail, so the note says so and points at watch.
	h2 := treeHarness(t, provider.StateRunning)
	assert.Equal(t, 0, h2.run("logs", "PROJ-PROV-9", "--failed"))
	assert.Equal(t, "no failed jobs in PROJ-PROV-9 yet; it is running\n  try: bam watch PROJ-PROV-9\n", h2.stderr.String())

	assert.Equal(t, 0, h.run("logs", "PROJ-BUILD-482", "--failed", "--json"))
	assert.Equal(t, "[]\n", h.stdout.String(), "--json always writes exactly one document, even when empty")

	assert.Equal(t, 2, h.run("logs", "--last"))
	assert.Equal(t, 0, h.run("logs", "PROJ-PROV12", "--failed"))
	assert.Equal(t, "terraform apply\nError: quota exceeded\n", h.stdout.String())
	assert.Equal(t, "PROJ-PROV12-8 (failed)\n", h.stderr.String(), "a plan names the build it picked, on stderr")
	assert.Equal(t, 0, h.run("logs", "PROJ-PROV12-8", "--follow"))
	assert.Contains(t, h.stdout.String(), "Error: quota exceeded")

	assert.Equal(t, 0, h.run("logs", "PROJ-PROV12-8", "--json"))
	assert.Contains(t, h.stdout.String(), `"job": "PROJ-PROV12-TF-8"`)

	assert.Equal(t, 0, h.run("logs", "PROJ-PROV12-TF-8"), "a job result key names the build and the job")
	assert.Equal(t, "terraform apply\nError: quota exceeded\n", h.stdout.String())
	assert.Equal(t, 2, h.run("logs", "PROJ-PROV12-NOPE-8"), "the job in the key is honoured")
	assert.Equal(t, 0, h.run("logs", workOrigin+"/browse/PROJ-PROV12-TF-8/log"), "a job page link")
	assert.Equal(t, "terraform apply\nError: quota exceeded\n", h.stdout.String())
	assert.Empty(t, h.stderr.String(), "a link names its build already")
	assert.Equal(t, 0, h.run("logs", workOrigin+"/browse/PROJ-PROV12-8"), "a build page link")
	assert.Empty(t, h.stderr.String(), "a link names its build already")
	assert.Equal(t, 2, h.run("logs", workOrigin+"/browse/PROJ-PROV12-NOPE-8"))
	assert.Equal(t, 0, h.run("logs", workOrigin+"/build/result/viewBuildResultLogs.action?buildKey=PROJ-PROV12-TF&buildNumber=8"), "an older .action job link")
	assert.Equal(t, "terraform apply\nError: quota exceeded\n", h.stdout.String())
}

func TestCancel(t *testing.T) {
	h := newHarness(t)
	h.fake.Sequences = map[string][]provider.Build{"PROJ-BUILD-483": {{Key: "PROJ-BUILD-483", State: provider.StateRunning}}}
	assert.Equal(t, 0, h.run("build", "cancel", "PROJ-BUILD-483"))
	assert.Equal(t, "stopping PROJ-BUILD-483\n  watch: bam watch PROJ-BUILD-483\n", h.stdout.String())
	assert.Equal(t, []string{"PROJ-BUILD-483"}, h.fake.Stopped)

	assert.Equal(t, 0, h.run("build", "cancel", "PROJ-BUILD-482"))
	assert.Contains(t, h.stderr.String(), "PROJ-BUILD-482 already finished (success)")
	assert.Len(t, h.fake.Stopped, 1)

	assert.Equal(t, 0, h.run("build", "cancel", "PROJ-BUILD-482", "--json"))
	assert.Empty(t, h.stderr.String(), "the human-only note is not printed in --json mode")
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "PROJ-BUILD-482", doc["key"])
	assert.Equal(t, false, doc["stopped"])
	assert.Equal(t, "success", doc["state"])
}

func TestCancelDryRunOnAServerThatCannotStop(t *testing.T) {
	h := newHarness(t)
	h.fake.Sequences = map[string][]provider.Build{"PROJ-BUILD-483": {{Key: "PROJ-BUILD-483", State: provider.StateRunning}}}
	h.fake.NoStop = true
	assert.Equal(t, 5, h.run("build", "cancel", "PROJ-BUILD-483", "--dry-run"))
	assert.Empty(t, h.stdout.String(), "no \"would stop\" for a stop that would fail")
	assert.Contains(t, h.stderr.String(), "this Bamboo cannot stop a build over REST")
}

func TestCancelDryRun(t *testing.T) {
	h := newHarness(t)
	h.fake.Sequences = map[string][]provider.Build{"PROJ-BUILD-483": {{Key: "PROJ-BUILD-483", State: provider.StateRunning}}}
	assert.Equal(t, 0, h.run("build", "cancel", "PROJ-BUILD-483", "--dry-run"))
	assert.Equal(t, "would stop PROJ-BUILD-483 (running)\n", h.stdout.String())
	assert.Contains(t, h.stderr.String(), "dry run: nothing stopped")
	assert.Equal(t, 0, h.run("build", "cancel", "PROJ-BUILD-483", "--dry-run", "--json"))
	assert.JSONEq(t, `{"key":"PROJ-BUILD-483","url":"`+workOrigin+`/browse/PROJ-BUILD-483","state":"running","stopped":false}`, h.stdout.String())
	assert.Equal(t, 0, h.run("build", "cancel", "PROJ-BUILD-482", "--dry-run"))
	assert.Contains(t, h.stderr.String(), "PROJ-BUILD-482 already finished (success)")
	assert.Empty(t, h.fake.Stopped)
}

func TestOpenAndURL(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("open", "provision-lab"))
	assert.Equal(t, []string{workOrigin + "/browse/PROJ-PROV12"}, h.opened, "the preset's branch: develop")
	assert.Equal(t, 0, h.run("url", "PROJ-BUILD-482"))
	assert.Equal(t, workOrigin+"/browse/PROJ-BUILD-482\n", h.stdout.String())
	assert.Equal(t, 0, h.run("url", "PROJ"))
	assert.Equal(t, 0, h.run("url", "PROJ-BUILD-JOB1-482"))
	assert.Equal(t, 2, h.run("url", "not a key"))
	assert.Equal(t, 0, h.run("url", "PROJ-BUILD-482", "--job", "int"))
	assert.Equal(t, workOrigin+"/browse/PROJ-BUILD-INT-482\n", h.stdout.String())
	assert.Equal(t, 0, h.run("url", "provision-lab", "--job", "JOB1"))
	assert.Equal(t, workOrigin+"/browse/PROJ-PROV12-JOB1\n", h.stdout.String())
	assert.Equal(t, 0, h.run("open", "PROJ-BUILD-482", "--job", "INT"))
	assert.Equal(t, workOrigin+"/browse/PROJ-BUILD-INT-482", h.opened[len(h.opened)-1])
	assert.Equal(t, 2, h.run("url", "PROJ", "--job", "INT"))
	assert.Contains(t, h.stderr.String(), "--job needs a plan or build, not PROJ")
	assert.Equal(t, 2, h.run("url", "PROJ-BUILD-482", "--job", "PROJ-BUILD-INT"))
	assert.Equal(t, 2, h.run("open"))

	assert.Equal(t, 0, h.run("url", "PROJ-BUILD-0482"))
	assert.Equal(t, workOrigin+"/browse/PROJ-BUILD-482\n", h.stdout.String(), "leading zeros are dropped, as for build show")
	assert.Equal(t, 2, h.run("url", "PROJ-BUILD-0"))
	assert.Contains(t, h.stderr.String(), "build numbers start at 1, not PROJ-BUILD-0")

	h.env.OpenBrowser = func(string) error { return errors.New("no display") }
	assert.Equal(t, 0, h.run("open", "PROJ-BUILD"))
	assert.Equal(t, workOrigin+"/browse/PROJ-BUILD\n", h.stdout.String())
	assert.Contains(t, h.stderr.String(), "could not open a browser")
}

func TestVarCompletionUsesTargetOptions(t *testing.T) {
	h := newHarness(t)
	h.run("__complete", "run", "provision-lab", "--var", "cluster_type=")
	assert.Contains(t, h.stdout.String(), "cluster_type=k8s\n")
	assert.Contains(t, h.stdout.String(), "cluster_type=dcos\n")

	h.run("__complete", "run", "prov")
	assert.Contains(t, h.stdout.String(), "provision-lab\n")
}

func TestVarCompletionOffersAPlanKeysVariables(t *testing.T) {
	h := newHarness(t)
	h.run("__complete", "run", "PROJ-PROV", "--var", "")
	assert.Equal(t, "cluster_name=\ncluster_type=\n:6\n", h.stdout.String())
	h.run("__complete", "run", "PROJ-PROV", "--var", "cluster_type=")
	assert.Equal(t, ":4\n", h.stdout.String())
}

func TestHelpListsShortcuts(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("--help"))
	assert.Contains(t, h.stdout.String(), "Shortcuts:")
	assert.Regexp(t, `run\s+Trigger a plan or preset`, h.stdout.String())
}

func TestRunRevisionDryRun(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("run", "build", "--revision", "abc1234", "--dry-run"))
	assert.Contains(t, h.stdout.String(), "Revision abc1234\n")
	assert.Empty(t, h.fake.Triggered)

	h = newHarness(t)
	assert.Equal(t, 0, h.run("run", "build", "--revision", "abc1234", "--dry-run", "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "abc1234", doc["revision"])
}

func TestRunJSONHasAnEmptyRevisionByDefault(t *testing.T) {
	h := newHarness(t)
	h.fake.TriggerResult = queuedBuild("PROJ-BUILD-483")
	assert.Equal(t, 0, h.run("run", "build", "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "", doc["revision"])
}

func TestRunRevisionWithoutWatch(t *testing.T) {
	h := newHarness(t)
	h.fake.TriggerResult = queuedBuild("PROJ-BUILD-483")
	assert.Equal(t, 0, h.run("run", "build", "--revision", "abc1234"))
	require.Len(t, h.fake.Triggered, 1)
	assert.Equal(t, "abc1234", h.fake.Triggered[0].Revision)
	assert.Contains(t, h.stdout.String(), "revision abc1234 is checked when the build starts: bam watch PROJ-BUILD-483")
}

func TestRunRevisionNotBuilt(t *testing.T) {
	h := newHarness(t)
	h.fake.TriggerResult = queuedBuild("PROJ-BUILD-483")
	h.fake.Sequences = map[string][]provider.Build{"PROJ-BUILD-483": {
		{Key: "PROJ-BUILD-483", State: provider.StateQueued},
		{Key: "PROJ-BUILD-483", State: provider.StateNotBuilt},
	}}
	assert.Equal(t, 1, h.run("run", "build", "--revision", "0000000", "--watch"))
	out := h.stdout.String()
	assert.Contains(t, out, "Bamboo could not build revision 0000000\n")
	assert.Greater(t, strings.Index(out, "Bamboo could not build"), strings.Index(out, "next:"), "after the result and its hints")
}

func TestRunNotBuiltWithoutRevisionSaysNothingAboutRevisions(t *testing.T) {
	h := newHarness(t)
	h.fake.TriggerResult = queuedBuild("PROJ-BUILD-483")
	h.fake.Sequences = map[string][]provider.Build{"PROJ-BUILD-483": {{Key: "PROJ-BUILD-483", State: provider.StateNotBuilt}}}
	assert.Equal(t, 1, h.run("run", "build", "--watch"))
	assert.NotContains(t, h.stdout.String(), "revision")
}

func TestRunRevisionIgnored(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		h := newHarness(t)
		h.fake.TriggerResult = queuedBuild("PROJ-BUILD-483")
		h.fake.TriggerErr = errs.Bamboof("Bamboo ignored the revision").Wrap(errs.ErrUnsupported)
		args := []string{"run", "build", "--revision", "abc1234"}
		if asJSON {
			args = append(args, "--json")
		}
		assert.Equal(t, 5, h.run(args...))
		assert.Contains(t, h.stderr.String(), "Bamboo ignored the revision; PROJ-BUILD-483 was stopped")
		assert.Equal(t, []string{"PROJ-BUILD-483"}, h.fake.Stopped)
		if asJSON {
			assert.Empty(t, h.stdout.String())
		}
	}
}

func TestRunRevisionNeedsAValue(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("run", "build", "--revision", ""))
	assert.Contains(t, h.stderr.String(), "--revision needs a value")
	assert.Empty(t, h.connectOpts, "no network before the check")
}

const lastCommit = "9b30b9aba03a4f45881629b03531006b89c1e0e7"

func withRevision(h *harness) {
	h.fake.History["PROJ-BUILD"][0].Revision = lastCommit
}

func TestRunRevisionFromDryRun(t *testing.T) {
	h := newHarness(t)
	withRevision(h)
	assert.Equal(t, 0, h.run("run", "build", "--from", "482", "--revision", "from", "--dry-run"))
	assert.Contains(t, h.stdout.String(), "Revision 9b30b9a (from #482)\n")
	assert.Empty(t, h.fake.Triggered)

	h = newHarness(t)
	withRevision(h)
	assert.Equal(t, 0, h.run("run", "build", "--from", "LAST", "--revision", "FROM", "--dry-run"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "Revision 9b30b9a (from #482)\n", "last and from are matched in any case")

	h = newHarness(t)
	withRevision(h)
	assert.Equal(t, 0, h.run("run", "build", "--from", "482", "--revision", "from", "--dry-run", "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, lastCommit, doc["revision"])
	assert.Equal(t, "PROJ-BUILD-482", doc["revision_from"])
}

func TestRunRevisionFromSendsTheFullCommit(t *testing.T) {
	h := newHarness(t)
	withRevision(h)
	h.fake.TriggerResult = queuedBuild("PROJ-BUILD-483")
	assert.Equal(t, 0, h.run("run", "build", "--from", "482", "--revision", "from"))
	require.Len(t, h.fake.Triggered, 1)
	assert.Equal(t, lastCommit, h.fake.Triggered[0].Revision)
	assert.Contains(t, h.stdout.String(), "revision 9b30b9a is checked when the build starts: bam watch PROJ-BUILD-483")
}

// --from last is resolved once, so the variables and the commit cannot come
// from two different builds.
func TestRunRevisionFromLastListsBuildsOnce(t *testing.T) {
	h := newHarness(t)
	withRevision(h)
	assert.Equal(t, 0, h.run("run", "build", "--from", "last", "--revision", "from", "--dry-run", "--json"))
	assert.Equal(t, 1, h.fake.ListCalls("PROJ-BUILD"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "PROJ-BUILD-482", doc["revision_from"])
}

func TestRunRevisionFromNeedsFrom(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("run", "build", "--revision", "from"))
	assert.Contains(t, h.stderr.String(), "--revision from needs --from")
	assert.Empty(t, h.connectOpts, "no network before the check")
}

func TestRunRevisionFromABuildWithoutARevision(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("run", "build", "--from", "482", "--revision", "from"))
	assert.Contains(t, h.stderr.String(), "PROJ-BUILD-482 has no revision to reuse")
	assert.Empty(t, h.fake.Triggered)
}

func TestRunJSONHasAnEmptyRevisionFromByDefault(t *testing.T) {
	h := newHarness(t)
	h.fake.TriggerResult = queuedBuild("PROJ-BUILD-483")
	assert.Equal(t, 0, h.run("run", "build", "--revision", "abc1234", "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "abc1234", doc["revision"])
	assert.Equal(t, "", doc["revision_from"])
}

func TestInvestigate(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("build", "investigate", "PROJ-PROV12-8", "--context", "1"))
	out := h.stdout.String()
	assert.Contains(t, out, "Apply / Terraform (TF)  first error at line 2")
	assert.Contains(t, out, "       1  terraform apply\n  >     2  Error: quota exceeded\n")
	assert.Contains(t, out, "Likely cause: Error: quota exceeded (job TF)")
	assert.Contains(t, out, "try: bam logs PROJ-PROV12-8 --job TF")

	assert.Equal(t, 0, h.run("build", "investigate", "PROJ-BUILD-482"))
	assert.Empty(t, h.stdout.String())
	assert.Contains(t, h.stderr.String(), "PROJ-BUILD-482 did not fail (success), nothing to investigate")

	h.fake.Sequences = map[string][]provider.Build{"PROJ-BUILD-483": {{Key: "PROJ-BUILD-483", State: provider.StateRunning}}}
	assert.Equal(t, 0, h.run("build", "investigate", "PROJ-BUILD-483"))
	assert.Contains(t, h.stderr.String(), "try: bam watch PROJ-BUILD-483")
}

func TestInvestigateJSON(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("build", "investigate", "PROJ-PROV12-8", "--context", "1", "--json"))
	var doc struct {
		Build       struct{ Key, State string }
		LikelyCause string `json:"likely_cause"`
		Jobs        []struct {
			Short  string
			Errors []struct {
				Line    int
				Pattern string
				Start   int
				Context []string
				Count   int
			}
		}
	}
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "PROJ-PROV12-8", doc.Build.Key)
	assert.Equal(t, "Error: quota exceeded (job TF)", doc.LikelyCause)
	require.Len(t, doc.Jobs, 1)
	assert.Equal(t, "TF", doc.Jobs[0].Short)
	require.NotEmpty(t, doc.Jobs[0].Errors)
	e := doc.Jobs[0].Errors[0]
	assert.Equal(t, 2, e.Line)
	assert.Equal(t, "error", e.Pattern)
	assert.Equal(t, 1, e.Start)
	assert.Equal(t, "terraform apply", e.Context[0])
	assert.Equal(t, 1, e.Count)

	assert.Equal(t, 0, h.run("build", "investigate", "PROJ-BUILD-482", "--json"))
	assert.Contains(t, h.stdout.String(), `"likely_cause": "",
  "jobs": []`)
	assert.Empty(t, h.stderr.String())
}

func TestInvestigateCompare(t *testing.T) {
	h := newHarness(t)
	h.fake.History["PROJ-PROV12"] = append(h.fake.History["PROJ-PROV12"],
		provider.Build{Key: "PROJ-PROV12-7", PlanKey: "PROJ-PROV12", Number: 7, State: provider.StateSuccess})
	h.fake.BuildVars["PROJ-PROV12-7"] = map[string]string{"cluster_type": "k8s", "cluster_name": "beta"}
	assert.Equal(t, 0, h.run("build", "investigate", "PROJ-PROV12-8", "--compare"))
	assert.Contains(t, h.stdout.String(), "Changed since last success (PROJ-PROV12-7): var cluster_type=dcos (was k8s)\n")

	assert.Equal(t, 0, h.run("build", "investigate", "PROJ-PROV12-8", "--compare", "--json"))
	var doc struct {
		Changes struct {
			Since     struct{ Key string }
			Revisions []map[string]string
			Variables []map[string]string
		}
	}
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "PROJ-PROV12-7", doc.Changes.Since.Key)
	assert.Equal(t, []map[string]string{}, doc.Changes.Revisions)
	assert.Equal(t, []map[string]string{{"name": "cluster_type", "was": "k8s", "now": "dcos"}}, doc.Changes.Variables)

	assert.Equal(t, 0, h.run("build", "investigate", "PROJ-PROV12-8", "--json"))
	assert.Contains(t, h.stdout.String(), `"changes": null`)
}

func TestNegativeLineCountsAreUsageErrors(t *testing.T) {
	for args, try := range map[string]string{
		"build investigate PROJ-PROV12-8 --context -2": "--context 0 shows only the error lines",
		"build investigate PROJ-PROV12-8 --max -1":     "--max 0 shows all error blocks",
		"logs PROJ-PROV12-8 --tail -5":                 "--tail 0 prints the whole log",
	} {
		h := newHarness(t)
		assert.Equal(t, 2, h.run(strings.Fields(args)...), args)
		assert.Contains(t, h.stderr.String(), "takes 0 or more", args)
		assert.Contains(t, h.stderr.String(), try, args)
	}
}

func TestNegativeTimeoutIsAUsageError(t *testing.T) {
	for _, args := range [][]string{
		{"watch", "PROJ-PROV12-8", "--timeout", "-5m"},
		{"run", "PROJ-PROV12", "--watch", "--timeout", "-1s"},
		{"build", "rerun", "PROJ-PROV12-8", "--watch", "--timeout", "-1h"},
	} {
		h := newHarness(t)
		assert.Equal(t, 2, h.run(args...), args)
		assert.Contains(t, h.stderr.String(), "--timeout takes 0 or more", args)
		assert.Contains(t, h.stderr.String(), "leave out --timeout", args)
	}
}
