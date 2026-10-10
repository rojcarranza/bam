package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func TestSOpensTheServerPickerWithEveryServer(t *testing.T) {
	m := New(Deps{Servers: []Server{
		{Alias: "lab", URL: "https://bamboo.lab.example"},
		{Alias: "work", URL: "https://bamboo.example.com"},
	}, Initial: "lab"})
	m.width, m.height = 80, 24

	m, _ = send(m, mkKey("S"))
	require.Equal(t, overlayServers, m.overlay)
	require.Equal(t, 2, m.picker.len())
	require.Contains(t, m.View(), "bamboo.lab.example")
}

func TestChoosingAServerReconnectsAndReloads(t *testing.T) {
	instantHomeTick(t)
	var asked []string
	d := Deps{
		Servers: []Server{{Alias: "lab", URL: labOrigin}, {Alias: "work", URL: "https://bamboo.example.com"}},
		Initial: "lab",
		Connect: func(_ context.Context, alias string) (*app.Service, error) {
			asked = append(asked, alias)
			return testService(), nil
		},
	}
	m := New(d)
	m.width, m.height = 80, 24

	m, _ = send(m, mkKey("S"))
	m, _ = send(m, mkKey("j"))
	m, cmd := send(m, mkKey("enter"))
	require.Equal(t, overlayNone, m.overlay, "choosing closes the overlay")
	require.NotNil(t, cmd)
	// switchServer also restarts Home's auto-refresh tick (home spec §4.2).
	msgs := runBatch(cmd)
	require.Len(t, msgs, 2, "the Home tick restart and the reconnect")
	require.ElementsMatch(t, []string{"tui.homeTickMsg", "tui.connectedMsg"},
		[]string{fmt.Sprintf("%T", msgs[0]), fmt.Sprintf("%T", msgs[1])})
	require.Equal(t, []string{"work"}, asked)
}

// TestSwitchingServerCancelsTheWatch: the old server's build is not this
// server's build.
func TestSwitchingServerCancelsTheWatch(t *testing.T) {
	m := New(Deps{Servers: []Server{{Alias: "lab"}, {Alias: "work"}}, Initial: "lab",
		Connect: func(context.Context, string) (*app.Service, error) { return testService(), nil }})
	m.width, m.height = 80, 24
	cancelled := false
	m.watchCancel = func() { cancelled = true }
	m.detail = ptr(sampleBuild())

	m, _ = send(m, mkKey("S"))
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter"))
	require.True(t, cancelled)
	require.Nil(t, m.detail, "the old server's build detail is cleared")
}

func TestEscClosesThePickerWithoutSwitching(t *testing.T) {
	m := New(Deps{Servers: []Server{{Alias: "lab"}, {Alias: "work"}}, Initial: "lab"})
	m.width, m.height = 80, 24
	m, _ = send(m, mkKey("S"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, overlayNone, m.overlay)
	require.Equal(t, "lab", m.server)
}

func TestOverlayStaysInsideTheTerminal(t *testing.T) {
	m := goldenModel(80, 24)
	m.overlay = overlayServers
	m.picker.setItems([]pickerItem{{Label: "lab", Detail: labOrigin}})
	for _, line := range strings.Split(m.View(), "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), 80)
	}
}

// TestPickerKeysDoNotLeakToThePanels: while an overlay is open, j moves the
// picker and nothing else.
func TestPickerKeysDoNotLeakToThePanels(t *testing.T) {
	m := goldenModel(80, 24)
	m.deps.Servers = append(m.deps.Servers, Server{Alias: "work", URL: "https://bamboo.example.com"})
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{{Key: "A"}, {Key: "B"}}})
	m, _ = send(m, mkKey("S"))
	before := m.plans.cursor
	m, _ = send(m, mkKey("j"))
	require.Equal(t, before, m.plans.cursor)
	require.Equal(t, 1, m.picker.cursor)
}

func TestPOpensTheProjectPickerWithAnAllRow(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m, cmd := send(m, mkKey("P"))
	require.Equal(t, overlayProjects, m.overlay)
	require.NotNil(t, cmd, "the picker loads the project list")

	m, _ = send(m, cmd().(projectsLoadedMsg))
	rows := m.picker.rows()
	require.Equal(t, "all projects", rows[0].Label)
	require.Equal(t, "", rows[0].Value)
	require.Equal(t, "PROJ", rows[1].Value)
}

