package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func testModel() Model {
	m := New(Deps{Servers: []Server{{Alias: "lab", URL: "https://bamboo.lab.example"}}, Initial: "lab"})
	m.width, m.height = 80, 24
	m.screen = screenPlan // the v0.2 tests start on the plan screen; Home has its own tests
	m.focus = focusBuilds
	return m
}

func send(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

// instantHomeTick makes Home's auto-refresh tick fire at once, so a test can
// run every command of a batch without knowing where the tick sits in it.
// tea.Tick reads the duration when the command is built, so call it before
// the model makes any. No test in this package runs in parallel, so the
// swap is safe.
func instantHomeTick(t *testing.T) {
	t.Helper()
	old := homeRefreshEvery
	homeRefreshEvery = 0
	t.Cleanup(func() { homeRefreshEvery = old })
}

// runBatch runs cmd and, when it is a batch, every command inside it, and
// returns the messages in batch order. Callers use instantHomeTick first.
func runBatch(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, sub := range batch {
		out = append(out, runBatch(sub)...)
	}
	return out
}

// TestTabCyclesPanels pins the plan screen's tab order (plan screen spec §4.2).
func TestTabCyclesPanels(t *testing.T) {
	m := testModel()
	require.Equal(t, focusBuilds, m.focus)
	for _, want := range []focus{focusStages, focusTree, focusBuilds} {
		m, _ = send(m, mkKey("tab"))
		require.Equal(t, want, m.focus)
	}
	for _, want := range []focus{focusTree, focusStages, focusBuilds} {
		m, _ = send(m, mkKey("shift+tab"))
		require.Equal(t, want, m.focus)
	}
}

// TestNumberKeysFocusPanelsDirectly pins 1, 2 and 3 on the plan screen.
func TestNumberKeysFocusPanelsDirectly(t *testing.T) {
	m := testModel()
	for _, tc := range []struct {
		key  string
		want focus
	}{{"2", focusStages}, {"3", focusTree}, {"1", focusBuilds}} {
		m, _ = send(m, mkKey(tc.key))
		require.Equal(t, tc.want, m.focus, "key %q", tc.key)
	}
}

// TestBackResolvesInSpecOrder pins plan screen spec §4.1: overlay, log
// screen, Stages or Tree to Builds, then Home.
func TestBackResolvesInSpecOrder(t *testing.T) {
	m := testModel()
	m.overlay, m.screen, m.focus = overlayHelp, screenLogs, focusStages

	m, cmd := send(m, mkKey("esc"))
	require.Equal(t, overlayNone, m.overlay)
	require.Equal(t, screenLogs, m.screen, "the overlay closes before the log screen does")
	require.Nil(t, cmd)

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, focusStages, m.focus, "the log screen closes before focus moves")

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, focusBuilds, m.focus, "Stages backs out to Builds")

	m.focus = focusTree
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, focusBuilds, m.focus, "Tree backs out to Builds")

	m.builds.setQuery("fail")
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenPlan, m.screen, "esc clears the Builds filter before leaving")
	require.Empty(t, m.builds.query)

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen, "esc on Builds returns to Home")
}

// TestQuitKeys pins q and ctrl-c from the columns screen.
func TestQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		_, cmd := send(testModel(), mkKey(k))
		require.NotNil(t, cmd, "key %q", k)
		require.IsType(t, tea.QuitMsg{}, cmd(), "key %q", k)
	}
}

// TestWindowSizeIsRemembered so the layout can size its panels.
func TestWindowSizeIsRemembered(t *testing.T) {
	m, _ := send(testModel(), tea.WindowSizeMsg{Width: 120, Height: 40})
	require.Equal(t, 120, m.width)
	require.Equal(t, 40, m.height)
}

