package tui

import (
	"errors"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/stretchr/testify/require"
)

func typeLine(m Model, s string) Model {
	for _, r := range s {
		m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func runCmdLine(m Model, line string) (Model, tea.Cmd) {
	m, _ = send(m, mkKey(":"))
	m = typeLine(m, line)
	return send(m, mkKey("enter"))
}

func TestColonOpensThePromptOnHomeAndPanels(t *testing.T) {
	m, _ := send(homeModel(), mkKey(":"))
	require.Equal(t, inputCommand, m.inputFor)
	p := homeModel()
	p.screen = screenColumns
	p, _ = send(p, mkKey(":"))
	require.Equal(t, inputCommand, p.inputFor)
}

func TestColonIsTextInsideAnotherInput(t *testing.T) {
	m, _ := send(homeModel(), mkKey("/"))
	m, _ = send(m, mkKey(":"))
	require.Equal(t, inputFilter, m.inputFor)
	require.Equal(t, ":", m.input.Value())
}

func TestPresetsAndPlansCommands(t *testing.T) {
	m, _ := runCmdLine(homeModel(), "presets")
	require.Equal(t, homePresets, m.home.view)
	m, _ = runCmdLine(m, "plans")
	require.Equal(t, homePlans, m.home.view)

	p := homeModel()
	p.screen = screenColumns
	p, _ = runCmdLine(p, "plans")
	require.Equal(t, screenHome, p.screen, ":plans from the panels returns Home")
}

func TestAUniquePrefixIsAccepted(t *testing.T) {
	m, _ := runCmdLine(homeModel(), "pre")
	require.Equal(t, homePresets, m.home.view)
}

func TestAnAmbiguousOrUnknownCommandIsAStatusError(t *testing.T) {
	m, _ := runCmdLine(homeModel(), "p")
	require.EqualError(t, m.err, `ambiguous command "p": plans, presets, projects, project`)
	require.Equal(t, homePlans, m.home.view)

	m, _ = runCmdLine(homeModel(), "frobnicate")
	require.EqualError(t, m.err, `unknown command "frobnicate"; commands: plans presets sets projects project server sort tree quit`)
	require.Equal(t, inputNone, m.inputFor)
}

func TestSortCommand(t *testing.T) {
	m, _ := runCmdLine(homeModel(), "sort -age")
	require.Equal(t, homeSort{col: colAge, desc: true}, m.home.sort)
	m, _ = runCmdLine(m, "sort bogus")
	require.EqualError(t, m.err, `unknown sort column "bogus"; columns: key name state age`)
	m, _ = runCmdLine(homeModel(), "sort")
	require.EqualError(t, m.err, ":sort needs a column")
}

func TestProjectCommand(t *testing.T) {
	m, cmd := runCmdLine(homeModel(), "project OPS")
	require.Equal(t, "OPS", m.project)
	require.NotNil(t, cmd)
	m, _ = runCmdLine(m, "project all")
	require.Equal(t, "", m.project)

	// finding 4: rejecting an unknown project only makes sense once projects
	// are actually configured; projectChoices falls back to the loaded
	// plans' projects otherwise, and those are not a validation list.
	configured := homeModel()
	configured.svc.Cfg.Project = &config.ProjectFile{Version: 1, Projects: []string{"OPS", "PROJ"}}
	m, _ = runCmdLine(configured, "project NOPE")
	require.EqualError(t, m.err, `project "NOPE" is not listed; projects: OPS PROJ`)
}

// TestProjectCommandAcceptsAnyKeyWhenNothingIsConfigured covers finding 4:
// with no configured projects, :project must accept any key rather than
// reject it against the projects merely seen in the loaded plans.
func TestProjectCommandAcceptsAnyKeyWhenNothingIsConfigured(t *testing.T) {
	m, cmd := runCmdLine(homeModel(), "project OTHER")
	require.Equal(t, "OTHER", m.project)
	require.NoError(t, m.err)
	require.NotNil(t, cmd)
}

func TestServerCommand(t *testing.T) {
	m := homeModel()
	m.deps.Servers = append(m.deps.Servers, Server{Alias: "work", URL: "https://bamboo.example.com"})
	m, _ = runCmdLine(m, "server work")
	require.Equal(t, "work", m.server)
	m, _ = runCmdLine(m, "server nope")
	require.EqualError(t, m.err, `no server "nope"; servers: lab work`)
}

func TestQuitCommand(t *testing.T) {
	_, cmd := runCmdLine(homeModel(), "q")
	require.NotNil(t, cmd)
	require.IsType(t, tea.QuitMsg{}, cmd())
}

func TestCompletion(t *testing.T) {
	m := homeModel()
	require.Equal(t, []string{"plans", "presets", "projects", "project"}, m.complete("p"))
	require.Equal(t, []string{"project OPS", "project PROJ", "project all"}, m.complete("project "))
	require.Equal(t, []string{"sort age"}, m.complete("sort ag"))
	require.Equal(t, []string{"sort -age"}, m.complete("sort -a"))
}

func TestTabCompletesAUniqueCandidate(t *testing.T) {
	m, _ := send(homeModel(), mkKey(":"))
	m = typeLine(m, "pres")
	m, _ = send(m, mkKey("tab"))
	require.Equal(t, "presets", m.input.Value())
	m, _ = send(homeModel(), mkKey(":"))
	m = typeLine(m, "ser")
	m, _ = send(m, mkKey("tab"))
	require.Equal(t, "server ", m.input.Value(), "a command with an argument gets its space")
}

func TestTabOnAnAliasCompletesItsCommand(t *testing.T) {
	require.Equal(t, []string{"projects"}, homeModel().complete("proj"), "proj is :projects, not a prefix of :project")
	m, _ := send(homeModel(), mkKey(":"))
	m = typeLine(m, "proj")
	m, _ = send(m, mkKey("tab"))
	require.Equal(t, "projects", m.input.Value())
}

func TestHelpListsTheCommands(t *testing.T) {
	m := goldenModel(120, 50) // tall enough for the whole help overlay
	m, _ = send(m, mkKey("?"))
	v := m.View()
	for _, r := range commandHelpRows() {
		require.Contains(t, v, r.Keys)
	}
}

// TestACommandErrorClearsOnTheNextCommand: a mistyped command's error must
// not outlive the next command the user runs.
func TestACommandErrorClearsOnTheNextCommand(t *testing.T) {
	m, _ := runCmdLine(homeModel(), "p")
	require.Error(t, m.err)
	m, _ = runCmdLine(m, "presets")
	require.NoError(t, m.err, "a successful command clears the error of the one before")

	m, _ = runCmdLine(homeModel(), "frobnicate")
	require.Error(t, m.err)
	m, _ = send(m, mkKey(":"))
	require.NoError(t, m.err, "opening the prompt again clears it, so the prompt line is not fighting an old error")
}

// TestACommandNeverClearsAnotherError: only the command bar's own errors are
// its to clear.
func TestACommandNeverClearsAnotherError(t *testing.T) {
	m := homeModel()
	m, _ = send(m, errMsg{Err: errors.New("bamboo returned 500"), Stream: streamCancel, Gen: m.cancelGen})
	m, _ = runCmdLine(m, "presets")
	require.EqualError(t, m.err, "bamboo returned 500")
}

// TestAPastedChunkActsLikeTypedKeys: a terminal may deliver several typed
// characters as one message; they must act as the keys they are.
func TestAPastedChunkActsLikeTypedKeys(t *testing.T) {
	m, _ := send(homeModel(), mkKey("/fail"))
	require.Equal(t, inputFilter, m.inputFor)
	require.Equal(t, "fail", m.input.Value())

	m, _ = send(homeModel(), mkKey("ss"))
	require.Equal(t, homeSort{col: colState}, m.home.sort)
}

// TestCommonPrefixKeepsWholeRunes: tab must never leave half a multi-byte
// rune in the prompt. "é" and "è" share their first UTF-8 byte.
func TestCommonPrefixKeepsWholeRunes(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"presets", "project"}, "pr"},
		{[]string{"préfixe a", "préfixe b"}, "préfixe "},
		{[]string{"café", "cafè"}, "caf"},
		{[]string{"é", "è"}, ""},
		{[]string{"same", "same"}, "same"},
		{[]string{"abc", "ab"}, "ab"},
	} {
		got := commonPrefix(tc.in)
		require.Equal(t, tc.want, got, "%q", tc.in)
		require.True(t, utf8.ValidString(got), "%q gave invalid UTF-8 %q", tc.in, got)
	}
}
