package view

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// timedTree is a root that ran for 10 minutes, two children that ran side by
// side (one waited 90 seconds for an agent), a build Bamboo gave no times for
// and a build that is still running.
func timedTree() app.Tree {
	t0 := fixedNow.Add(-time.Hour)
	at := func(n *app.TreeNode, queued, started time.Duration, run time.Duration) *app.TreeNode {
		n.Build.QueuedAt = t0.Add(queued)
		n.Build.StartedAt = t0.Add(started)
		n.Build.QueueDuration = started - queued
		n.Build.Duration = run
		n.Build.FinishedAt = t0.Add(started + run)
		return n
	}
	deploy := at(tnode("OPS-DEPLOY-88", provider.StateSuccess, 0), 10*time.Minute, 10*time.Minute+90*time.Second, 5*time.Minute)
	docs := at(tnode("PROJ-DOCS-30", provider.StateSuccess, 0), 10*time.Minute, 10*time.Minute+time.Second, 2*time.Minute)
	smoke := at(tnode("OPS-SMOKE-51", provider.StateFailed, 0), 16*time.Minute+30*time.Second, 16*time.Minute+30*time.Second, 90*time.Second)
	deploy.Children = []*app.TreeNode{smoke}
	quiet := tnode("OPS-AUDIT-9", provider.StateSuccess, time.Minute) // no times at all
	live := tnode("OPS-LIVE-2", provider.StateRunning, 0)
	live.Build.StartedAt = fixedNow.Add(-time.Minute)
	superseded := at(tnode("OPS-DEPLOY-87", provider.StateFailed, 0), 0, 0, time.Minute)
	superseded.Superseded = true
	root := at(tnode("PROJ-API-142", provider.StateFailed, 0), 0, 0, 10*time.Minute)
	root.Children = []*app.TreeNode{deploy, docs, quiet, live, superseded}
	return app.Tree{Root: root, State: app.TreeState(root)}
}

func TestTreeTimingTextGolden(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, TreeTimingText(o, timedTree(), false))
	golden(t, "tree_timing", buf.String())
}

func TestTreeTimingTextOnANarrowTerminalKeepsEveryLineInWidth(t *testing.T) {
	o, buf := testOut(true)
	o.Width = 60
	require.NoError(t, TreeTimingText(o, timedTree(), false))
	for _, l := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		assert.LessOrEqual(t, len([]rune(l)), 60, l)
	}
}

func TestTreeTimingTextFailedOnlyDropsASuccessfulBranch(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, TreeTimingText(o, timedTree(), true))
	assert.NotContains(t, buf.String(), "PROJ-DOCS-30")
	assert.Contains(t, buf.String(), "OPS-SMOKE-51")
}

func TestTreeTimingTextWithNoTimesSaysSo(t *testing.T) {
	root := tnode("PROJ-API-142", provider.StateSuccess, time.Minute)
	o, buf := testOut(false)
	require.NoError(t, TreeTimingText(o, app.Tree{Root: root, State: provider.StateSuccess}, false))
	assert.Contains(t, buf.String(), "no build of this tree has times to draw")
}

func TestTreeTimingJSONAddsOnlyATimingField(t *testing.T) {
	tr := timedTree()
	plain, err := json.Marshal(TreeJSON(tr, false))
	require.NoError(t, err)
	timed, err := json.Marshal(TreeTimingJSON(tr, false))
	require.NoError(t, err)
	var a, b map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(plain, &a))
	require.NoError(t, json.Unmarshal(timed, &b))
	require.Contains(t, b, "timing")
	delete(b, "timing")
	assert.Equal(t, a, b, "every existing field is unchanged")
	assert.NotContains(t, a, "timing")

	var doc struct {
		Timing TimingDoc `json:"timing"`
	}
	require.NoError(t, json.Unmarshal(timed, &doc))
	assert.Equal(t, int64(18*60*1000), doc.Timing.DurationMS)
	assert.Equal(t, 1, doc.Timing.Unfinished)
	byKey := map[string]TimingBuildDoc{}
	for _, b := range doc.Timing.Builds {
		byKey[b.Key] = b
	}
	deploy := byKey["OPS-DEPLOY-88"]
	assert.EqualValues(t, 10*60*1000, *deploy.QueuedMS)
	assert.EqualValues(t, 90*1000, *deploy.WaitMS)
	assert.EqualValues(t, 5*60*1000, *deploy.RunMS)
	assert.Equal(t, 2, byKey["OPS-SMOKE-51"].Depth)
	assert.False(t, byKey["OPS-AUDIT-9"].TimesFromBamboo)
	assert.Nil(t, byKey["OPS-AUDIT-9"].RunMS)
	assert.NotContains(t, byKey, "OPS-DEPLOY-87", "a superseded run is not part of the run")
}
