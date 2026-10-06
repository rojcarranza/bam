package tui

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func failedRec() *app.SetRecord {
	return &app.SetRecord{ID: "release-20261006T084800Z", Set: "release", Status: app.SetFailed,
		StartedAt: goldenNow.Add(-12 * time.Minute), Stages: []app.StageRecord{
			{Runs: []app.RunRecord{{Target: "smoke", Name: "smoke", BuildKey: "PROJ-PROV12-8", State: provider.StateSuccess}}},
			{Runs: []app.RunRecord{
				{Target: "build", Name: "build[env=qa]", BuildKey: "PROJ-BUILD-44", State: provider.StateFailed, Reruns: 1},
				{Target: "build", Name: "build[env=prod]", BuildKey: "PROJ-BUILD-45", State: provider.StateSuccess},
			}}}}
}

// setScreenModel is the set screen on release with rec loaded.
func setScreenModel(t *testing.T, rec *app.SetRecord, elsewhere string) Model {
	m := setsModel(t)
	next, _ := m.openSet()
	m = next.(Model)
	m, _ = send(m, setLoadedMsg{Gen: m.setGen, Rec: rec, Elsewhere: elsewhere})
	return m
}

func TestSetScreenGoldens(t *testing.T) {
	requireGolden(t, "set-failed-80x24", setScreenModel(t, failedRec(), "").View())
	requireGolden(t, "set-elsewhere-80x24", setScreenModel(t, &app.SetRecord{ID: "release-20261006T091500Z", Set: "release",
		Status: app.SetRunning, StartedAt: goldenNow}, "pid 77 on desk, since 09:15").View())
	requireGolden(t, "set-new-80x24", setScreenModel(t, nil, "").View())
}

func TestSetScreenOffersOnlyTheKeysThatApply(t *testing.T) {
	failed := setScreenModel(t, failedRec(), "")
	require.Contains(t, failed.keyLine(), "F rerun failed")
	require.NotContains(t, failed.keyLine(), "U resume")
	interrupted := failedRec()
	interrupted.Status = app.SetInterrupted
	m := setScreenModel(t, interrupted, "")
	require.Contains(t, m.keyLine(), "U resume")
	require.NotContains(t, m.keyLine(), "F rerun")
	require.Contains(t, setScreenModel(t, nil, "").keyLine(), "R run")
}

func TestSetScreenBuildReturnsToTheSet(t *testing.T) {
	m := setScreenModel(t, failedRec(), "")
	m, _ = send(m, mkKey("j")) // build[env=qa], the failed run
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenColumns, m.screen)
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenSet, m.screen, "esc from a set's build returns to the set")
}

func TestSetScreenRefusesWhenHeldElsewhere(t *testing.T) {
	m := setScreenModel(t, &app.SetRecord{ID: "release-1", Set: "release", Status: app.SetRunning}, "pid 77 on desk, since 09:15")
	for _, k := range []string{"R", "U", "F"} {
		next, _ := send(m, mkKey(k))
		require.Equal(t, screenSet, next.screen, k)
		require.Contains(t, next.status, "running in another bam (pid 77 on desk, since 09:15)", k)
	}
}

func TestSetScreenEscReturnsToTheSetsTable(t *testing.T) {
	m := setScreenModel(t, failedRec(), "")
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
	require.Equal(t, homeSets, m.home.view)
}

// The key line must fit the status bar at 80 columns in its longest form,
// or the bar drops it whole.
func TestSetScreenKeyLineFitsAt80Columns(t *testing.T) {
	m := setScreenModel(t, failedRec(), "")
	require.Contains(t, m.statusBar(80), "F rerun failed")
	require.Contains(t, m.statusBar(80), "R run")
}
