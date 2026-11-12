package view

import (
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-11 P16): the status list and its prompt line.
func statusEntries() []app.StatusEntry {
	at := fixedNow
	return []app.StatusEntry{
		{Here: true, Run: app.JournalRun{BuildKey: "PROJ-BUILD-483", PlanKey: "PROJ-BUILD", Target: "build", Server: "work", StartedAt: at.Add(-3 * time.Hour)},
			Build: provider.Build{State: provider.StateRunning}, Progress: provider.Progress{Valid: true, Percent: 0.55}},
		{Run: app.JournalRun{BuildKey: "OPS-DEP-7", PlanKey: "OPS-DEP", Branch: "release", Server: "lab", StartedAt: at.Add(-time.Hour)},
			Build: provider.Build{State: provider.StateFailed}},
		{Run: app.JournalRun{BuildKey: "PROJ-BUILD-480", PlanKey: "PROJ-BUILD", Target: "build", Server: "work", StartedAt: at.Add(-5 * time.Hour)},
			Build: provider.Build{State: provider.StateRunning}},
	}
}

func TestStatusGolden(t *testing.T) {
	o, buf := testOut(true)
	require.NoError(t, Status(o, statusEntries(), func(r app.JournalRun) string { return "https://bamboo.example.com/browse/" + r.BuildKey }))
	golden(t, "status", buf.String())
}

func TestStatusShortLine(t *testing.T) {
	assert.Equal(t, "PROJ-BUILD-483 running 55% (+1 running)", StatusShort(statusEntries()))
	assert.Equal(t, "", StatusShort(nil))
}
