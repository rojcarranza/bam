package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// instantClock fires every After at once, so a train runs to its end.
type instantClock struct{}

func (instantClock) Now() time.Time { return goldenNow }
func (instantClock) After(time.Duration) <-chan time.Time {
	c := make(chan time.Time, 1)
	c <- goldenNow
	return c
}

// trainModel is the set screen on a one-stage set "release" whose run
// triggers PROJ-BUILD-50 and succeeds, with a real set store.
func trainModel(t *testing.T) Model {
	m := goldenModel(80, 24)
	svc := testService()
	svc.Clock = instantClock{}
	svc.Sets = &app.SetStore{Dir: filepath.Join(t.TempDir(), "sets"),
		Host: func() (string, error) { return "laptop", nil }, PID: func() int { return 4242 }}
	svc.Cfg.Machine.Sets = map[string]config.Set{"release": {Stages: []config.Stage{{{Target: "build"}}}}}
	svc.Origin = labOrigin
	f := svc.P.(*fake.Provider)
	f.TriggerResults = map[string]provider.Build{"PROJ-BUILD": {Key: "PROJ-BUILD-50", PlanKey: "PROJ-BUILD", State: provider.StateQueued}}
	f.Sequences = map[string][]provider.Build{"PROJ-BUILD-50": {{Key: "PROJ-BUILD-50", State: provider.StateRunning}, {Key: "PROJ-BUILD-50", State: provider.StateSuccess}}}
	m.svc = svc
	m.screen = screenHome
	m.home.view = homeSets
	m.focus = focusSets
	infos, err := app.DescribeSets(svc.Cfg, func(string) string { return "" })
	require.NoError(t, err)
	m.sets.setItems([]setItem{{Info: infos[0]}})
	next, _ := m.openSet()
	return next.(Model)
}

// pump runs cmd and feeds every message it yields back into m until the
// train's channel closes.
func pump(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for i := 0; cmd != nil && i < 200; i++ {
		msg := cmd()
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				if c != nil {
					if mm, ok := c().(setEventMsg); ok {
						m, cmd = send(m, mm)
					}
				}
			}
			continue
		}
		switch msg.(type) {
		case setEventMsg, setClosedMsg:
			m, cmd = send(m, msg)
		default:
			return m
		}
	}
	return m
}

func TestSetRunFromTheScreen(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("R"))
	require.Equal(t, screenSetVars, m.screen)
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.Equal(t, screenSet, m.screen)
	require.True(t, m.train.running())
	m = pump(t, m, cmd)
	require.False(t, m.train.running(), "the train ended")
	require.Contains(t, m.status, "release succeeded")
	require.Equal(t, provider.StateSuccess, m.setRows()[0].Build.State)
}

func TestSetVarsAddEditRemove(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("R"))
	m, _ = send(m, mkKey("a"))
	require.Len(t, m.setVars.rows, 1)
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("env")})
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("qa")})
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, setVarRow{Name: "env", Value: "qa"}, m.setVars.rows[0])
	require.Equal(t, []string{"env=qa"}, m.setVars.flags())
	m, _ = send(m, mkKey("x"))
	require.Empty(t, m.setVars.rows)
}

func TestSetVarsRequiredRowsBlockRun(t *testing.T) {
	m := trainModel(t)
	m.setView.rec = &app.SetRecord{ID: "release-1", Set: "release", Status: app.SetFailed, VarNames: []string{"env"}}
	m, _ = send(m, mkKey("F"))
	require.Equal(t, screenSetVars, m.screen)
	require.True(t, m.setVars.rows[0].Required)
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.Nil(t, cmd)
	require.Equal(t, screenSetVars, m.screen)
	require.Contains(t, errorWhat(m.setVars.err), "env needs a value")
}

func TestSetVarsDryRunShowsEveryRun(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("R"))
	m, _ = send(m, mkKey("d"))
	require.Equal(t, overlayDryRun, m.overlay)
	require.Contains(t, m.View(), "PROJ-BUILD")
}

func TestSetTrainSurvivesLeavingTheScreen(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("R"))
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.True(t, m.train.running())
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
	require.True(t, m.train.running(), "leaving the screen does not stop the train")
	require.Contains(t, m.View(), "running")
}

func TestOnlyOneTrainPerWindow(t *testing.T) {
	m := trainModel(t)
	m.train = trainState{set: "other", ch: make(chan app.SetEvent), cancel: func() {}, board: app.NewSetBoard(app.SetPlan{Name: "other"})}
	m, _ = send(m, mkKey("R"))
	require.Equal(t, screenSet, m.screen)
	require.Contains(t, m.status, "other is running in this window")
	_ = context.Background
}

// R on the sets table opens the run prompt for the selected set at once.
func TestRunFromTheSetsTable(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
	m, _ = send(m, mkKey("R"))
	require.Equal(t, screenSetVars, m.screen)
	require.Equal(t, "release", m.setView.info.Name)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenSet, m.screen, "esc from the prompt lands on the set screen")
}

func TestQuitWhileATrainRunsAsks(t *testing.T) {
	m := trainModel(t)
	block := make(chan app.SetEvent) // a train that never ends on its own
	stopped := false
	m.train = trainState{set: "release", ch: block, board: app.NewSetBoard(app.SetPlan{Name: "release"}),
		cancel: func() { stopped = true }, gen: 1}
	m, cmd := send(m, mkKey("q"))
	require.Equal(t, overlayConfirm, m.overlay)
	require.Contains(t, m.confirm.Prompt, "release is running in this window")
	require.Nil(t, cmd, "nothing quits before the answer")
	m, cmd = send(m, mkKey("y"))
	require.True(t, stopped, "yes ends the train's watching")
	require.NotNil(t, cmd)
	require.Empty(t, m.svc.P.(*fake.Provider).Stopped, "no build is stopped")
}

func TestSwitchServerWhileATrainRunsAsks(t *testing.T) {
	m := trainModel(t)
	m.train = trainState{set: "release", ch: make(chan app.SetEvent), board: app.NewSetBoard(app.SetPlan{}), cancel: func() {}, gen: 1}
	m, _ = send(m, mkKey("esc"))
	next, _ := m.switchServer("other")
	require.Equal(t, overlayConfirm, next.(Model).overlay)
}

// Quitting waits for the train to finish its own ending (the record written
// interrupted, the lock released) before the UI exits: it drains the train's
// events until the channel closes.
func TestQuitWaitsForTheTrainToEnd(t *testing.T) {
	m := trainModel(t)
	ch := make(chan app.SetEvent, 2)
	ch <- app.SetEvent{Type: app.SetRunEvent}
	ch <- app.SetEvent{Type: app.SetRunEvent}
	m.train = trainState{set: "release", ch: ch, board: app.NewSetBoard(app.SetPlan{}), gen: 1,
		cancel: func() { close(ch) }}
	m, _ = send(m, mkKey("q"))
	_, cmd := send(m, mkKey("y"))
	require.NotNil(t, cmd)
	msg := cmd()
	require.IsType(t, tea.QuitMsg{}, msg)
	require.Empty(t, ch, "every pending event was drained before quitting")
}
