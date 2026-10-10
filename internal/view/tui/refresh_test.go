package tui

import (
	"errors"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func plan(project, key string) provider.Plan {
	return provider.Plan{Key: key, Name: key, ProjectKey: project}
}

func TestPlansLoadedKeepsTheCursorOnItsPlan(t *testing.T) {
	m := testModel()
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{plan("PROJ", "PROJ-A"), plan("PROJ", "PROJ-B")}})
	m.plans.cursor = 1 // PROJ-B
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{
		plan("PROJ", "PROJ-0"), plan("PROJ", "PROJ-A"), plan("PROJ", "PROJ-B")}})
	got, _ := m.plans.selected()
	require.Equal(t, "PROJ-B", got.Key, "a new row above must not move the cursor off its plan")
}

func TestPlansLoadedKeepsTheRowsOfAFailedProject(t *testing.T) {
	m := testModel()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{plan("OPS", "OPS-N"), plan("PROJ", "PROJ-A")}})
	require.False(t, m.home.stale)
	require.Equal(t, now, m.home.loadedAt)

	boom := errors.New("bamboo returned 500")
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{plan("PROJ", "PROJ-B")},
		Failed: []string{"OPS"}, Err: boom})
	keys := []string{}
	for _, p := range m.plans.rows() {
		keys = append(keys, p.Key)
	}
	require.Equal(t, []string{"OPS-N", "PROJ-B"}, keys, "OPS keeps its old row; PROJ updates")
	require.ErrorIs(t, m.err, boom)
	require.True(t, m.home.stale)
	require.Equal(t, now, m.home.loadedAt, "a partial load is not a fresh one")
}

func TestPlansLoadedNamesAMissingProject(t *testing.T) {
	m := testModel()
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Missing: []string{"GONE"}})
	require.Equal(t, "project GONE is configured but not on lab", m.status)
}

// TestPlansLoadedClearsAPlansErrorOnceALoadFullySucceeds covers finding 5a:
// an error that came from the plans stream must not survive a load in which
// every project succeeded.
func TestPlansLoadedClearsAPlansErrorOnceALoadFullySucceeds(t *testing.T) {
	m := testModel()
	boom := errors.New("bamboo returned 500")
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{plan("PROJ", "PROJ-B")},
		Failed: []string{"OPS"}, Err: boom})
	require.ErrorIs(t, m.err, boom)

	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{plan("OPS", "OPS-N"), plan("PROJ", "PROJ-B")}})
	require.NoError(t, m.err, "a fully successful load clears an error that came from the plans stream")
}

// TestAnErrorFromAnotherStreamSurvivesAPlansRefresh covers finding 5a's other
// half: a plans refresh must never clear an error that came from a different
// stream, such as a cancel that failed.
func TestAnErrorFromAnotherStreamSurvivesAPlansRefresh(t *testing.T) {
	m := testModel()
	boom := errors.New("cannot cancel PROJ-BUILD-9")
	m, _ = send(m, errMsg{Err: boom, Stream: streamCancel, Gen: m.cancelGen})
	require.ErrorIs(t, m.err, boom)

	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{plan("PROJ", "PROJ-A")}})
	require.ErrorIs(t, m.err, boom, "a plans refresh must not clear an error from another stream")
}

// TestMissingProjectStatusOnlyChangesWhenTheSetChanges covers finding 5b: the
// "configured but not on" status must not overwrite an unrelated status every
// 30s refresh when the missing set has not changed.
func TestMissingProjectStatusOnlyChangesWhenTheSetChanges(t *testing.T) {
	m := testModel()
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Missing: []string{"GONE"}})
	require.Equal(t, "project GONE is configured but not on lab", m.status)

	m.status = "copied to clipboard (osc 52)" // something else happened since
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Missing: []string{"GONE"}})
	require.Equal(t, "copied to clipboard (osc 52)", m.status, "an unchanged missing set must not overwrite another status")

	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Missing: []string{"GONE", "OTHER"}})
	require.Equal(t, "project GONE, OTHER are configured but not on lab", m.status, "a changed missing set still says so")
}

func TestAPlansErrorMarksHomeStale(t *testing.T) {
	m := testModel()
	m, _ = send(m, errMsg{Err: errors.New("down"), Stream: streamPlans, Gen: m.plansGen})
	require.True(t, m.home.stale)
}

func tickModel() Model {
	m := homeModel()
	m.home.lastKey = homeNow
	return m
}

func TestATickOnHomeReloadsThePlansAndSchedulesTheNext(t *testing.T) {
	m := tickModel()
	gen := m.plansGen
	m, cmd := send(m, homeTickMsg{Gen: m.home.tickGen})
	require.Equal(t, gen+1, m.plansGen)
	require.True(t, m.plans.loading)
	require.NotNil(t, cmd) // load + next tick; never executed here, a tick blocks
}

func TestAStaleTickDoesNothing(t *testing.T) {
	m := tickModel()
	gen := m.plansGen
	m, cmd := send(m, homeTickMsg{Gen: m.home.tickGen - 1})
	require.Equal(t, gen, m.plansGen)
	require.Nil(t, cmd)
}

func TestATickOffHomeEndsTheLoop(t *testing.T) {
	m := tickModel()
	m.screen = screenPlan
	gen := m.plansGen
	m, cmd := send(m, homeTickMsg{Gen: m.home.tickGen})
	require.Equal(t, gen, m.plansGen)
	require.Nil(t, cmd)
}

