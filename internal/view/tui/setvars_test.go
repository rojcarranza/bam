package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// pump runs cmd and every command it leads to, feeding the train's
// messages (events, close, the record reload, the start) back into m. It
// skips anything else, such as Home's tick, so it never sleeps.
func pump(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for i := 0; len(queue) > 0 && i < 500; i++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if b, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, b...)
			continue
		}
		switch msg.(type) {
		case setEventMsg, setClosedMsg, setLoadedMsg, setsLoadedMsg, trainStartedMsg, setDryMsg:
			var next tea.Cmd
			m, next = send(m, msg)
			queue = append(queue, next)
		}
	}
	return m
}

func TestSetRunFromTheScreen(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("R"))
	require.Equal(t, screenSetVars, m.screen)
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	m, cmd = send(m, cmd()) // the start, run as a command
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
	m, cmd := send(m, mkKey("d"))
	m, _ = send(m, cmd()) // the plan, run as a command
	require.Equal(t, overlayDryRun, m.overlay)
	require.Contains(t, m.View(), "PROJ-BUILD")
}

func TestSetTrainSurvivesLeavingTheScreen(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("R"))
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	m, _ = send(m, cmd()) // the start, run as a command
	require.True(t, m.train.running())
	stopTrain(t, m.train)
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

// The quit prompt is longer than the modal is wide; it wraps instead of
// losing the part that says the builds keep running.
func TestALongConfirmPromptWraps(t *testing.T) {
	m := trainModel(t)
	m.train = trainState{set: "release", ch: make(chan app.SetEvent), board: app.NewSetBoard(app.SetPlan{}), cancel: func() {}, gen: 1}
	m, _ = send(m, mkKey("q"))
	body := m.confirmBody(40)
	for _, l := range strings.Split(body, "\n") {
		require.LessOrEqual(t, lipgloss.Width(l), 40, l)
	}
	require.Contains(t, strings.Join(strings.Fields(body), " "), strings.Join(strings.Fields(m.confirm.Prompt), " "))
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

// When a train ends, the set screen re-reads its record, so the header and
// the keys offered match the run that just finished.
func TestTheSetScreenRereadsTheRecordWhenATrainEnds(t *testing.T) {
	m := trainModel(t)
	f := m.svc.P.(*fake.Provider)
	f.Sequences["PROJ-BUILD-50"] = []provider.Build{{Key: "PROJ-BUILD-50", State: provider.StateFailed}}
	m, _ = send(m, mkKey("R"))
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	m = pump(t, m, cmd)
	require.False(t, m.train.running())
	require.NotNil(t, m.setView.rec, "the record of the run that just ended")
	require.Equal(t, app.SetFailed, m.setView.rec.Status)
	require.Contains(t, m.keyLine(), "F rerun failed")
}

// Starting a train and planning a dry run talk to Bamboo, so they run in a
// command, never inside Update: the UI stays responsive while they work.
func TestStartingATrainDoesNotBlockUpdate(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("R"))
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.False(t, m.train.running(), "nothing has started inside Update")
	require.Empty(t, m.svc.P.(*fake.Provider).Triggered, "no trigger inside Update")
	require.Contains(t, m.View(), "starting")
	require.NotNil(t, cmd)
	m, _ = send(m, cmd())
	require.True(t, m.train.running())
	stopTrain(t, m.train)
	require.Equal(t, screenSet, m.screen)
}

func TestTheSetDryRunDoesNotBlockUpdate(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("R"))
	m, cmd := send(m, mkKey("d"))
	require.Equal(t, overlayNone, m.overlay, "the dry run is still being planned")
	require.NotNil(t, cmd)
	m, _ = send(m, cmd())
	require.Equal(t, overlayDryRun, m.overlay)
}

// A secret-looking variable never shows its value: masked in the row, and
// typed as a password while being edited, as the run form does.
func TestSetVarsMaskSecrets(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("R"))
	m.setVars.rows = []setVarRow{{Name: "db_password", Value: "hunter2"}}
	require.NotContains(t, m.View(), "hunter2")
	require.Contains(t, m.View(), "********")
	m, _ = send(m, mkKey("enter"))
	require.True(t, m.setVars.editing)
	require.Equal(t, textinput.EchoPassword, m.setVars.input.EchoMode)
	require.NotContains(t, m.View(), "hunter2")
}

// A train ended by the user (switching server) must not speak afterwards: a
// late event of it is ignored, so no stale "failed" lands under the new
// server and no read is left waiting on a channel that is gone.
func TestAnEndedTrainsLateEventIsIgnored(t *testing.T) {
	m := trainModel(t)
	m.train = trainState{set: "release", ch: make(chan app.SetEvent), board: app.NewSetBoard(app.SetPlan{Name: "release"}),
		cancel: func() {}, gen: 7}
	m.status = ""
	m.train.end()
	m, cmd := send(m, setEventMsg{Gen: 7, Event: app.SetEvent{Type: app.SetDone, State: provider.StateFailed}})
	require.Nil(t, cmd, "no further read is issued")
	require.Empty(t, m.status, "the ended train's result is not shown")
}

// stopTrain ends a train a test started and waits for it to finish writing
// its record, so the test's temp directory can be removed.
func stopTrain(t *testing.T, tr trainState) {
	t.Helper()
	cancel, ch := tr.cancel, tr.ch
	t.Cleanup(func() {
		if cancel != nil {
			cancel()
		}
		for range ch {
		}
	})
}

// Leaving the prompt while a start is in flight must not drop the train: it
// has the lock and may have triggered builds, so it is installed and watched.
func TestAStartedTrainIsKeptWhenThePromptWasLeft(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("R"))
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	m, _ = send(m, mkKey("esc")) // leave the prompt before the start lands
	m, _ = send(m, cmd())
	require.True(t, m.train.running(), "the started train is still this window's")
	stopTrain(t, m.train)
}

// Switching server after yes waits for the train to finish its own ending,
// as quitting does, before the switch happens.
func TestSwitchServerWaitsForTheTrainToEnd(t *testing.T) {
	m := trainModel(t)
	ch := make(chan app.SetEvent, 1)
	ch <- app.SetEvent{Type: app.SetRunEvent}
	m.train = trainState{set: "release", ch: ch, board: app.NewSetBoard(app.SetPlan{}), gen: 1, cancel: func() { close(ch) }}
	next, _ := m.switchServer("other")
	m = next.(Model)
	m, cmd := send(m, mkKey("y"))
	require.Equal(t, "lab", m.server, "not switched before the train ended")
	msg := cmd()
	require.Empty(t, ch, "the train was drained")
	m, _ = send(m, msg)
	require.Equal(t, "other", m.server)
}

func TestTheDryRunMarksThePromptBusy(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("R"))
	m, cmd := send(m, mkKey("d"))
	require.NotNil(t, cmd)
	require.True(t, m.setVars.busy)
	_, again := send(m, mkKey("d"))
	require.Nil(t, again, "no second plan while one is in flight")
	_, start := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.Nil(t, start, "no start while a plan is in flight")
	m, _ = send(m, cmd())
	require.False(t, m.setVars.busy)
}
