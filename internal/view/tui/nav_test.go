package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/require"
)

// The way back is one stack of the screens esc returns to. These tests are
// the journeys of BAM-53 machine 7: each of the first three was a defect when
// nine separate fields held the way back (issue #237).

// wayBack lists the screens esc returns through, the bottom of the stack
// first.
func wayBack(m Model) []screen {
	out := []screen{}
	for _, f := range m.nav {
		out = append(out, f.screen)
	}
	return out
}

// drilled counts the plan screens a drill from a Tree row left on the way back.
func drilled(m Model) int {
	n := 0
	for _, f := range m.nav {
		if f.screen == screenPlan && f.plan != nil && !f.clean {
			n++
		}
	}
	return n
}

// escTo presses esc until the screen is want, at most five times, and says
// how many it took.
func escTo(t *testing.T, m Model, want screen) (Model, int) {
	t.Helper()
	for i := 1; i <= 5; i++ {
		m, _ = send(m, mkKey("esc"))
		if m.screen == want {
			return m, i
		}
	}
	return m, 0
}

// Defect 1: picking a project from a plan opened from the board kept the
// board's way back, so the next plan opened from Home returned to the board.
func TestPickingAProjectFromABoardPlanDoesNotSendEscToTheBoard(t *testing.T) {
	m := pinCursor(boardModel(100, 30))
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)

	next, _ := m.setProject("OPS") // what P and :project do
	m = next.(Model)
	require.Equal(t, screenHome, m.screen)
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{{Key: "OPS-NIGHTLY", Name: "Nightly", ProjectKey: "OPS"}}})
	m.focus = focusPlans
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, "OPS-NIGHTLY", m.plan.key)

	m, n := escTo(t, m, screenHome)
	require.NotZero(t, n, "esc reaches Home, not the board")
	require.Equal(t, screenHome, m.screen)
}

// Defect 2: a run started from a plan opened from the board returned to the
// board on esc; spec §4.1 says Home.
func TestEscAfterARunFromABoardPlanReturnsHome(t *testing.T) {
	m := pinCursor(boardModel(100, 30))
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)

	queued := sampleBuild()
	queued.Key, queued.Number, queued.State = "PROJ-BUILD-46", 46, provider.StateQueued
	m.screen = screenForm
	m.form.phase = formSending
	m, _ = send(m, triggeredMsg{Gen: m.runGen, Build: queued})
	require.Equal(t, screenPlan, m.screen)

	m, n := escTo(t, m, screenHome)
	require.NotZero(t, n, "esc reaches Home, not the board")
}

// Defect 3: a server switch from a tree opened over a board plan landed on
// Home, where the plan itself lands on the board.
func TestServerSwitchFromATreeOverABoardPlanLandsOnTheBoard(t *testing.T) {
	m := pinCursor(boardModel(100, 30))
	m.deps.Connect = func(context.Context, string) (*app.Service, error) { return nil, errors.New("unreachable") }
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)
	plan, _ := m.switchServer("other")
	require.Equal(t, screenBoard, plan.(Model).screen, "from the plan: the board")

	opened, _ := m.openTree("PROJ-BUILD-44")
	m = opened.(Model)
	require.Equal(t, screenTree, m.screen)
	next, _ := m.switchServer("other")
	require.Equal(t, screenBoard, next.(Model).screen, "from the tree: the board too")
}

func TestNavStackOperations(t *testing.T) {
	var s navStack
	_, ok := s.pop()
	require.False(t, ok, "an empty stack has nothing to return to")

	s.push(navFrame{screen: screenHome})
	shared := s
	s.push(navFrame{screen: screenPlan})
	shared.push(navFrame{screen: screenSet})
	require.Equal(t, screenPlan, s[1].screen, "models that share a stack never write to each other's")

	f, ok := s.pop()
	require.True(t, ok)
	require.Equal(t, screenPlan, f.screen)
	require.Len(t, s, 1)

	s.resetTo(navFrame{screen: screenBoard})
	require.True(t, s.rootedOnBoard())
	s.resetTo()
	require.Empty(t, s)
}

// Home keeps the way back to the projects table and nothing opened from it.
func TestNavToHomeKeepsOnlyTheProjectsTrail(t *testing.T) {
	s := navStack{{screen: screenHome, projects: true}, {screen: screenHome}, {screen: screenSet}}
	s.toHome()
	require.Equal(t, navStack{{screen: screenHome, projects: true}}, s)
	s.dropTrail()
	require.Empty(t, s)

	s = navStack{{screen: screenBoard}, {screen: screenPlan}}
	s.toHome()
	require.Empty(t, s)
}

// esc from the run form returns to the screen that opened it, and a log
// screen to the plan screen, whichever of Home and the plan screen it is.
func TestEscFromTheFormAndFromLogsReturnsToWhereTheyOpened(t *testing.T) {
	m := goldenHome(120, 30)
	m.svc = testService()
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{{Key: "PROJ-BUILD", Name: "Build", ProjectKey: "PROJ"}}})
	m.focus = focusPlans
	m, _ = send(m, mkKey("R"))
	require.Equal(t, screenForm, m.screen)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
	require.Empty(t, wayBack(m))

	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, []screen{screenHome}, wayBack(m))
	m, _ = send(m, mkKey("R"))
	require.Equal(t, screenForm, m.screen)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenPlan, m.screen, "the form was opened over the plan screen")
}

// A run keeps Home's way back to the projects table: esc leaves the plan for
// Home, and esc on Home leaves for the projects.
func TestEscAfterARunFromAProjectsTrailStillReachesTheProjects(t *testing.T) {
	m := fromOPS(t)
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{{Key: "OPS-NIGHTLY", Name: "Nightly", ProjectKey: "OPS"}}})
	m.screen = screenForm
	m.form.phase = formSending
	queued := sampleBuild()
	queued.Key, queued.PlanKey, queued.State = "OPS-NIGHTLY-3", "OPS-NIGHTLY", provider.StateQueued
	m, _ = send(m, triggeredMsg{Gen: m.runGen, Build: queued})
	require.Equal(t, screenPlan, m.screen)

	m, n := escTo(t, m, screenHome)
	require.NotZero(t, n)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, homeProjects, m.home.view)
}
