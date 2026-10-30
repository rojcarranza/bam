package view

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"
	"github.com/stretchr/testify/require"
)

var treeAt = time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)

func treeEv(typ app.TreeEventType, t app.Tree) app.TreeEvent {
	return app.TreeEvent{Type: typ, Time: treeAt, Tree: t}
}

func twoTrees() (app.Tree, app.Tree) {
	running := tnode("PROJ-API-142", provider.StateRunning, 0)
	r := app.Tree{Root: running, State: provider.StateRunning}
	child := tnode("OPS-DEPLOY-88", provider.StateSuccess, time.Minute)
	done := tnode("PROJ-API-142", provider.StateSuccess, time.Minute, child)
	return r, app.Tree{Root: done, State: provider.StateSuccess}
}

func TestTreeLinesPrintsChangesThenTheTree(t *testing.T) {
	first, last := twoTrees()
	o, buf := testOut(false)
	l := NewTreeLines(o, false)
	l.Event(treeEv(app.TreeUpdate, first))
	l.Event(treeEv(app.TreeUpdate, last))
	l.Event(treeEv(app.TreeDone, last))
	out := buf.String()
	require.True(t, strings.HasPrefix(out, "2026-10-08T09:00:00Z PROJ-API-142 running\n"+
		"2026-10-08T09:00:00Z PROJ-API-142 success\n"+
		"2026-10-08T09:00:00Z OPS-DEPLOY-88 success\n"), out)
	require.Contains(t, out, "└─ OPS-DEPLOY-88", "the final tree follows")
}

func TestTreeLiveRedrawsInPlace(t *testing.T) {
	first, last := twoTrees()
	o, buf := testOut(true)
	l := NewTreeLive(o, nil, false)
	l.Event(treeEv(app.TreeUpdate, first))
	l.Event(treeEv(app.TreeUpdate, last))
	require.Contains(t, buf.String(), "\x1b[1A\x1b[J", "the one-line first tree is redrawn")
	require.True(t, strings.HasSuffix(buf.String(), "OPS-DEPLOY-88  ✓ success  1m00s\n"), buf.String())
}

func TestTreeLiveTickRedrawsARunningTreeWithItsNewTimes(t *testing.T) {
	running := tnode("PROJ-API-142", provider.StateRunning, 0)
	running.Build.StartedAt = fixedNow.Add(-45 * time.Second)
	now := fixedNow
	o, buf := testOut(true)
	o.Now = func() time.Time { return now }
	l := NewTreeLive(o, nil, false)
	l.Tick()
	require.Empty(t, buf.String(), "no tree yet, nothing to draw")
	l.Event(treeEv(app.TreeUpdate, app.Tree{Root: running, State: provider.StateRunning}))
	require.Contains(t, buf.String(), "▸ running  45s")
	now = now.Add(5 * time.Second)
	l.Tick()
	require.True(t, strings.HasSuffix(buf.String(), "\x1b[1A\x1b[JPROJ-API-142  ▸ running  50s\n"), buf.String())

	_, last := twoTrees()
	l.Event(treeEv(app.TreeDone, last))
	before := buf.Len()
	l.Tick()
	require.Equal(t, before, buf.Len(), "a finished tree is not redrawn")
}

// A build in its first second has a time of 0s; "–" is only for a time bam
// does not know.
func TestTreeLiveShowsAJustStartedBuildAt0s(t *testing.T) {
	for name, tc := range map[string]struct {
		started, queued time.Time
		want            string
	}{
		"started 400ms ago":         {started: fixedNow.Add(-400 * time.Millisecond), want: "▸ running  0s"},
		"server clock 2s ahead":     {started: fixedNow.Add(2 * time.Second), want: "▸ running  0s"},
		"only queued 400ms ago":     {queued: fixedNow.Add(-400 * time.Millisecond), want: "▸ running  0s"},
		"no start or queue time":    {want: "▸ running  –"},
		"started 2.6s ago, whole s": {started: fixedNow.Add(-2600 * time.Millisecond), want: "▸ running  2s"},
	} {
		t.Run(name, func(t *testing.T) {
			running := tnode("PROJ-API-142", provider.StateRunning, 0)
			running.Build.StartedAt, running.Build.QueuedAt = tc.started, tc.queued
			o, buf := testOut(true)
			o.Now = func() time.Time { return fixedNow }
			NewTreeLive(o, nil, false).Event(treeEv(app.TreeUpdate, app.Tree{Root: running, State: provider.StateRunning}))
			require.Equal(t, "PROJ-API-142  "+tc.want+"\n", buf.String())
		})
	}
}

func TestTreeLiveShowsAndMovesARunningBuildsEstimate(t *testing.T) {
	docs := tnode("PROJ-DOCS-30", provider.StateRunning, 0)
	docs.Build.StartedAt = fixedNow.Add(-4 * time.Minute)
	docs.Progress = provider.Progress{Valid: true, Average: 10 * time.Minute, Elapsed: 4 * time.Minute, Remaining: 6 * time.Minute, Percent: 0.4}
	docs.ProgressAt = fixedNow
	root := tnode("PROJ-API-142", provider.StateSuccess, time.Minute, docs)
	now := fixedNow
	o, buf := testOut(true)
	o.Now = func() time.Time { return now }
	l := NewTreeLive(o, nil, false)
	l.Event(treeEv(app.TreeUpdate, app.Tree{Root: root, State: provider.StateRunning}))
	require.Contains(t, buf.String(), "└─ PROJ-DOCS-30  ▸ running  4m00s  [##########--------------]  40%  ~6m00s left\n")
	now = now.Add(time.Minute)
	l.Tick()
	require.True(t, strings.HasSuffix(buf.String(), "└─ PROJ-DOCS-30  ▸ running  5m00s  [############------------]  50%  ~5m00s left\n"), buf.String())
}

