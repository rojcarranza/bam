package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// failedPlan is the plan screen on the failed build PROJ-BUILD-44, its stages
// read in and Stages focused. The server holds the whole build, as Bamboo
// answers it to a read of that one build, and the failed job's log.
func failedPlan(t *testing.T) Model {
	t.Helper()
	m := planWithStages(t)
	m.focus = focusStages
	f := m.svc.P.(*fake.Provider)
	f.History["PROJ-BUILD"] = []provider.Build{sampleBuild()}
	// Maven prints its error lines from the start of the line.
	f.Logs["PROJ-BUILD-INT-44"] = []string{
		"Running integration suite",
		"[ERROR] ConnectionRefused: bamboo.lab.example:5432",
		"[ERROR] 3 tests failed",
	}
	return m
}

// Piece 3 of SCR-54: the Stages panel names the failed tests.
func TestStagesNamesTheFailedTestsAndCountsTheRest(t *testing.T) {
	m := testModel()
	b := sampleBuild()
	b.FailedTests = nil
	for i := 1; i <= 7; i++ {
		b.FailedTests = append(b.FailedTests, fmt.Sprintf("com.example.OrderTest.case%d", i))
	}
	b.FailedTests[0] = "com.example.VeryLongSuiteName." + strings.Repeat("x", 80)
	m.detail, m.expanded = &b, defaultExpanded(b)

	body := ansi.Strip(m.stagesBody(56, 24))
	require.Contains(t, body, "7 failed tests")
	require.Contains(t, body, "com.example.OrderTest.case5")
	require.NotContains(t, body, "case6", "only five names are listed")
	require.Contains(t, body, "+2 more")
	for _, l := range strings.Split(body, "\n") {
		require.LessOrEqual(t, len([]rune(l)), 56, "a long name is cut to the panel: %q", l)
	}
}

func TestStagesKeepsEveryStageRowWhileListingFailedTests(t *testing.T) {
	m := failedPlan(t)
	v := ansi.Strip(m.View())
	for _, want := range []string{"Deploy", "3 failed tests", "  a", "  b", "  c"} {
		require.Contains(t, v, want)
	}
}

func TestIShowsTheFailedJobsErrorsAndTheLikelyCause(t *testing.T) {
	m := failedPlan(t)
	m, cmd := send(m, mkKey("i"))
	require.Equal(t, overlayInfo, m.overlay)
	require.NotNil(t, cmd)
	m, _ = send(m, cmd())
	require.Equal(t, overlayInfo, m.overlay)
	require.Equal(t, "Why PROJ-BUILD-44 failed", m.infoPane.title)
	v := ansi.Strip(m.View())
	require.Contains(t, v, "Test / Integration")
	require.Contains(t, v, "ConnectionRefused: bamboo.lab.example:5432")
	require.Contains(t, v, "Likely cause")
}

// Issue #249: the plan screen opens on the Builds list, whose row is what
// Bamboo's result list answers, with no stages and no failed tests. i must
// still show what bam build investigate shows for the build.
func TestIOnTheBuildsListShowsTheFailedTestsAndTheLikelyCause(t *testing.T) {
	m := failedPlan(t)
	m.focus = focusBuilds
	row, ok := m.builds.selected()
	require.True(t, ok)
	require.Equal(t, "PROJ-BUILD-44", row.Key)
	require.Empty(t, row.Stages, "a listing carries no stages")
	require.Empty(t, row.FailedTests, "a listing carries no failed tests")

	m, cmd := send(m, mkKey("i"))
	require.NotNil(t, cmd)
	m, _ = send(m, cmd())
	require.Equal(t, overlayInfo, m.overlay)
	v := ansi.Strip(m.View())
	require.Contains(t, v, "Test / Integration (INT)  first error at line 2")
	require.Contains(t, v, "Failed tests: a, b, c")
	require.Contains(t, v, "Likely cause: [ERROR] ConnectionRefused: bamboo.lab.example:5432")
}

func TestIOnABuildThatDidNotFailSaysSo(t *testing.T) {
	m := failedPlan(t)
	b := *m.detail
	b.State = provider.StateSuccess
	m.detail = &b
	m, cmd := send(m, mkKey("i"))
	require.Equal(t, overlayNone, m.overlay)
	require.Nil(t, cmd)
	require.Contains(t, m.status, "did not fail")
}

func restartableFailedPlan(t *testing.T) Model {
	t.Helper()
	m := failedPlan(t)
	f := m.svc.P.(*fake.Provider)
	failed := *m.detail
	running := failed
	running.State = provider.StateRunning
	f.Sequences = map[string][]provider.Build{"PROJ-BUILD-44": {failed, running}}
	return m
}

func TestFAsksThenRerunsTheFailedJobsAndWatchesTheBuild(t *testing.T) {
	m := restartableFailedPlan(t)
	f := m.svc.P.(*fake.Provider)

	m, cmd := send(m, mkKey("F"))
	require.Equal(t, overlayConfirm, m.overlay)
	require.Nil(t, cmd)
	require.Equal(t, "Rerun the failed jobs of PROJ-BUILD-44?", m.confirm.Prompt)
	require.Empty(t, f.Restarted, "nothing restarts before the answer")

	m, cmd = send(m, mkKey("y"))
	require.NotNil(t, cmd)
	m, cmd = send(m, cmd())
	require.Equal(t, []string{"PROJ-BUILD-44"}, f.Restarted)
	require.Equal(t, "restarted PROJ-BUILD-44", m.status)
	require.Equal(t, provider.StateRunning, m.detail.State)
	require.NotNil(t, cmd, "the restarted build is read and watched again")
	require.NotNil(t, m.watchCancel, "a watch is running")
}

func TestFAnswerNoRestartsNothing(t *testing.T) {
	m := restartableFailedPlan(t)
	m, _ = send(m, mkKey("F"))
	m, cmd := send(m, mkKey("n"))
	require.Equal(t, overlayNone, m.overlay)
	require.Nil(t, cmd)
	require.Empty(t, m.svc.P.(*fake.Provider).Restarted)
}

func TestFRefusesABuildThatSucceededOrStillRuns(t *testing.T) {
	for state, want := range map[provider.State]string{
		provider.StateSuccess: "succeeded",
		provider.StateRunning: "has not finished",
	} {
		m := failedPlan(t)
		b := *m.detail
		b.State = state
		m.detail = &b
		m, cmd := send(m, mkKey("F"))
		require.Equal(t, overlayNone, m.overlay, state)
		require.Nil(t, cmd)
		require.Contains(t, m.status, want)
	}
}

func TestFSaysWhenTheServerCannotRestart(t *testing.T) {
	m := failedPlan(t)
	m.svc.P.(*fake.Provider).NoRestart = true
	m, _ = send(m, mkKey("F"))
	require.Equal(t, overlayNone, m.overlay)
	require.Contains(t, m.status, "cannot restart")
}

func TestFShowsARestartErrorAndLeavesTheBuildAlone(t *testing.T) {
	m := restartableFailedPlan(t)
	m.svc.P.(*fake.Provider).RestartErr = fmt.Errorf("boom")
	m, _ = send(m, mkKey("F"))
	m, cmd := send(m, mkKey("y"))
	m, _ = send(m, cmd())
	require.Error(t, m.err)
	require.Equal(t, provider.StateFailed, m.detail.State)
}
