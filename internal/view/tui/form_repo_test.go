package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// BAW-7: the run form shows a preset's repositories as read-only rows, and a
// run whose repositories are not on the wanted branches asks in a dialog.
func repoFormModel(opened *[]string) Model {
	m := formModel()
	m.form.ref.Repositories = map[string]string{"infra-scripts": "feature/cli-tools"}
	m.form.repos = presetRepoRows(m.form.ref)
	m.deps.Open = func(u string) error { *opened = append(*opened, u); return nil }
	f := m.svc.P.(*fake.Provider)
	f.Branches = map[string][]provider.Branch{"PROJ-PROV": {{Key: "PROJ-PROV12", ShortName: "develop", PlanKey: "PROJ-PROV"}}}
	f.History = map[string][]provider.Build{"PROJ-PROV12": {{Key: "PROJ-PROV12-8", PlanKey: "PROJ-PROV12", Number: 8, State: provider.StateSuccess,
		Stages: []provider.Stage{{Jobs: []provider.Job{{Key: "PROJ-PROV12-TF-8", Branches: map[string]string{"infra-scripts": "develop"}}}}}}}}
	return m
}

func TestFormShowsRepositoriesReadOnly(t *testing.T) {
	var opened []string
	m := repoFormModel(&opened)
	v := m.View()
	require.Contains(t, v, "infra-scripts")
	require.Contains(t, v, "feature/cli-tools")
	require.Contains(t, v, "1 repositories")

	// The cursor walks the variables and the revision row only.
	for range 10 {
		m, _ = send(m, mkKey("j"))
		require.LessOrEqual(t, m.form.cursor, len(m.form.fields))
	}
	// Enter on a row never opens an input for a repository; b does nothing in the form.
	m.form.cursor = 0
	m, _ = send(m, mkKey("b"))
	require.False(t, m.form.editing)
}

func TestTriggerChecksRepositoriesOpensTheTabAndAsksFirst(t *testing.T) {
	var opened []string
	m := repoFormModel(&opened)
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.NotNil(t, cmd)
	require.Empty(t, m.svc.P.(*fake.Provider).Triggered, "nothing runs before the check")

	msg, ok := cmd().(repoCheckedMsg)
	require.True(t, ok)
	require.Equal(t, []string{"https://bamboo.lab.example/branch/admin/config/configureChainBranchRepositories.action?planKey=PROJ-PROV12"}, opened)
	require.True(t, msg.Check.Needed())

	m, cmd = send(m, msg)
	require.Nil(t, cmd, "the dialog waits for the person")
	require.Equal(t, overlayConfirm, m.overlay)
	require.Contains(t, m.confirm.Prompt, "infra-scripts  develop → feature/cli-tools")
	require.Contains(t, m.confirm.Prompt, "Repositories tab")
	require.Empty(t, m.svc.P.(*fake.Provider).Triggered)

	m, cmd = send(m, mkKey("y"))
	require.NotNil(t, cmd, "confirming triggers")
	_ = m
}

func TestTriggerWithTheWantedBranchesRunsWithoutADialog(t *testing.T) {
	var opened []string
	m := repoFormModel(&opened)
	m.form.ref.Repositories = map[string]string{"infra-scripts": "develop"}
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	msg, ok := cmd().(repoCheckedMsg)
	require.True(t, ok)
	require.Empty(t, opened)
	m, cmd = send(m, msg)
	require.Equal(t, overlayNone, m.overlay)
	require.NotNil(t, cmd, "the run starts at once")
	require.IsType(t, triggeredMsg{}, cmd())
}
