package tui

import (
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func setInfos() []app.SetInfo {
	return []app.SetInfo{
		{Name: "release", DefinedIn: []string{".bam.yaml"}, Stages: [][]app.SetRunInfo{
			{{Target: "smoke", Runs: []string{"smoke"}}},
			{{Target: "build", Runs: []string{"build[env=qa]", "build[env=prod]"}}},
		}},
		{Name: "nightly", DefinedIn: []string{".bam.yaml"}, Stages: [][]app.SetRunInfo{{{Target: "build", Runs: []string{"build"}}}}},
	}
}

// setsModel is Home on the sets view with release failed 12 minutes ago and
// nightly never run.
func setsModel(t *testing.T) Model {
	m := goldenModel(80, 24)
	m.screen = screenHome
	m.svc = testService()
	m.svc.Sets = &app.SetStore{Dir: filepath.Join(t.TempDir(), "sets"),
		Host: func() (string, error) { return "laptop", nil }, PID: func() int { return 4242 }}
	last := app.SetRecord{ID: "release-20261006T084800Z", Set: "release", Status: app.SetFailed,
		StartedAt: goldenNow.Add(-12 * time.Minute)}
	m.home.view = homeSets
	m.focus = focusSets
	m.sets.setItems([]setItem{{Info: setInfos()[0], Last: &last}, {Info: setInfos()[1]}})
	return m
}

func TestSetsGolden80x24(t *testing.T) {
	requireGolden(t, "sets-80x24", setsModel(t).View())
}

func TestSetsTableCountsExpandedRunsAndLastRun(t *testing.T) {
	v := setsModel(t).View()
	require.Regexp(t, `release\s+2\s+3\s+✗ failed\s+12m`, v)
	require.Regexp(t, `nightly\s+1\s+1\s+–`, v)
}

func TestSetsCommandShowsTheSetsView(t *testing.T) {
	instantHomeTick(t)
	m := goldenModel(80, 24)
	m.screen = screenHome
	m.deps.Sets = func() ([]app.SetInfo, error) { return setInfos(), nil }
	m.svc = testService()
	m.svc.Sets = &app.SetStore{Dir: t.TempDir()}
	next, cmd := m.runCommandLine("sets")
	m = next.(Model)
	require.Equal(t, homeSets, m.home.view)
	require.Equal(t, focusSets, m.focus)
	require.NotNil(t, cmd)
	for _, c := range cmd().(tea.BatchMsg) {
		if c == nil {
			continue
		}
		if msg, ok := c().(setsLoadedMsg); ok {
			m, _ = send(m, msg)
		}
	}
	require.Equal(t, 2, m.sets.len())
}

func TestSetsFilterAndMove(t *testing.T) {
	m := setsModel(t)
	m, _ = send(m, mkKey("j"))
	s, _ := m.sets.selected()
	require.Equal(t, "nightly", s.Info.Name)
	m.applyFilter("rel")
	require.Equal(t, 1, m.sets.len())
}

func TestSetStatusCell(t *testing.T) {
	require.Equal(t, "–", setStatusCell(nil))
	for st, want := range map[app.SetStatus]string{
		app.SetRunning: "running", app.SetSuccessful: "successful", app.SetFailed: "failed",
		app.SetInterrupted: "interrupted", app.SetTimedOut: "timed out",
	} {
		require.Contains(t, setStatusCell(&app.SetRecord{Status: st}), want)
	}
	_ = provider.StateSuccess
}
