package tui

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func ptr[T any](v T) *T { return &v }

func sampleBuild() provider.Build {
	return provider.Build{
		Key: "PROJ-BUILD-44", PlanKey: "PROJ-BUILD", Number: 44, Branch: "main",
		State: provider.StateFailed, Reason: "Manual run by jdoe",
		URL:      labOrigin + "/browse/PROJ-BUILD-44",
		Duration: 192 * time.Second,
		Stages: []provider.Stage{
			{Name: "Build", State: provider.StateSuccess, Duration: 42 * time.Second, Jobs: []provider.Job{
				{Key: "PROJ-BUILD-COMP-44", Name: "Compile", State: provider.StateSuccess, Duration: 28 * time.Second,
					URL: labOrigin + "/browse/PROJ-BUILD-COMP-44"},
				{Key: "PROJ-BUILD-UNIT-44", Name: "Unit tests", State: provider.StateSuccess, Duration: 14 * time.Second,
					URL: labOrigin + "/browse/PROJ-BUILD-UNIT-44"},
			}},
			{Name: "Test", State: provider.StateFailed, Duration: 150 * time.Second, Jobs: []provider.Job{
				{Key: "PROJ-BUILD-INT-44", Name: "Integration", State: provider.StateFailed, Duration: 150 * time.Second,
					URL: labOrigin + "/browse/PROJ-BUILD-INT-44"},
			}},
			{Name: "Deploy", State: provider.StateNotBuilt},
		},
		FailedTests: []string{"a", "b", "c"},
	}
}

func TestTreeCollapsedShowsStagesOnly(t *testing.T) {
	rows := buildTree(sampleBuild(), map[string]bool{})
	require.Len(t, rows, 3)
	for _, r := range rows {
		require.Equal(t, rowStage, r.Kind)
	}
	require.Equal(t, "Build", rows[0].Name)
	require.Equal(t, provider.StateNotBuilt, rows[2].State)
}

func TestTreeExpandedShowsThatStagesJobsOnly(t *testing.T) {
	rows := buildTree(sampleBuild(), map[string]bool{"Test": true})
	require.Len(t, rows, 4)
	require.Equal(t, rowStage, rows[1].Kind)
	require.Equal(t, "Test", rows[1].Name)
	require.Equal(t, rowJob, rows[2].Kind)
	require.Equal(t, "Integration", rows[2].Name)
	require.Equal(t, "PROJ-BUILD-INT-44", rows[2].Key)
	require.Equal(t, rowStage, rows[3].Kind)
	require.Equal(t, "Deploy", rows[3].Name)
}

// TestFailedStagesExpandByDefault: the failure is what the user came for.
func TestFailedStagesExpandByDefault(t *testing.T) {
	require.True(t, defaultExpanded(sampleBuild())["Test"])
	require.False(t, defaultExpanded(sampleBuild())["Build"])
}

// TestStagesBodyShowsTheTreeAndTheFailedTests: state, number and branch are
// in the Builds row and the header, so Stages starts at the reason line.
func TestStagesBodyShowsTheTreeAndTheFailedTests(t *testing.T) {
	m := testModel()
	m.detail = ptr(sampleBuild())
	m.expanded = defaultExpanded(sampleBuild())
	body := m.stagesBody(56, 20)
	require.NotContains(t, body, "#44", "the build header line is gone")
	require.Contains(t, body, "Integration")
	require.Contains(t, body, "3 failed tests")
	require.NotContains(t, body, "Compile", "a passing stage stays collapsed")
}
