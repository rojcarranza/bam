package cli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func statsHarness(t *testing.T) *harness {
	h := newHarness(t)
	at := fixedNow.Add(-48 * time.Hour)
	b := func(n int, st provider.State) provider.Build {
		return provider.Build{Key: "PROJ-BUILD-" + itoaStats(n), PlanKey: "PROJ-BUILD", Number: n, State: st,
			StartedAt: at.Add(time.Duration(n) * time.Hour), FinishedAt: at.Add(time.Duration(n)*time.Hour + time.Minute), Duration: time.Minute}
	}
	h.fake.History["PROJ-BUILD"] = []provider.Build{b(3, provider.StateSuccess), b(2, provider.StateSuccess), b(1, provider.StateFailed)}
	return h
}

func itoaStats(n int) string { return string(rune('0' + n)) }

func TestPlanStatsCommand(t *testing.T) {
	h := statsHarness(t)
	require.Equal(t, 0, h.run("plan", "stats", "build"), h.stderr.String())
	require.Contains(t, h.stdout.String(), "PROJ-BUILD")
	require.Contains(t, h.stdout.String(), "67%")

	h = statsHarness(t)
	require.Equal(t, 0, h.run("plan", "stats", "build", "--json"), h.stderr.String())
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	require.Equal(t, float64(2), doc["passed"])
	require.Equal(t, "PROJ-BUILD", doc["plan_key"])
}

func TestPlanStatsLastRange(t *testing.T) {
	for n, try := range map[string]string{"0": "-n 1", "101": "-n 100"} {
		h := statsHarness(t)
		require.Equal(t, 2, h.run("plan", "stats", "build", "--last", n))
		require.Contains(t, h.stderr.String(), "--last takes 1 to 100, not "+n)
		require.Contains(t, h.stderr.String(), "try: bam plan stats build "+try)
		require.Empty(t, h.connectOpts, "no network before the check")
	}
}

func TestPlanStatsUsesThePresetsBranch(t *testing.T) {
	h := newHarness(t)
	h.fake.History["PROJ-PROV12"] = []provider.Build{{Key: "PROJ-PROV12-8", PlanKey: "PROJ-PROV12", Number: 8, State: provider.StateSuccess, Duration: time.Minute}}
	require.Equal(t, 0, h.run("plan", "stats", "provision-lab", "--json"), h.stderr.String())
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	require.Equal(t, "PROJ-PROV12", doc["plan_key"])
	require.Equal(t, "develop", doc["branch"])
}
