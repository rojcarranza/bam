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

func supersededTree() app.Tree {
	newer := tnode("OPS-SMOKE-52", provider.StateSuccess, time.Minute)
	older := tnode("OPS-SMOKE-51", provider.StateFailed, 100*time.Second, tnode("OPS-AUDIT-3", provider.StateFailed, time.Second))
	older.Superseded = true
	older.Build.Stages = []provider.Stage{stage("Smoke", provider.StateFailed, 100*time.Second, "Smoke tests")}
	older.Build.FailedTests = []string{"a"}
	root := tnode("OPS-DEPLOY-88", provider.StateSuccess, 3*time.Minute, newer, older)
	return app.Tree{Root: root, State: app.TreeState(root)}
}

func TestTreeTextSupersededRun(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, TreeText(o, supersededTree(), false))
	out := buf.String()
	require.Contains(t, out, "earlier run")
	require.NotContains(t, out, "stage Smoke", "an earlier run's stages are not shown")
	require.NotContains(t, out, "OPS-AUDIT-3", "nor its children")
	require.NotContains(t, out, "next: bam logs OPS-SMOKE-51", "nor a next line")
	golden(t, "tree_superseded", out)
}

func TestTreeTextFailedOnlyDropsSupersededRuns(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, TreeText(o, supersededTree(), true))
	require.Equal(t, 1, strings.Count(buf.String(), "\n"), "nothing current failed: the root line only")
}

func TestTreeJSONSupersededStatus(t *testing.T) {
	data, err := json.Marshal(TreeJSON(supersededTree(), false))
	require.NoError(t, err)
	var doc struct {
		Root struct {
			Children []struct {
				Status   string         `json:"status"`
				Build    map[string]any `json:"build"`
				Children []any          `json:"children"`
			} `json:"children"`
		} `json:"root"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	old := doc.Root.Children[1]
	require.Equal(t, "superseded", old.Status)
	require.Equal(t, "OPS-SMOKE-51", old.Build["key"])
	require.Len(t, old.Children, 1, "its children are kept")
}

func TestRerunList(t *testing.T) {
	deploy := tnode("OPS-DEPLOY-88", provider.StateFailed, time.Minute)
	deploy.Build.Stages = []provider.Stage{stage("Deploy", provider.StateFailed, time.Minute, "Helm upgrade")}
	docs := tnode("PROJ-DOCS-30", provider.StateStopped, time.Minute)
	o, buf := testOut(false)
	require.NoError(t, RerunList(o, []*app.TreeNode{deploy, docs}))
	require.Equal(t, [][]string{
		{"Rerun", "the", "failed", "jobs", "of", "2", "builds?"},
		{"OPS-DEPLOY-88", "✗", "failed", "Deploy", "›", "Helm", "upgrade"},
		{"PROJ-DOCS-30", "⊘", "stopped"},
	}, fields(buf.String()))
	o, buf = testOut(false)
	require.NoError(t, RerunList(o, []*app.TreeNode{docs}))
	require.True(t, strings.HasPrefix(buf.String(), "Rerun the failed jobs of 1 build?\n"))
}

// fields is each line of s split on whitespace.
func fields(s string) [][]string {
	var out [][]string
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		out = append(out, strings.Fields(l))
	}
	return out
}
