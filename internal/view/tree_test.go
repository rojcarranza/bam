package view

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func tnode(key string, st provider.State, d time.Duration, kids ...*app.TreeNode) *app.TreeNode {
	i := strings.LastIndex(key, "-")
	return &app.TreeNode{PlanKey: key[:i], Build: provider.Build{Key: key, PlanKey: key[:i], State: st, Duration: d,
		URL: "https://bamboo.example.com/browse/" + key}, Children: kids}
}

func stage(name string, st provider.State, d time.Duration, failedJobs ...string) provider.Stage {
	s := provider.Stage{Name: name, State: st, Duration: d}
	for _, j := range failedJobs {
		s.Jobs = append(s.Jobs, provider.Job{Name: j, State: provider.StateFailed, Key: "X-" + j})
	}
	return s
}

// sampleTree is the tree of the spec's §6.1.
func sampleTree() app.Tree {
	smoke := tnode("OPS-SMOKE-51", provider.StateFailed, 100*time.Second)
	smoke.Build.Stages = []provider.Stage{stage("Smoke", provider.StateFailed, 100*time.Second, "Smoke tests")}
	smoke.Build.FailedTests = []string{"a", "b", "c"}
	deploy := tnode("OPS-DEPLOY-88", provider.StateSuccess, 192*time.Second, smoke)
	deploy.Build.Stages = []provider.Stage{stage("Deploy", provider.StateSuccess, 192*time.Second)}
	docs := tnode("PROJ-DOCS-30", provider.StateRunning, 0)
	docs.Build.StartedAt = fixedNow.Add(-45 * time.Second)
	audit := &app.TreeNode{PlanKey: "OPS-AUDIT", NotRun: true}
	root := tnode("PROJ-API-142", provider.StateFailed, 723*time.Second, deploy, audit, docs)
	root.Build.Stages = []provider.Stage{stage("Build", provider.StateSuccess, 130*time.Second), stage("Test", provider.StateSuccess, 295*time.Second)}
	return app.Tree{Root: root, State: app.TreeState(root)}
}

func TestTreeTextGolden(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, TreeText(o, sampleTree(), false))
	golden(t, "tree", buf.String())
}

func TestTreeTextFailedOnly(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, TreeText(o, sampleTree(), true))
	out := buf.String()
	require.NotContains(t, out, "PROJ-DOCS-30", "a running branch with no failure is left out")
	require.Contains(t, out, "OPS-SMOKE-51")
	require.Contains(t, out, "OPS-AUDIT")
	golden(t, "tree_failed", out)
}

func TestTreeTextFailedOnlyOnASuccessfulTree(t *testing.T) {
	root := tnode("PROJ-API-142", provider.StateSuccess, time.Minute, tnode("OPS-DEPLOY-88", provider.StateSuccess, time.Minute))
	o, buf := testOut(false)
	require.NoError(t, TreeText(o, app.Tree{Root: root, State: provider.StateSuccess}, true))
	require.Equal(t, 1, strings.Count(buf.String(), "\n"), "the root line only")
}

func TestTreeTextLoopAndNext(t *testing.T) {
	loop := tnode("PROJ-API-142", provider.StateFailed, 0)
	loop.Loop = true
	smoke := tnode("OPS-SMOKE-51", provider.StateFailed, time.Minute, loop)
	smoke.Build.Stages = []provider.Stage{stage("Smoke", provider.StateFailed, time.Minute, "Smoke tests")}
	root := tnode("PROJ-API-142", provider.StateFailed, time.Minute, smoke)
	o, buf := testOut(false)
	require.NoError(t, TreeText(o, app.Tree{Root: root, State: provider.StateFailed}, false))
	out := buf.String()
	require.Contains(t, out, "↺ already shown above")
	require.Contains(t, out, "next: bam logs OPS-SMOKE-51 --failed\n")
	require.NotContains(t, out, "next: bam logs PROJ-API-142", "PROJ-API-142 has no failed job")
}