func TestATickUnderAnOverlayWaitsWithoutLoading(t *testing.T) {
	m := tickModel()
	m.overlay = overlayHelp
	gen := m.plansGen
	m, cmd := send(m, homeTickMsg{Gen: m.home.tickGen})
	require.Equal(t, gen, m.plansGen)
	require.NotNil(t, cmd, "the loop continues so refreshing resumes once the overlay closes")
}

func TestATickDuringALoadDoesNotStartAnother(t *testing.T) {
	m := tickModel()
	m.plans.loading = true
	gen := m.plansGen
	m, cmd := send(m, homeTickMsg{Gen: m.home.tickGen})
	require.Equal(t, gen, m.plansGen)
	require.NotNil(t, cmd)
}

func TestRefreshStopsWhenIdleAndResumesOnAKey(t *testing.T) {
	m := tickModel()
	m.now = func() time.Time { return homeNow.Add(homeIdleAfter) }
	gen := m.plansGen
	m, cmd := send(m, homeTickMsg{Gen: m.home.tickGen})
	require.True(t, m.home.idle)
	require.Equal(t, gen, m.plansGen, "a parked UI makes no requests")
	require.Nil(t, cmd)

	tick := m.home.tickGen
	m, cmd = send(m, mkKey("j"))
	require.False(t, m.home.idle)
	require.Equal(t, tick+1, m.home.tickGen)
	require.NotNil(t, cmd, "the key restarts the loop")
}

func TestLeavingAndReturningHomeRestartsTheLoop(t *testing.T) {
	m := tickModel()
	tick := m.home.tickGen
	m, _ = send(m, mkKey("enter"))
	m, cmd := send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
	require.Greater(t, m.home.tickGen, tick)
	require.NotNil(t, cmd)
}

func TestInitStartsTheLoopOnHome(t *testing.T) {
	m := New(Deps{})
	require.NotNil(t, m.Init())
}

func TestTheHeaderSaysRefreshingAndStale(t *testing.T) {
	m := tickModel()
	m, _ = send(m, homeTickMsg{Gen: m.home.tickGen})
	require.Contains(t, m.homeHeader(), "refreshing…")

	m, _ = send(m, errMsg{Err: errors.New("down"), Stream: streamPlans, Gen: m.plansGen})
	m.now = func() time.Time { return homeNow.Add(3 * time.Minute) }
	require.Contains(t, m.homeHeader(), "stale 3m")
}

func TestAWatchedBuildShowsAsRunningOnItsRow(t *testing.T) {
	m := tickModel()
	b := provider.Build{Key: "PROJ-PROV12-9", PlanKey: "PROJ-PROV12", Branch: "develop", Number: 9, State: provider.StateRunning}
	m.noteLive(b)
	live := m.liveFor("PROJ-PROV")
	require.NotNil(t, live)
	require.Equal(t, 9, live.Number)
}

func TestAFinishedWatchUpdatesTheRowAtOnce(t *testing.T) {
	m := tickModel()
	b := provider.Build{Key: "PROJ-PROV-9", PlanKey: "PROJ-PROV", Number: 9, State: provider.StateRunning}
	m.noteLive(b)
	b.State, b.FinishedAt = provider.StateFailed, homeNow
	m.noteLive(b)
	require.Nil(t, m.liveFor("PROJ-PROV"))
	for _, p := range m.plans.items {
		if p.Key == "PROJ-PROV" {
			require.Equal(t, 9, p.LastBuild.Number)
			require.Equal(t, provider.StateFailed, p.LastBuild.State)
		}
	}
}

func TestAnOlderFinishedBuildDoesNotReplaceTheRow(t *testing.T) {
	m := tickModel()
	m.noteLive(provider.Build{Key: "PROJ-PROV-3", PlanKey: "PROJ-PROV", Number: 3, State: provider.StateSuccess})
	for _, p := range m.plans.items {
		if p.Key == "PROJ-PROV" {
			require.Equal(t, 8, p.LastBuild.Number, "opening an old build must not rewind the row")
		}
	}
}

func TestWatchEventsFeedTheMarker(t *testing.T) {
	m := tickModel()
	m.watchGen = 5
	e := app.Event{Type: app.EventState, Build: provider.Build{Key: "PROJ-BUILD-5", PlanKey: "PROJ-BUILD", Number: 5, State: provider.StateRunning}}
	m, _ = send(m, watchEventMsg{Gen: 5, Event: e})
	require.NotNil(t, m.liveFor("PROJ-BUILD"))
}

func TestSwitchingTheWatchDropsTheOldMarker(t *testing.T) {
	m := tickModel()
	m.svc = testService()
	m.noteLive(provider.Build{Key: "PROJ-PROV-9", PlanKey: "PROJ-PROV", Number: 9, State: provider.StateRunning})
	require.NotNil(t, m.liveFor("PROJ-PROV"))

	next, _ := m.startWatch("PROJ-BUILD-44") // do not execute the returned command
	nm := next.(Model)
	require.Nil(t, nm.liveFor("PROJ-PROV"), "watching a different build must drop the old marker")
}

func TestATriggeredBuildKeepsItsMarker(t *testing.T) {
	m := tickModel()
	m.svc = testService()
	m, _ = send(m, triggeredMsg{Gen: m.formGen, Build: provider.Build{
		Key: "PROJ-PROV12-9", PlanKey: "PROJ-PROV12", Branch: "develop", Number: 9, State: provider.StateQueued}})
	require.NotNil(t, m.liveFor("PROJ-PROV"), "a just-triggered build must survive startWatch's own stopWatch")
}
