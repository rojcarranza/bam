package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// treeHarness adds PROJ-PROV-9, a child of PROJ-BUILD-482, ending in final.
func treeHarness(t *testing.T, final provider.State) *harness {
	t.Helper()
	h := newHarness(t)
	child := provider.Build{Key: "PROJ-PROV-9", URL: workOrigin + "/browse/PROJ-PROV-9", PlanKey: "PROJ-PROV", Number: 9,
		State: final, Reason: "Child of PROJ-BUILD-482", StartedAt: fixedNow.Add(-10 * time.Minute)}
	if final == provider.StateFailed {
		child.Stages = []provider.Stage{{Name: "Apply", State: provider.StateFailed,
			Jobs: []provider.Job{{Key: "PROJ-PROV-TF-9", Name: "Terraform", State: provider.StateFailed}}}}
	}
	h.fake.History["PROJ-PROV"] = []provider.Build{child}
	return h
}

func TestBuildTreeExitCodes(t *testing.T) {
	h := treeHarness(t, provider.StateSuccess)
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482"))
	assert.Contains(t, h.stdout.String(), "PROJ-BUILD-482")
	assert.Contains(t, h.stdout.String(), "└─ PROJ-PROV-9")

	h = treeHarness(t, provider.StateFailed)
	assert.Equal(t, 1, h.run("build", "tree", "PROJ-BUILD-482"))
	assert.Contains(t, h.stdout.String(), "next: bam logs PROJ-PROV-9 --failed")
}

func TestBuildTreeJSON(t *testing.T) {
	h := treeHarness(t, provider.StateSuccess)
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "success", doc["state"])
	kids := doc["root"].(map[string]any)["children"].([]any)
	require.Len(t, kids, 1)
	assert.Equal(t, "PROJ-PROV", kids[0].(map[string]any)["plan_key"])
}

func TestBuildTreeDepthIsCheckedFirst(t *testing.T) {
	h := treeHarness(t, provider.StateSuccess)
	assert.Equal(t, 2, h.run("build", "tree", "PROJ-BUILD-482", "--depth", "0"))
	assert.Contains(t, h.stderr.String(), "--depth takes 1 or more")
	assert.Empty(t, h.connectOpts, "no connection is made")
}

func TestBuildTreeStillRunning(t *testing.T) {
	h := treeHarness(t, provider.StateRunning)
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482"))
	assert.Contains(t, h.stderr.String(), "still running: bam build tree PROJ-BUILD-482 --watch")
}

func TestBuildTreeWatch(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	assert.Equal(t, 1, h.run("build", "tree", "PROJ-BUILD-482", "--watch"))
	assert.Contains(t, h.stdout.String(), "PROJ-PROV-9 failed")
	assert.Contains(t, h.stdout.String(), "└─ PROJ-PROV-9", "the final tree")
}

func TestBuildTreeWatchInterrupted(t *testing.T) {
	h := treeHarness(t, provider.StateRunning)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Equal(t, 130, h.runCtx(ctx, "build", "tree", "PROJ-BUILD-482", "--watch"))
	assert.Contains(t, h.stderr.String(), "still running: bam build tree PROJ-BUILD-482 --watch")
	assert.Empty(t, h.fake.Stopped)
}
