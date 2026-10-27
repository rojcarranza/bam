package app

import (
	"errors"
	"testing"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resolvedTarget(t *testing.T, cfg *config.Config, name string) config.ResolvedTarget {
	t.Helper()
	rt, ok, err := cfg.Target(name)
	require.NoError(t, err)
	require.True(t, ok)
	return rt
}

func TestPlanSyncAddsAndMarks(t *testing.T) {
	s := newService(t, fakeBamboo())
	lab := s.Cfg.Project.Targets["provision-lab"]
	lab.Defaults["old_flag"] = "on"
	s.Cfg.Project.Targets["provision-lab"] = lab

	p, err := s.PlanSync(bg, resolvedTarget(t, s.Cfg, "provision-lab"))
	require.NoError(t, err)
	assert.Equal(t, "provision-lab", p.Name)
	assert.Equal(t, "PROJ-PROV", p.Plan)
	assert.Equal(t, s.Cfg.ProjectPath, p.File)
	assert.Equal(t, []config.DraftVar{{Name: "cluster_name", Value: "", Comment: `no plan default; last used "beta" in PROJ-PROV12-8`}}, p.Added)
	assert.Equal(t, []string{"old_flag"}, p.Stale)
	require.Len(t, p.Edits, 1)
	assert.Equal(t, SyncEdit{Path: s.Cfg.ProjectPath, KeyPath: []string{"targets"}, Edit: config.TargetEdit{
		Add:    p.Added,
		Mark:   []string{"old_flag"},
		Unmark: []string{"cluster_type", "compute_nodes", "db_password"},
		Plan:   "PROJ-PROV",
	}}, p.Edits[0])
}

func TestPlanSyncSpreadsEditsAcrossLayers(t *testing.T) {
	s := newService(t, fakeBamboo())
	s.Cfg.Machine.Targets = map[string]config.Target{"provision-lab": {Defaults: config.StringMap{"cluster_type": "dcos", "stale_here": "1"}}}
	s.Cfg.Machine.Repos = map[string]config.Repo{s.Cfg.RepoRoot: {Targets: map[string]config.Target{"provision-lab": {Plan: "PROJ-PROV"}}}}

	p, err := s.PlanSync(bg, resolvedTarget(t, s.Cfg, "provision-lab"))
	require.NoError(t, err)
	assert.Equal(t, s.Cfg.MachinePath, p.File, "added names go to the last layer that sets plan")
	require.Len(t, p.Edits, 3)
	assert.Empty(t, p.Edits[0].Edit.Add)
	assert.Equal(t, []string{"cluster_type"}, p.Edits[1].Edit.Unmark)
	assert.Equal(t, []string{"stale_here"}, p.Edits[1].Edit.Mark)
	assert.Equal(t, []string{"repos", s.Cfg.RepoRoot, "targets"}, p.Edits[2].KeyPath)
	assert.Equal(t, p.Added, p.Edits[2].Edit.Add)
}

func TestPlanSyncUpToDateHasNoAdds(t *testing.T) {
	s := newService(t, fakeBamboo())
	lab := s.Cfg.Project.Targets["provision-lab"]
	lab.Defaults["cluster_name"] = "x"
	s.Cfg.Project.Targets["provision-lab"] = lab
	p, err := s.PlanSync(bg, resolvedTarget(t, s.Cfg, "provision-lab"))
	require.NoError(t, err)
	assert.Empty(t, p.Added)
	assert.Empty(t, p.Stale)
	assert.NotNil(t, p.Stale, "always a slice, for JSON")
}

func TestPlanSyncMasksNewSecrets(t *testing.T) {
	f := fakeBamboo()
	f.Variables["PROJ-PROV"] = append(f.Variables["PROJ-PROV"], provider.Variable{Name: "api_secret", Masked: true})
	s := newService(t, f)
	p, err := s.PlanSync(bg, resolvedTarget(t, s.Cfg, "provision-lab"))
	require.NoError(t, err)
	assert.Contains(t, p.Added, config.DraftVar{Name: "api_secret", Value: "${API_SECRET}", Comment: "masked by Bamboo; set env var API_SECRET"})
}

// A lean preset leaves out what the plan already sends; sync must not copy it back.
func TestPlanSyncKeepsALeanPresetLean(t *testing.T) {
	s := newService(t, fakeBamboo())
	lab := s.Cfg.Project.Targets["provision-lab"]
	lab.Defaults = config.StringMap{"cluster_name": "x"}
	s.Cfg.Project.Targets["provision-lab"] = lab
	p, err := s.PlanSync(bg, resolvedTarget(t, s.Cfg, "provision-lab"))
	require.NoError(t, err)
	assert.Empty(t, p.Added, "plan defaults and the stored db_password are not added")
	assert.Empty(t, p.Stale)
}

func TestPlanSyncNeedsDeclaredVariables(t *testing.T) {
	f := fakeBamboo()
	f.VariablesErr = errs.Bamboof("no").Wrap(errs.ErrUnsupported)
	s := newService(t, f)
	_, err := s.PlanSync(bg, resolvedTarget(t, s.Cfg, "provision-lab"))
	require.Error(t, err)
	assert.Equal(t, errs.KindBamboo, errs.KindOf(err))
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Contains(t, err.Error(), "cannot read plan variables of PROJ-PROV on this server")
}

func TestDisplayDraftValue(t *testing.T) {
	assert.Equal(t, "eu", DisplayDraftValue(config.DraftVar{Name: "region", Value: "eu"}))
	assert.Equal(t, MaskedDisplay, DisplayDraftValue(config.DraftVar{Name: "db_password", Value: "hunter2"}))
	assert.Equal(t, "${DB_PASSWORD}", DisplayDraftValue(config.DraftVar{Name: "db_password", Value: "${DB_PASSWORD}"}))
	assert.Equal(t, "", DisplayDraftValue(config.DraftVar{Name: "db_password", Value: ""}))
}
