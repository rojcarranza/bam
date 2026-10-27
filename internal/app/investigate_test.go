package app

import (
	"testing"

	"github.com/r0jjames/bam-cli/internal/provider"
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
	assert.Equal(t, "Error: BucketAlreadyExists (job INT)", inv.LikelyCause)
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