func TestTreeLiveFitsTheEstimateToANarrowTerminal(t *testing.T) {
	docs := tnode("PROJ-DOCS-30", provider.StateRunning, 0)
	docs.Build.StartedAt = fixedNow.Add(-4 * time.Minute)
	docs.Progress = provider.Progress{Valid: true, Average: 10 * time.Minute, Elapsed: 4 * time.Minute, Remaining: 6 * time.Minute, Percent: 0.4}
	docs.ProgressAt = fixedNow
	root := tnode("PROJ-API-142", provider.StateSuccess, time.Minute, docs)
	for _, width := range []int{60, 45} {
		o, buf := testOut(true)
		o.Width = width
		NewTreeLive(o, nil, false).Event(treeEv(app.TreeUpdate, app.Tree{Root: root, State: provider.StateRunning}))
		for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
			require.Less(t, style.Width(line), width, "width %d: %q would wrap", width, line)
		}
		if width == 60 {
			require.Contains(t, buf.String(), "running  4m00s   40%  ~6m00s left\n", "the numbers outlast the cells")
		}
	}
	o, buf := testOut(true)
	o.Width = 30
	NewTreeLive(o, nil, false).Event(treeEv(app.TreeUpdate, app.Tree{Root: root, State: provider.StateRunning}))
	require.NotContains(t, buf.String(), "%", "no room left: no estimate rather than a wrapped one")
}

func TestTreeLiveRedrawCountsWrappedLines(t *testing.T) {
	root := tnode("PROJ-API-142", provider.StateRunning, 0)
	root.Build.PlanName = "A plan with a name long enough to wrap twice"
	o, buf := testOut(true)
	o.Width = 30
	l := NewTreeLive(o, nil, false)
	l.Event(treeEv(app.TreeUpdate, app.Tree{Root: root, State: provider.StateRunning}))
	first := buf.String()
	rows := (style.Width(strings.TrimSuffix(first, "\n")) + o.Width - 1) / o.Width
	require.Greater(t, rows, 1)
	l.Tick()
	require.True(t, strings.HasPrefix(buf.String()[len(first):], fmt.Sprintf("\x1b[%dA\x1b[J", rows)), "the cursor goes up over every row the line took")
}

func TestTreeNDJSONWritesATreePerUpdate(t *testing.T) {
	first, last := twoTrees()
	var b bytes.Buffer
	n := NewTreeNDJSON(Out{W: &b}, false)
	n.Event(treeEv(app.TreeUpdate, first))
	n.Event(treeEv(app.TreeUpdate, last))
	n.Event(treeEv(app.TreeDone, last))
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	require.Len(t, lines, 2, "done repeats the last update, so it is not written")
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &doc))
	require.Equal(t, "success", doc["state"])
}

func loadingEv(done, total int) app.TreeEvent {
	return app.TreeEvent{Type: app.TreeLoading, Progress: app.TreeProgress{Plans: app.TreeCount{Done: done, Total: total}}}
}

func TestTreeLiveShowsProgressOnItsOwnWriterAndClearsIt(t *testing.T) {
	first, _ := twoTrees()
	o, out := testOut(true)
	var prog bytes.Buffer
	l := NewTreeLive(o, &prog, false)
	l.Event(loadingEv(1, 4))
	require.Empty(t, out.String(), "no tree yet")
	require.Equal(t, "\r\x1b[Kreading builds 1/4 plans", prog.String())
	l.Event(treeEv(app.TreeUpdate, first))
	require.Equal(t, "\r\x1b[Kreading builds 1/4 plans\r\x1b[K", prog.String(), "cleared before the tree")
	require.NotEmpty(t, out.String())
}

// An interrupted watch ends without a tree: Stop must clear the line before
// the CLI prints its "still running" note.
func TestTreeLiveStopClearsAShownLine(t *testing.T) {
	o, _ := testOut(true)
	var prog bytes.Buffer
	l := NewTreeLive(o, &prog, false)
	l.Event(loadingEv(1, 4))
	l.Stop()
	require.True(t, strings.HasSuffix(prog.String(), "\r\x1b[K"), "%q", prog.String())
	var none *TreeLive
	none.Stop()
}

func TestTreeLiveWithoutAProgressWriterDropsLoading(t *testing.T) {
	o, out := testOut(true)
	l := NewTreeLive(o, nil, false)
	l.Event(app.TreeEvent{Type: app.TreeLoading})
	l.Stop()
	require.Empty(t, out.String())
}

func TestPipeAndNDJSONRenderersIgnoreLoading(t *testing.T) {
	var b bytes.Buffer
	o := Out{W: &b}
	NewTreeLines(o, false).Event(app.TreeEvent{Type: app.TreeLoading})
	NewTreeNDJSON(o, false).Event(app.TreeEvent{Type: app.TreeLoading})
	require.Empty(t, b.String())
}
