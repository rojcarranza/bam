package cli

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runningLab makes PROJ-PROV12-8 a running build with a server estimate.
func runningLab(h *harness) {
	b := h.fake.History["PROJ-PROV12"][0]
	b.State = provider.StateRunning
	b.StartedAt = fixedNow.Add(-99 * time.Second)
	h.fake.History["PROJ-PROV12"] = []provider.Build{b}
	h.fake.Progressions = map[string][]provider.Progress{"PROJ-PROV12-8": {{
		Valid: true, Average: 3 * time.Minute, Elapsed: 99 * time.Second, Remaining: 81 * time.Second, Percent: 0.55, Stage: "Apply"}}}
}

func TestBuildShowPrintsTheEstimateWhileRunning(t *testing.T) {
	h := newHarness(t)
	runningLab(h)

	require.Equal(t, 0, h.run("build", "show", "PROJ-PROV12-8"))

	assert.Contains(t, h.stdout.String(), "Estimate")
	assert.Contains(t, h.stdout.String(), "55%")
	assert.Contains(t, h.stdout.String(), "~1m21s left")
}

func TestBuildShowJSONCarriesProgress(t *testing.T) {
	h := newHarness(t)
	runningLab(h)

	require.Equal(t, 0, h.run("build", "show", "PROJ-PROV12-8", "--json"))

	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	pr, ok := doc["progress"].(map[string]any)
	require.True(t, ok, "progress object: %s", h.stdout.String())
	assert.Equal(t, 0.55, pr["percent"])
	assert.Equal(t, float64(81000), pr["remaining_ms"])
}

func TestBuildShowOmitsProgressForAFinishedBuild(t *testing.T) {
	h := newHarness(t)

	require.Equal(t, 0, h.run("build", "show", "PROJ-BUILD-482", "--json"))

	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.NotContains(t, doc, "progress")
}

func TestBuildShowSurvivesAServerWithoutEstimates(t *testing.T) {
	h := newHarness(t)
	runningLab(h)
	h.fake.ProgressErr = errs.Bamboof("nope").Wrap(errs.ErrUnsupported)

	require.Equal(t, 0, h.run("build", "show", "PROJ-PROV12-8"))

	assert.NotContains(t, h.stdout.String(), "Estimate")
}

func TestBuildListShowsAnETAForRunningBuilds(t *testing.T) {
	h := newHarness(t)
	runningLab(h)

	require.Equal(t, 0, h.run("build", "list", "provision-lab"))

	assert.Contains(t, h.stdout.String(), "ETA")
	assert.Contains(t, h.stdout.String(), "1m21s")
}

func TestBuildListAsksOnlyAboutRunningBuilds(t *testing.T) {
	h := newHarness(t)

	require.Equal(t, 0, h.run("build", "list", "PROJ-BUILD"))

	assert.NotContains(t, h.stdout.String(), "ETA")
	assert.Equal(t, 0, h.fake.ProgressCalls("PROJ-BUILD-482"), "a finished build has no estimate to ask for")
}

// A server without progress still gets a bar, from the plan's history.
func TestBuildShowEstimatesFromHistory(t *testing.T) {
	h := newHarness(t)
	h.fake.ProgressErr = errs.Bamboof("this Bamboo does not report build progress").Wrap(errs.ErrUnsupported)
	ok := func(n int, d time.Duration) provider.Build {
		return provider.Build{Key: fmt.Sprintf("PROJ-BUILD-%d", n), PlanKey: "PROJ-BUILD", Number: n, State: provider.StateSuccess, Duration: d}
	}
	running := provider.Build{Key: "PROJ-BUILD-490", URL: workOrigin + "/browse/PROJ-BUILD-490", PlanKey: "PROJ-BUILD", Number: 490,
		State: provider.StateRunning, StartedAt: fixedNow.Add(-time.Minute)}
	h.fake.History["PROJ-BUILD"] = []provider.Build{running, ok(489, 4*time.Minute), ok(488, 4*time.Minute), ok(487, 4*time.Minute)}
	require.Equal(t, 0, h.run("build", "show", "PROJ-BUILD-490", "--json"), h.stderr.String())
	require.Contains(t, h.stdout.String(), `"source": "history"`)
}
