package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// Piece 6 of the TUI plan (BAM-20): :stats PLAN shows what bam plan stats
// shows, and H on a set lists its past runs as bam set history does.

// statsBuild is a finished build of PROJ-BUILD; failing names the jobs that
// failed in it.
func statsBuild(n int, st provider.State, d time.Duration, failing ...string) provider.Build {
	b := provider.Build{Key: "PROJ-BUILD-" + string(rune('0'+n)), PlanKey: "PROJ-BUILD", Number: n, State: st,
		StartedAt: goldenNow.Add(-time.Duration(10-n) * time.Hour), Duration: d}
	b.FinishedAt = b.StartedAt.Add(d)
	var jobs []provider.Job
	for i, name := range failing {
		jobs = append(jobs, provider.Job{Key: b.Key + "-J" + string(rune('0'+i)), Name: name, State: provider.StateFailed})
	}
	if len(jobs) > 0 {
		b.Stages = []provider.Stage{{Name: "Test", State: provider.StateFailed, Jobs: jobs}}
	}
	return b
}

// statsModel is Home on the plans table with PROJ-BUILD selected and three
// finished builds behind it: two passed, one failed on Integration tests.
func statsModel(t *testing.T) Model {
	m := goldenModel(100, 30)
	m.screen = screenHome
	m.svc = testService()
	m.svc.P.(*fake.Provider).History["PROJ-BUILD"] = []provider.Build{
		statsBuild(3, provider.StateSuccess, 4*time.Minute),
		statsBuild(2, provider.StateFailed, 3*time.Minute, "Integration tests"),
		statsBuild(1, provider.StateSuccess, 5*time.Minute),
	}
	m.plans.selectFirst(func(p provider.Plan) bool { return p.Key == "PROJ-BUILD" })
	return m
}

// runCmd runs a command and feeds its message back, as bubbletea would.
func deliver(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	require.NotNil(t, cmd)
	m, _ = send(m, cmd())
	return m
}

func TestStatsCommandShowsSuccessRateDurationsAndFailingJobs(t *testing.T) {
	m := statsModel(t)
	next, cmd := m.runCommandLine("stats")
	m = next.(Model)
	require.Equal(t, overlayInfo, m.overlay, "the overlay opens at once, before the builds are read")
	require.Contains(t, m.View(), "reading the last 20 builds")

	m = deliver(t, m, cmd)
	v := m.View()
	require.Contains(t, v, "Stats · PROJ-BUILD")
	require.Regexp(t, `Success\s+67%`, v)
	require.Contains(t, v, "median")
	require.Regexp(t, `Integration tests\s+1 of 1 failures\s+last #2`, v)

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, overlayNone, m.overlay)
	require.Equal(t, screenHome, m.screen)
}

func TestStatsCommandNamesAPlanOrPreset(t *testing.T) {
	m := statsModel(t)
	m.plans.selectFirst(func(p provider.Plan) bool { return p.Key == "OPS-NIGHTLY" }) // not the subject
	next, cmd := m.runCommandLine("stats PROJ-BUILD")
	m = deliver(t, next.(Model), cmd)
	require.Contains(t, m.View(), "Stats · PROJ-BUILD")

	// a preset resolves to its plan, as bam plan stats does
	next, cmd = statsModel(t).runCommandLine("stats build")
	m = deliver(t, next.(Model), cmd)
	require.Contains(t, m.View(), "Stats · build")
	require.Regexp(t, `Success\s+67%`, m.View())
}

func TestStatsCommandFromThePlanScreenReadsItsPlan(t *testing.T) {
	m := statsModel(t)
	m.screen = screenPlan
	m.buildsPlan = "PROJ-BUILD"
	next, cmd := m.runCommandLine("stats")
	m = deliver(t, next.(Model), cmd)
	require.Contains(t, m.View(), "Stats · PROJ-BUILD")
}

