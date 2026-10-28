package app

import (
	"context"
	"testing"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvestigate(t *testing.T) {
	p := logsProvider()
	p.Logs["PROJ-BUILD12-UNIT-44"] = []string{"ok"}
	p.Logs["PROJ-BUILD12-INT-44"] = []string{
		"terraform apply", "Error: BucketAlreadyExists", "x",
		"Failing task since return code of [terraform] was 1 while expected 0",
	}
	s := newService(t, p)
	inv, err := s.Investigate(bg, failedBuild(), InvestigateOptions{Context: 1, Max: 1})
	require.NoError(t, err)
	require.Len(t, inv.Jobs, 2)
	assert.Equal(t, "UNIT", inv.Jobs[0].Short)
	assert.Empty(t, inv.Jobs[0].Errors)
	assert.Equal(t, "INT", inv.Jobs[1].Short)
	assert.Equal(t, "Test", inv.Jobs[1].Stage)
	require.Len(t, inv.Jobs[1].Errors, 1)
	assert.Equal(t, []string{"terraform apply", "Error: BucketAlreadyExists", "x"}, inv.Jobs[1].Errors[0].Context)
	assert.Equal(t, 1, inv.Jobs[1].Hidden, "the bamboo-task line is past --max")
	assert.Zero(t, inv.Jobs[0].Hidden)
	assert.Equal(t, "Error: BucketAlreadyExists (job INT)", inv.LikelyCause)
}

type noRestart struct{ *fake.Provider }

func (noRestart) CannotRestart(context.Context) bool { return true }

func TestInvestigateNotesKnownRestartRefusal(t *testing.T) {
	inv, err := newService(t, logsProvider()).Investigate(bg, failedBuild(), InvestigateOptions{})
	require.NoError(t, err)
	assert.False(t, inv.NoRerun, "a provider without RestartChecker may restart")
	s := newService(t, logsProvider())
	s.P = noRestart{s.P.(*fake.Provider)}
	inv, err = s.Investigate(bg, failedBuild(), InvestigateOptions{})
	require.NoError(t, err)
	assert.True(t, inv.NoRerun)
}

func TestInvestigateSkipsBuildsThatDidNotFail(t *testing.T) {
	s := newService(t, logsProvider())
	b := failedBuild()
	b.State = provider.StateSuccess
	inv, err := s.Investigate(bg, b, InvestigateOptions{})
	require.NoError(t, err)
	assert.Empty(t, inv.Jobs)
	assert.Empty(t, inv.LikelyCause)
}

func TestInvestigateTruncatedLogKeepsLineNumbers(t *testing.T) {
	p := logsProvider()
	p.Logs["PROJ-BUILD12-INT-44"] = []string{"x", "Error: boom"}
	p.LogSkipped = map[string]int{"PROJ-BUILD12-INT-44": 1000}
	s := newService(t, p)
	inv, err := s.Investigate(bg, failedBuild(), InvestigateOptions{Context: 1})
	require.NoError(t, err)
	assert.Zero(t, inv.Jobs[0].Skipped)
	j := inv.Jobs[1]
	assert.Equal(t, 1000, j.Skipped)
	require.Len(t, j.Errors, 1)
	assert.Equal(t, 1002, j.Errors[0].Line)
	assert.Equal(t, 1001, j.Errors[0].Start)
}

func TestInvestigateConfiguredChildren(t *testing.T) {
	s := newService(t, logsProvider())
	inv, err := s.Investigate(bg, failedBuild(), InvestigateOptions{})
	require.NoError(t, err)
	assert.Empty(t, inv.Children)
	s.Cfg.Project.Children = map[string][]string{"PROJ-BUILD12": {"OPS-DEPLOY"}}
	inv, err = s.Investigate(bg, failedBuild(), InvestigateOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"OPS-DEPLOY"}, inv.Children)
}