func TestChoosingAProjectReloadsThePlans(t *testing.T) {
	instantHomeTick(t) // setProject restarts Home's tick, which batches with the load
	m := goldenModel(80, 24)
	m.svc = testService()
	m, _ = send(m, mkKey("P"))
	m, _ = send(m, projectsLoadedMsg{Gen: m.pickerGen, Stream: streamPicker, Projects: []provider.Project{{Key: "PROJ"}, {Key: "OPS"}}})
	m, _ = send(m, mkKey("j")) // onto PROJ
	m, cmd := send(m, mkKey("enter"))
	require.Equal(t, "PROJ", m.project)
	require.Equal(t, overlayNone, m.overlay)
	require.NotNil(t, cmd)
	var loaded bool
	for _, msg := range runBatch(cmd) {
		_, ok := msg.(plansLoadedMsg)
		loaded = loaded || ok
	}
	require.True(t, loaded, "the plans reload")
}

func TestChoosingAllProjectsClearsTheFilter(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.project = "PROJ"
	m, _ = send(m, mkKey("P"))
	m, _ = send(m, projectsLoadedMsg{Gen: m.pickerGen, Stream: streamPicker, Projects: []provider.Project{{Key: "PROJ"}}})
	m.picker.top()
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, "", m.project)
}

func TestBOpensTheBranchPickerForThePlanScreensPlan(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.plan.key = "PROJ-PROV"
	m, cmd := send(m, mkKey("b"))
	require.Equal(t, overlayBranches, m.overlay)
	require.NotNil(t, cmd)

	m, _ = send(m, branchesLoadedMsg{Gen: m.pickerGen, MasterKey: "PROJ-PROV", Branches: []provider.Branch{
		{Key: "PROJ-PROV12", ShortName: "develop", PlanKey: "PROJ-PROV"},
	}})
	rows := m.picker.rows()
	require.Equal(t, "default branch", rows[0].Label)
	require.Equal(t, "PROJ-PROV", rows[0].Value)
	require.Equal(t, "develop", rows[1].Label)
	require.Equal(t, "PROJ-PROV12", rows[1].Value)
}

func TestChoosingABranchLoadsThatBranchPlansBuilds(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.plan.key = "PROJ-PROV"
	m, _ = send(m, mkKey("b"))
	m, _ = send(m, branchesLoadedMsg{Gen: m.pickerGen, MasterKey: "PROJ-PROV", Branches: []provider.Branch{
		{Key: "PROJ-PROV12", ShortName: "develop", PlanKey: "PROJ-PROV"},
	}})
	m, _ = send(m, mkKey("j"))
	m, cmd := send(m, mkKey("enter"))
	require.Equal(t, overlayNone, m.overlay)
	require.Equal(t, "PROJ-PROV12", m.buildsPlan)
	require.NotNil(t, cmd)
	require.Equal(t, "PROJ-PROV12", cmd().(buildsLoadedMsg).PlanKey)
}

// TestBranchSwitchCancelsTheWatch: the watched build belongs to the old
// branch plan.
func TestBranchSwitchCancelsTheWatch(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.plan.key = "PROJ-PROV"
	cancelled := false
	m.watchCancel = func() { cancelled = true }
	m.detail = ptr(sampleBuild())
	m, _ = send(m, mkKey("b"))
	m, _ = send(m, branchesLoadedMsg{Gen: m.pickerGen, MasterKey: "PROJ-PROV", Branches: nil})
	m, _ = send(m, mkKey("enter"))
	require.True(t, cancelled)
	require.Nil(t, m.detail)
}

func TestBWithNoPlanSelectedDoesNothing(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.plan.key = ""
	m, cmd := send(m, mkKey("b"))
	require.Equal(t, overlayNone, m.overlay)
	require.Nil(t, cmd)
}

func TestHelpOverlayShowsEveryRowOfTheKeyMap(t *testing.T) {
	m := goldenModel(120, 40)
	m, _ = send(m, mkKey("?"))
	require.Equal(t, overlayHelp, m.overlay)
	v := m.View()
	for _, row := range keys.helpRows() {
		require.Contains(t, v, row.Keys, "help is missing %q", row.Keys)
	}
}