// TestMovementAppliesToTheFocusedPanel: j and k must not move two cursors.
func TestMovementAppliesToTheFocusedPanel(t *testing.T) {
	m := testModel()
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "A", Builds: []provider.Build{{Key: "A-1"}, {Key: "A-2"}}})
	m.detail, m.expanded = ptr(sampleBuild()), defaultExpanded(sampleBuild())

	m, _ = send(m, mkKey("j"))
	require.Equal(t, 1, m.builds.cursor)
	require.Equal(t, 0, m.treeCursor)

	m.focus = focusStages
	m, _ = send(m, mkKey("j"))
	require.Equal(t, 1, m.builds.cursor, "the builds cursor must not move")
	require.Equal(t, 1, m.treeCursor)
}

func TestGAndShiftGJumpToTheEnds(t *testing.T) {
	m := testModel()
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "A", Builds: []provider.Build{{Key: "A-1"}, {Key: "A-2"}, {Key: "A-3"}}})
	m, _ = send(m, mkKey("G"))
	require.Equal(t, 2, m.builds.cursor)
	m, _ = send(m, mkKey("g"))
	require.Equal(t, 0, m.builds.cursor)
}

// TestEnterOnBuildsOpensDetailAndFocusesMain is spec §4.1's second row.
func TestEnterOnBuildsOpensDetailAndFocusesMain(t *testing.T) {
	m := testModel()
	m.svc = testService()
	m.plan.key = "PROJ-BUILD"
	m, cmd := send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: []provider.Build{sampleBuild()}})
	require.NotNil(t, m.detail, "the newest build opens as soon as the list loads")
	require.NotNil(t, cmd, "opening a build reads its stages")
	m.focus = focusBuilds

	m, _ = send(m, mkKey("enter"))
	require.Equal(t, focusStages, m.focus)
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)
	require.True(t, m.expanded["Test"], "the failed stage opens")
}

func TestEnterOnAStageTogglesIt(t *testing.T) {
	m := testModel()
	m.detail, m.expanded, m.focus = ptr(sampleBuild()), map[string]bool{}, focusStages
	require.Len(t, m.treeRows(), 3)

	m, _ = send(m, mkKey("enter")) // the cursor is on Build
	require.True(t, m.expanded["Build"])
	require.Len(t, m.treeRows(), 5)

	m, _ = send(m, mkKey("enter"))
	require.False(t, m.expanded["Build"])
	require.Len(t, m.treeRows(), 3)
}

// TestTreeCursorClampsWhenAStageCollapses: collapsing above the cursor must
// not leave it pointing past the end.
func TestTreeCursorClampsWhenAStageCollapses(t *testing.T) {
	m := testModel()
	m.detail, m.expanded, m.focus = ptr(sampleBuild()), map[string]bool{"Build": true}, focusStages
	m.treeCursor = 4 // the last row while Build is open
	m, _ = send(m, mkKey("enter"))
	require.Less(t, m.treeCursor, len(m.treeRows()))
}

// TestNoLowercaseKeyMutatesAnything is part B's rule in place of part A's
// "nothing mutates": R only opens a form, and the keys that reach the server
// are uppercase or chorded, so neither is one keystroke away from a
// forty-minute pipeline.
func TestNoLowercaseKeyMutatesAnything(t *testing.T) {
	mutating := []key.Binding{keys.Run, keys.Trigger, keys.Cancel}
	for _, b := range mutating {
		for _, k := range b.Keys() {
			require.True(t, strings.HasPrefix(k, "ctrl+") || strings.ToLower(k) != k,
				"key %q reaches a mutation and is lowercase", k)
		}
	}
	// R opens a form and sends nothing, so it is the one of the three that
	// needs no confirmation. ctrl-R cannot be struck while a field is being
	// edited, and C always asks.
	require.Equal(t, []string{"R"}, keys.Run.Keys())
	require.Equal(t, []string{"ctrl+r"}, keys.Trigger.Keys())
	require.Equal(t, []string{"C"}, keys.Cancel.Keys())
}

