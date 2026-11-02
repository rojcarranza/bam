package config

import (
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #130: a preset holds repository branch overrides under
// repo_branches, keyed REPO or PLAN:REPO, like --repo-branch.
func TestParseRepoKey(t *testing.T) {
	cases := map[string]RepoBranch{
		"repo-a":               {Repository: "repo-a"},
		"PROJ-CHILD:repo-a":    {Plan: "PROJ-CHILD", Repository: "repo-a"},
		"PROJ-CHILD:ops:infra": {Plan: "PROJ-CHILD", Repository: "ops:infra"},
		"tools:infra":          {Repository: "tools:infra"},
	}
	for key, want := range cases {
		got, ok := ParseRepoKey(key)
		assert.True(t, ok, key)
		assert.Equal(t, want, got, key)
		assert.Equal(t, key, got.Key(), "the key round-trips")
	}
	for _, key := range []string{"", "  ", "PROJ-CHILD:", "PROJ-CHILD: "} {
		_, ok := ParseRepoKey(key)
		assert.False(t, ok, "%q", key)
	}
}

func TestRepoBranchesLoadFromAPresetsFile(t *testing.T) {
	dir := t.TempDir()
	presets := filepath.Join(dir, "bam", "presets.yaml")
	writeFile(t, presets, `presets:
  feature-101:
    plan: PROJ-PARENT
    repo_branches:
      repo-a: feature/PROJ-101
      PROJ-CHILD:repo-a: feature/PROJ-101-extra
`)
	cfg, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: filepath.Join(dir, "m.yaml"), PresetsFile: presets, Getenv: noEnv})
	require.NoError(t, err)

	got, ok, err := cfg.Target("feature-101")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, []RepoBranch{
		{Plan: "PROJ-CHILD", Repository: "repo-a", Branch: "feature/PROJ-101-extra"},
		{Repository: "repo-a", Branch: "feature/PROJ-101"},
	}, got.RepoBranchList())
}

func TestRepoBranchesMergeAcrossLayers(t *testing.T) {
	c := baseConfig()
	c.Project.Targets = map[string]Target{"lab": {Plan: "PROJ-LAB", RepoBranches: StringMap{"repo-a": "main", "repo-b": "develop"}}}
	c.Machine.Targets = map[string]Target{"lab": {RepoBranches: StringMap{"repo-a": "feature/PROJ-101"}}}
	lab, ok, err := c.Target("lab")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, StringMap{"repo-a": "feature/PROJ-101", "repo-b": "develop"}, lab.RepoBranches)
}

func TestRepoBranchesValidation(t *testing.T) {
	cases := map[string]struct {
		target Target
		want   string
	}{
		"empty plan prefix repo": {Target{Plan: "PROJ-A", RepoBranches: StringMap{"PROJ-B:": "x"}}, `repo_branches key "PROJ-B:" is not REPO or PLAN:REPO`},
		"empty branch":           {Target{Plan: "PROJ-A", RepoBranches: StringMap{"repo-a": ""}}, `repo_branches.repo-a has no branch`},
		"with branch":            {Target{Plan: "PROJ-A", Branch: "develop", RepoBranches: StringMap{"repo-a": "x"}}, `sets both branch and repo_branches`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := baseConfig()
			c.Project.Targets = map[string]Target{"t": tc.target}
			_, err := c.Targets()
			require.Error(t, err)
			assert.Equal(t, errs.KindConfig, errs.KindOf(err))
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
