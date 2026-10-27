package app

import (
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompareLastSuccess(t *testing.T) {
	p := fakeBamboo()
	b := failedBuild()
	b.Revisions = []provider.Revision{{Repository: "app", Revision: "a1b2c3d"}, {Repository: "infra", Revision: "same"}}
	p.History["PROJ-BUILD12"] = []provider.Build{
		{Key: "PROJ-BUILD12-45", PlanKey: "PROJ-BUILD12", Number: 45, State: provider.StateSuccess}, // newer, skipped
		{Key: "PROJ-BUILD12-41", PlanKey: "PROJ-BUILD12", Number: 41, State: provider.StateSuccess,
			Revisions: []provider.Revision{{Repository: "app", Revision: "9f8e7d6"}, {Repository: "infra", Revision: "same"}, {Repository: "old", Revision: "x"}}},
	}
	p.BuildVars = map[string]map[string]string{
		"PROJ-BUILD12-41": {"ENV": "dev", "db_password": "a", "KEEP": "1", "TOKEN": "********"},
		"PROJ-BUILD12-44": {"ENV": "staging", "db_password": "b", "KEEP": "1", "TOKEN": "********", "NEW": "y"},
	}
	c, err := newService(t, p).CompareLastSuccess(bg, b)
	require.NoError(t, err)
	assert.Equal(t, "PROJ-BUILD12-41", c.Since.Key)
	assert.Equal(t, []Change{{Name: "app", Was: "9f8e7d6", Now: "a1b2c3d"}, {Name: "old", Was: "x"}}, c.Revisions)
	assert.Equal(t, []Change{{Name: "ENV", Was: "dev", Now: "staging"}, {Name: "NEW", Now: "y"},
		{Name: "db_password", Was: MaskedDisplay, Now: MaskedDisplay}}, c.Variables)
}

func TestCompareLastSuccessNoneAndUnsupported(t *testing.T) {
	p := fakeBamboo()
	c, err := newService(t, p).CompareLastSuccess(bg, failedBuild())
	require.NoError(t, err)
	assert.Empty(t, c.Since.Key)

	p.History["PROJ-BUILD12"] = []provider.Build{{Key: "PROJ-BUILD12-41", PlanKey: "PROJ-BUILD12", Number: 41, State: provider.StateSuccess}}
	p.BuildVarsErr = errs.ErrUnsupported
	c, err = newService(t, p).CompareLastSuccess(bg, failedBuild())
	require.NoError(t, err)
	assert.Equal(t, "PROJ-BUILD12-41", c.Since.Key)
	assert.Equal(t, []Change{}, c.Revisions)
	assert.Nil(t, c.Variables)
}