// TestRefreshReloadsTheFocusedPanelOnly, spec §5.
func TestRefreshReloadsTheFocusedPanelOnly(t *testing.T) {
	m := testModel()
	m.svc = testService()
	m.buildsPlan = "PROJ-BUILD"

	m.focus = focusPlans
	_, cmd := send(m, mkKey("r"))
	require.NotNil(t, cmd)
	require.IsType(t, plansLoadedMsg{}, cmd())

	m.focus = focusBuilds
	_, cmd = send(m, mkKey("r"))
	require.NotNil(t, cmd)
	require.Equal(t, "PROJ-BUILD", cmd().(buildsLoadedMsg).PlanKey)
}

func TestRefreshOnMainRefetchesTheBuild(t *testing.T) {
	m := testModel()
	m.svc = testService()
	m.focus = focusStages
	m.detail = ptr(provider.Build{Key: "PROJ-BUILD-44", PlanKey: "PROJ-BUILD"})

	_, cmd := send(m, mkKey("r"))
	require.NotNil(t, cmd)
	require.IsType(t, buildLoadedMsg{}, cmd())
}

// TestRefreshClearsTheError so r is the documented retry after EventError.
func TestRefreshClearsTheError(t *testing.T) {
	m := testModel()
	m.svc = testService()
	m.err = errBoom
	m, _ = send(m, mkKey("r"))
	require.NoError(t, m.err)
}

func TestRefreshWithoutAConnectionDoesNothing(t *testing.T) {
	m := testModel()
	m.svc = nil
	_, cmd := send(m, mkKey("r"))
	require.Nil(t, cmd)
}

func TestBuildLoadedMsgUpdatesTheDetailWithoutLosingTheTreeCursor(t *testing.T) {
	m := testModel()
	m.detail, m.expanded, m.treeCursor = ptr(sampleBuild()), map[string]bool{"Test": true}, 2
	m, _ = send(m, buildLoadedMsg{Build: sampleBuild()})
	require.Equal(t, 2, m.treeCursor)
	require.True(t, m.expanded["Test"], "refreshing must not collapse what the user opened")
}

// TestInitAfterHandshakeFillsPlans pins the generation Init hands to its
// plans request. Run connects before the program loop, so Init takes the
// already-connected branch; Init's receiver is a copy, so a bump made there
// is lost and every plansLoadedMsg it asked for would be dropped as stale.
func TestInitAfterHandshakeFillsPlans(t *testing.T) {
	instantHomeTick(t)
	d := Deps{
		Connect: func(context.Context, string) (*app.Service, error) { return testService(), nil },
		Servers: []Server{{Alias: "lab", URL: "https://bamboo.lab.example"}},
		Initial: "lab",
	}
	m := New(d).withContext(t.Context())
	msg, ok := connectCmd(t.Context(), d, d.Initial, 0)().(connectedMsg)
	require.True(t, ok)
	m = m.connected(msg)

	// Run what Init asks for and feed every message back, the way the
	// bubbletea loop does. On Home, Init's batch also starts the auto-refresh
	// tick (home spec §4.2); its beat is dropped so the loop stops here.
	var feed func(Model, []tea.Msg) Model
	feed = func(m Model, msgs []tea.Msg) Model {
		for _, msg := range msgs {
			if _, tick := msg.(homeTickMsg); tick || msg == nil {
				continue
			}
			var next tea.Cmd
			m, next = send(m, msg)
			m = feed(m, runBatch(next))
		}
		return m
	}

	init := m.Init()
	require.NotNil(t, init)
	msgs := runBatch(init)
	require.Len(t, msgs, 2, "the plans load and the Home auto-refresh tick")
	require.True(t, slices.ContainsFunc(msgs, func(msg tea.Msg) bool {
		_, ok := msg.(homeTickMsg)
		return ok
	}), "Init starts the Home auto-refresh tick")
	m = feed(m, msgs)
	require.NoError(t, m.err)
	require.NotZero(t, m.plans.len(), "the Plans panel is empty after start-up")
	require.False(t, m.plans.loading)
}
