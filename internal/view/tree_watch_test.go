package view

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
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
	l := NewTreeLive(o, false)
	l.Event(treeEv(app.TreeUpdate, first))
	l.Event(treeEv(app.TreeUpdate, last))
	require.Contains(t, buf.String(), "\x1b[1A\x1b[J", "the one-line first tree is redrawn")
	require.True(t, strings.HasSuffix(buf.String(), "OPS-DEPLOY-88  ✓ success  1m00s\n"), buf.String())
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
