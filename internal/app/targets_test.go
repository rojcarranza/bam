package app

import (
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDescribeTargets(t *testing.T) {
	cfg := testConfig(t.TempDir())
	infos, err := DescribeTargets(cfg, func(k string) string { return map[string]string{"LAB_DB_PASSWORD": "x"}[k] })
	require.NoError(t, err)
	require.Len(t, infos, 2)
	assert.Equal(t, "build", infos[0].Name)
	lab := infos[1]
	assert.Equal(t, "PROJ-PROV", lab.Plan)
	assert.Equal(t, "develop", lab.Branch)
	assert.True(t, lab.Watch)
	assert.Equal(t, []string{"cluster_name"}, lab.Required)
	assert.Equal(t, []TargetVar{
		{Name: "cluster_type", Value: "k8s"},
		{Name: "compute_nodes", Value: "2"},
		{Name: "db_password", Value: "${LAB_DB_PASSWORD}", EnvRef: "LAB_DB_PASSWORD", EnvSet: true, Secret: true},
	}, lab.Defaults)
	assert.Equal(t, "${LAB_DB_PASSWORD} (set)", lab.Defaults[2].Display())

	_, err = DescribeTarget(cfg, func(string) string { return "" }, "nope")
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
}

// TestRefFromTargetResolvesTheBranchWithoutTheServiceConfig is what lets the
// terminal UI act on a preset it refreshed from disk: the configuration the
// Service was built with may be older than the panel.
func TestRefFromTargetResolvesTheBranchWithoutTheServiceConfig(t *testing.T) {
	s := newService(t, fakeBamboo())
	info := TargetInfo{
		Name: "provision-lab", Plan: "PROJ-PROV", Branch: "develop",
		Options:  map[string][]string{"cluster_type": {"k8s", "dcos"}},
		Required: []string{"cluster_name"},
		Defaults: []TargetVar{{Name: "cluster_type", Value: "k8s"}},
	}

	ref, err := s.RefFromTarget(bg, info)
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PROV12", ref.PlanKey, "the branch plan, not the master")
	assert.Equal(t, "PROJ-PROV", ref.MasterKey)
	assert.Equal(t, "develop", ref.Branch)
	require.NotNil(t, ref.Target)
	assert.Equal(t, []string{"cluster_name"}, ref.Target.Required)
	assert.Equal(t, "k8s", ref.Target.Defaults["cluster_type"])
}

// TestRefFromTargetKeepsEnvReferencesAsWritten, so ValidateVars still sees a
// ${NAME} to resolve rather than an already-expanded value.
func TestRefFromTargetKeepsEnvReferencesAsWritten(t *testing.T) {
	s := newService(t, fakeBamboo())
	info := TargetInfo{Name: "t", Plan: "PROJ-PROV",
		Defaults: []TargetVar{{Name: "db_password", Value: "${LAB_DB_PASSWORD}", EnvRef: "LAB_DB_PASSWORD", Secret: true}}}
	ref, err := s.RefFromTarget(bg, info)
	require.NoError(t, err)
	assert.Equal(t, "${LAB_DB_PASSWORD}", ref.Target.Defaults["db_password"])
}
