package tui

import (
	"os"
	"path/filepath"
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
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)
	m, _ = send(m, mkKey("esc")) // Stages back to Builds
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

func TestSetScreenPollsWhileHeldElsewhere(t *testing.T) {
	m := setScreenModel(t, &app.SetRecord{ID: "release-1", Set: "release", Status: app.SetRunning}, "pid 77 on desk, since 09:15")
	cmd := m.setPollCmd()
	require.NotNil(t, cmd, "a set held elsewhere is re-read")
	m.setView.elsewhere = ""
	require.Nil(t, m.setPollCmd(), "no polling when nothing is held elsewhere")
}

// Leaving a set's build any other way than esc forgets that it came from the
// set: esc later must not jump back to a set screen the user left long ago.
func TestLeavingASetBuildOtherwiseForgetsTheSet(t *testing.T) {
	m := setScreenModel(t, failedRec(), "")
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter"))
	require.True(t, m.fromSet)
	next, _ := m.runCommandLine("plans")
	m = next.(Model)
	require.False(t, m.fromSet, ":plans leaves the set behind")

	m = setScreenModel(t, failedRec(), "")
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter"))
	next, _ = m.switchServer("lab")
	require.False(t, next.(Model).fromSet, "switching server leaves the set behind")
}

func TestSOnTheSetScreenOpensTheServerPicker(t *testing.T) {
	m := setScreenModel(t, failedRec(), "")
	m, _ = send(m, mkKey("S"))
	require.Equal(t, overlayServers, m.overlay)
}

// Refreshing reads each recorded build's current state from Bamboo, so a
// set run elsewhere shows its builds moving.
func TestSetRefreshReadsLiveBuildStates(t *testing.T) {
	m := setsModel(t)
	rec := app.SetRecord{Version: 1, ID: app.NewSetID("release", goldenNow), Set: "release", Repo: "/repo",
		Status: app.SetRunning, StartedAt: goldenNow, VarNames: []string{}, Stages: []app.StageRecord{
			{Runs: []app.RunRecord{{Target: "smoke", Name: "smoke", PlanKey: "PROJ-BUILD", BuildKey: "PROJ-BUILD-44", State: provider.StateQueued}}},
		}}
	require.NoError(t, m.svc.Sets.Save(rec))
	msg := loadSetCmd(t.Context(), m.svc, "release", 1)().(setLoadedMsg)
	require.NotNil(t, msg.Rec)
	require.Equal(t, provider.StateFailed, msg.Rec.Stages[0].Runs[0].State, "the live state from Bamboo, not the recorded one")
}

// A set record that cannot be read is an error in the status bar, not a
// silent "never run".
func TestSetsLoadReportsAnUnreadableRecord(t *testing.T) {
	m := setsModel(t)
	file := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	m.svc.Sets.Dir = file
	m.deps.Sets = func() ([]app.SetInfo, error) { return setInfos(), nil }
	msg := loadSetsCmd(m.deps, m.svc, 1)()
	_, isErr := msg.(errMsg)
	require.True(t, isErr, "%#v", msg)
}