func TestTreeJSON(t *testing.T) {
	data, err := json.Marshal(TreeJSON(sampleTree(), false))
	require.NoError(t, err)
	var doc struct {
		State    string   `json:"state"`
		Warnings []string `json:"warnings"`
		Root     struct {
			PlanKey  string         `json:"plan_key"`
			Status   string         `json:"status"`
			Build    map[string]any `json:"build"`
			Children []struct {
				PlanKey  string         `json:"plan_key"`
				Status   string         `json:"status"`
				Build    map[string]any `json:"build"`
				Children []any          `json:"children"`
			} `json:"children"`
		} `json:"root"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	require.Equal(t, "running", doc.State)
	require.NotNil(t, doc.Warnings, "always an array")
	require.Equal(t, "PROJ-API", doc.Root.PlanKey)
	require.Equal(t, "failed", doc.Root.Status)
	require.Equal(t, "PROJ-API-142", doc.Root.Build["key"])
	require.Len(t, doc.Root.Children, 3)
	audit := doc.Root.Children[1]
	require.Equal(t, "not_run", audit.Status)
	require.Nil(t, audit.Build)
	require.NotNil(t, audit.Children)
	require.NotContains(t, string(data), `"progress"`)
}

// logTree is a root whose "Run integration builds" job started LAB-INT-46
// with a script.
func logTree() app.Tree {
	in := tnode("LAB-INT-46", provider.StateSuccess, 60*time.Second)
	in.FromLog, in.Stage = true, "Run integration builds"
	in.Build.PlanName = "Integrate.ForNextRelease"
	root := tnode("PROJ-API-142", provider.StateSuccess, 120*time.Second, in)
	return app.Tree{Root: root, State: app.TreeState(root)}
}

func TestTreeTextNamesTheStageThatStartedALinkedBuild(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, TreeText(o, logTree(), false))
	require.Regexp(t, `LAB-INT-46 \(Integrate.ForNextRelease\) +✓ success +1m0?0?s? +started by stage Run integration builds`, buf.String())
}

func TestTreeJSONMarksALinkedBuild(t *testing.T) {
	data, err := json.Marshal(TreeJSON(logTree(), false))
	require.NoError(t, err)
	var doc struct {
		Root struct {
			FromLog  bool `json:"from_log"`
			Children []struct {
				FromLog bool   `json:"from_log"`
				Stage   string `json:"stage"`
			} `json:"children"`
		} `json:"root"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	require.False(t, doc.Root.FromLog)
	require.True(t, doc.Root.Children[0].FromLog)
	require.Contains(t, string(data), `"plan_name":"Integrate.ForNextRelease"`)
	require.Equal(t, "Run integration builds", doc.Root.Children[0].Stage)
}

// TestTreeTextNestsALinkedBuildUnderItsStage: a build a job's script started
// sits under the stage of that job, so the stage reads as running it.
func TestTreeTextNestsALinkedBuildUnderItsStage(t *testing.T) {
	tr := logTree()
	tr.Root.Build.Stages = []provider.Stage{
		stage("Validate", provider.StateSuccess, 30*time.Second),
		stage("Run integration builds", provider.StateSuccess, 60*time.Second),
		stage("Publish", provider.StateSuccess, 30*time.Second),
	}
	o, buf := testOut(false)
	require.NoError(t, TreeText(o, tr, false))
	lines := strings.Split(buf.String(), "\n")
	require.Contains(t, lines[1], "├─ stage Validate")
	require.Contains(t, lines[2], "├─ stage Run integration builds")
	require.Contains(t, lines[3], "│  └─ LAB-INT-46 (Integrate.ForNextRelease)")
	require.NotContains(t, lines[3], "started by stage", "the stage above says it")
	require.Contains(t, lines[4], "└─ stage Publish")
}

func TestTreeProgressText(t *testing.T) {
	c := func(d, n int) app.TreeCount { return app.TreeCount{Done: d, Total: n} }
	for _, tc := range []struct {
		p    app.TreeProgress
		want string
	}{
		{app.TreeProgress{}, "reading the tree"},
		{app.TreeProgress{Projects: c(1, 2)}, "listing plans 1/2 projects"},
		{app.TreeProgress{Projects: c(2, 2), Plans: c(12, 40)}, "reading builds 12/40 plans"},
		{app.TreeProgress{Projects: c(3, 3), Plans: c(2, 6), Builds: c(4, 4), Jobs: c(3, 9)}, "reading builds 2/6 plans · reading logs 3/9 jobs"},
		{app.TreeProgress{Projects: c(2, 2), Plans: c(4, 4), Builds: c(3, 3)}, "reading details 3/3 builds"},
	} {
		require.Equal(t, tc.want, TreeProgressText(tc.p))
	}
}