// TestErrorOverlayShowsWhyAndTry, spec §6.
func TestErrorOverlayShowsWhyAndTry(t *testing.T) {
	m := goldenModel(80, 24)
	m.err = errs.Authf("no token for %s", labOrigin).
		WithWhy("the keychain has no entry for this origin").
		WithTry("run bam login --server lab")
	m, _ = send(m, mkKey("e"))
	require.Equal(t, overlayError, m.overlay)
	v := m.View()
	require.Contains(t, v, "no token for")
	require.Contains(t, v, "keychain has no entry")
	require.Contains(t, v, "bam login")
}

// TestAuthErrorNamesBamLoginEvenWithoutATry: the UI cannot exit 4, so it must
// say what to do.
func TestAuthErrorNamesBamLoginEvenWithoutATry(t *testing.T) {
	m := goldenModel(80, 24)
	m.err = errs.Authf("401 from %s", labOrigin)
	m, _ = send(m, mkKey("e"))
	require.Contains(t, m.View(), "bam login")
}

func TestStatusBarShowsTheWhatNotTheWholeError(t *testing.T) {
	m := goldenModel(80, 24)
	m.err = errs.Bamboof("plan PROJ-BUILD not found").WithWhy("the server returned 404")
	bar := m.statusBar(80)
	require.Contains(t, bar, "not found")
	require.NotContains(t, bar, "404", "the why belongs behind e")
}

func TestEWithNoErrorDoesNothing(t *testing.T) {
	m := goldenModel(80, 24)
	m.err = nil
	m, _ = send(m, mkKey("e"))
	require.Equal(t, overlayNone, m.overlay)
}

func TestDismissingTheErrorClearsTheStatusBar(t *testing.T) {
	m := goldenModel(80, 24)
	m.err = errs.Bamboof("boom")
	m, _ = send(m, mkKey("e"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, overlayNone, m.overlay)
	require.NoError(t, m.err, "closing the error overlay dismisses the error")
}

// TestHelpIsReachableFromTheLogScreen.
func TestHelpIsReachableFromTheLogScreen(t *testing.T) {
	m := logModel()
	m, _ = send(m, mkKey("?"))
	require.Equal(t, overlayHelp, m.overlay)
}

// TestHelpScrollsToItsLastRow: the table is taller than 24 rows now, so the
// overlay has to scroll or the last keys are unreachable.
func TestHelpScrollsToItsLastRow(t *testing.T) {
	m := goldenModel(80, 24)
	m, _ = send(m, mkKey("?"))
	require.NotContains(t, m.View(), "q ctrl-c", "the table does not fit, so it starts at the top")

	for i := 0; i < 20; i++ {
		m, _ = send(m, mkKey("j"))
	}
	require.Contains(t, m.View(), "q ctrl-c", "j must reach the last row")
}

// TestHelpFitsWholeOnATallTerminal.
func TestHelpFitsWholeOnATallTerminal(t *testing.T) {
	m := goldenModel(120, 44)
	m, _ = send(m, mkKey("?"))
	v := m.View()
	for _, row := range keys.helpRows() {
		require.Contains(t, v, row.Keys)
	}
}

// TestErrorOverlayWrapsALongWhy: the reason is the useful part of an error,
// so a long one wraps rather than being cut at the box's edge.
func TestErrorOverlayWrapsALongWhy(t *testing.T) {
	m := goldenModel(80, 24)
	m.err = errs.Bamboof("could not start build of PROJ-SPECS").
		WithWhy("Change detection ignored for plan PROJ-SPECS, plan is suspended from building as Bamboo could not find a license for this instance.")
	m, _ = send(m, mkKey("e"))
	v := m.View()
	require.Contains(t, v, "suspended from")
	require.Contains(t, v, "license for this")
}

// TestEOpensTheErrorInTheRunForm: a failed trigger leaves the user in the
// form, so the form must let them read why.
func TestEOpensTheErrorInTheRunForm(t *testing.T) {
	for _, fields := range [][]formField{nil, {{Name: "cluster_name", Value: "lab1"}}} {
		m := goldenModel(80, 24)
		m.screen = screenForm
		m.form = formState{fields: fields}
		m.err = errs.Bamboof("could not start build of PROJ-SPECS").WithWhy("no license")
		m, _ = send(m, mkKey("e"))
		require.Equal(t, overlayError, m.overlay, "fields=%d", len(fields))
		require.Contains(t, m.View(), "no license")
	}
}