func TestStatsCommandWithNothingSelectedSaysSo(t *testing.T) {
	m := statsModel(t)
	m.plans.setItems(nil)
	next, cmd := m.runCommandLine("stats")
	m = next.(Model)
	require.Nil(t, cmd)
	require.Equal(t, overlayNone, m.overlay)
	require.ErrorContains(t, m.err, ":stats PLAN")
}

func TestStatsCommandFailureClosesTheOverlayAndShowsTheError(t *testing.T) {
	m := statsModel(t)
	next, cmd := m.runCommandLine("stats NOPE")
	m = deliver(t, next.(Model), cmd)
	require.Equal(t, overlayNone, m.overlay)
	require.ErrorContains(t, m.err, "is a project key")
}

func TestStatsAnswerForAnAbandonedOverlayIsDropped(t *testing.T) {
	m := statsModel(t)
	next, cmd := m.runCommandLine("stats")
	m = next.(Model)
	m, _ = send(m, mkKey("esc")) // the user left before the builds arrived
	m = deliver(t, m, cmd)
	require.Equal(t, overlayNone, m.overlay)
}

// historyModel is the sets table with release run twice in this repository
// (the last failed) and nightly never run.
func historyModel(t *testing.T) Model {
	m := setsModel(t)
	end := goldenNow.Add(-5 * time.Minute)
	for _, rec := range []app.SetRecord{
		{Version: 1, ID: "release-20260918T100000Z", Set: "release", Repo: "/repo", Status: app.SetSuccessful,
			StartedAt: goldenNow.Add(-2 * time.Hour), FinishedAt: &end,
			Stages: []app.StageRecord{{Runs: []app.RunRecord{{State: provider.StateSuccess}}}}},
		{Version: 1, ID: "release-20260918T115000Z", Set: "release", Repo: "/repo", Status: app.SetFailed,
			StartedAt: goldenNow.Add(-10 * time.Minute), FinishedAt: &end,
			Stages: []app.StageRecord{{Runs: []app.RunRecord{{State: provider.StateSuccess}, {State: provider.StateFailed}}}}},
		{Version: 1, ID: "release-other-repo", Set: "release", Repo: "/elsewhere", Status: app.SetSuccessful, StartedAt: goldenNow},
	} {
		require.NoError(t, m.svc.Sets.Save(rec))
	}
	return m
}

func TestHOnTheSetsTableListsThePastRunsNewestFirst(t *testing.T) {
	m := historyModel(t)
	m, cmd := send(m, mkKey("H"))
	require.Nil(t, cmd)
	require.Equal(t, overlayInfo, m.overlay)
	v := m.View()
	require.Contains(t, v, "History · release")
	require.Contains(t, v, "STATUS")
	require.Regexp(t, `(?s)release-20260918T115000Z.*failed.*release-20260918T100000Z.*successful`, v)
	require.Contains(t, v, "1/2")
	require.NotContains(t, v, "release-other-repo", "another repository's run is not this repository's history")

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, overlayNone, m.overlay)
	require.Equal(t, screenHome, m.screen)
}

func TestHOnASetThatNeverRanSaysSo(t *testing.T) {
	m := historyModel(t)
	m, _ = send(m, mkKey("j")) // nightly
	m, _ = send(m, mkKey("H"))
	require.Equal(t, overlayInfo, m.overlay)
	require.Contains(t, m.View(), "no runs of nightly recorded")
}

func TestHOnTheSetScreenListsThePastRuns(t *testing.T) {
	m := historyModel(t)
	next, _ := m.openSet()
	m, _ = send(next.(Model), setLoadedMsg{Gen: m.setGen + 1})
	require.Equal(t, screenSet, m.screen)
	m, _ = send(m, mkKey("H"))
	require.Equal(t, overlayInfo, m.overlay)
	require.Contains(t, m.View(), "release-20260918T115000Z")
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenSet, m.screen, "esc closes the list and stays on the set")
}

func TestSetHistoryIsCappedLikeBamSetHistory(t *testing.T) {
	m := historyModel(t)
	recs, err := m.svc.SetHistory("release", 1)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Equal(t, "release-20260918T115000Z", recs[0].ID)
}
