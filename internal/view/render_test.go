package view

import (
	"strings"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleBuilds() []provider.Build {
	return []provider.Build{
		{Key: "PROJ-PLAN-1843", State: provider.StateRunning, Branch: "develop", StartedAt: fixedNow.Add(-2 * time.Minute), Duration: 2*time.Minute + 3*time.Second, Reason: "Manual run by jdoe"},
		{Key: "PROJ-PLAN-1842", State: provider.StateSuccess, Branch: "develop", StartedAt: fixedNow.Add(-18 * time.Minute), Duration: 4*time.Minute + 11*time.Second, Reason: "Changes by a1b2c3d"},
		{Key: "PROJ-PLAN7-12", State: provider.StateFailed, Branch: "feat/foo", StartedAt: fixedNow.Add(-time.Hour), Duration: 3*time.Minute + 40*time.Second, Reason: "Manual run by jdoe"},
	}
}

func TestBuildListMatchesSpecExample(t *testing.T) {
	o, buf := testOut(true)
	require.NoError(t, BuildList(o, sampleBuilds()))
	assert.Equal(t, ""+
		"BUILD           STATE      BRANCH    STARTED  DURATION  REASON\n"+
		"PROJ-PLAN-1843  ▸ running  develop   2m ago   2m03s     Manual run by jdoe\n"+
		"PROJ-PLAN-1842  ✓ success  develop   18m ago  4m11s     Changes by a1b2c3d\n"+
		"PROJ-PLAN7-12   ✗ failed   feat/foo  1h ago   3m40s     Manual run by jdoe\n", buf.String())
}

func TestBuildListOnPipeUsesAbsoluteTimes(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, BuildList(o, sampleBuilds()[:1]))
	assert.Contains(t, buf.String(), "2026-09-12T11:58:00Z")
}

func TestPlanListGolden(t *testing.T) {
	o, buf := testOut(true)
	groups := []PlanGroup{{
		Project: provider.Project{Key: "PROJ", Name: "Example Project"},
		Plans: []provider.Plan{
			{Key: "PROJ-BUILD", Name: "Build and test", LastBuild: &provider.BuildSummary{Key: "PROJ-BUILD-482", Number: 482, State: provider.StateSuccess, FinishedAt: fixedNow.Add(-12 * time.Minute), Reason: "Manual run by jdoe"}},
			{Key: "PROJ-PROV", Name: "Provision lab", LastBuild: &provider.BuildSummary{Key: "PROJ-PROV-97", Number: 97, State: provider.StateFailed, FinishedAt: fixedNow.Add(-2 * time.Hour), Reason: "Scheduled"}},
			{Key: "PROJ-OLD", Name: "Legacy"},
		},
	}}
	require.NoError(t, PlanList(o, groups))
	golden(t, "plan_list", buf.String())
}

func TestPlanListWithoutProjectNameHasNoTrailingSpace(t *testing.T) {
	o, buf := testOut(true)
	require.NoError(t, PlanList(o, []PlanGroup{{Project: provider.Project{Key: "PROJ"}}}))
	assert.True(t, strings.HasPrefix(buf.String(), "PROJ\n"), buf.String())
}

func TestBuildDetailGolden(t *testing.T) {
	o, buf := testOut(true)
	b := provider.Build{
		Key: "PROJ-BUILD12-44", URL: "https://bamboo.example.com/browse/PROJ-BUILD12-44", PlanKey: "PROJ-BUILD12", Branch: "develop",
		Number: 44, State: provider.StateFailed, Reason: "Custom build by jdoe", CustomBuild: true, Labels: []string{"nightly"},
		StartedAt: fixedNow.Add(-3 * time.Hour), FinishedAt: fixedNow.Add(-3*time.Hour + 220*time.Second), Duration: 220 * time.Second,
		QueueDuration: 12 * time.Second, Agent: "linux-3",
		Revisions: []provider.Revision{{Repository: "app", Revision: "a1b2c3d4e5f6"}},
		Stages: []provider.Stage{
			{Name: "Checkout", State: provider.StateSuccess, Duration: 12 * time.Second, Jobs: []provider.Job{{Key: "PROJ-BUILD12-JOB1-44", Name: "Checkout", State: provider.StateSuccess, Duration: 12 * time.Second}}},
			{Name: "Test", State: provider.StateFailed, Duration: 31 * time.Second, Jobs: []provider.Job{
				{Key: "PROJ-BUILD12-UNIT-44", Name: "Unit", State: provider.StateSuccess, Duration: 28 * time.Second},
				{Key: "PROJ-BUILD12-INT-44", Name: "Integration", State: provider.StateFailed, Duration: 31 * time.Second}}},
			{Name: "Package", State: provider.StateNotBuilt},
		},
		FailedTests: []string{"app.AuthTest.TestLogin", "app.AuthTest.TestRefresh"},
	}
	require.NoError(t, BuildDetail(o, b))
	golden(t, "build_detail", buf.String())
	assert.Contains(t, buf.String(), "bam logs PROJ-BUILD12-44 --job INT")
}

func TestVarsMasksSecrets(t *testing.T) {
	o, buf := testOut(true)
	require.NoError(t, Vars(o, app.PlanVarsResult{FromBuild: "PROJ-PROV12-8", Rows: []app.VarRow{
		{Name: "cluster_type", Value: "k8s", LastUsed: "dcos"},
		{Name: "db_password", Value: "real-secret", LastUsed: "real-secret", Masked: true},
	}}))
	assert.NotContains(t, buf.String(), "real-secret")
	assert.Contains(t, buf.String(), "LAST USED (#8)")
	golden(t, "vars", buf.String())
}

func TestTargetDetailGolden(t *testing.T) {
	o, buf := testOut(true)
	require.NoError(t, TargetDetail(o, app.TargetInfo{
		Name: "provision-lab", Plan: "PROJ-PROV", Branch: "develop", Watch: true, Timeout: 45 * time.Minute,
		Defaults: []app.TargetVar{
			{Name: "cluster_type", Value: "k8s"},
			{Name: "db_password", Value: "${LAB_DB_PASSWORD}", EnvRef: "LAB_DB_PASSWORD", EnvSet: false, Secret: true},
		},
		Options:   map[string][]string{"cluster_type": {"k8s", "dcos"}},
		Required:  []string{"cluster_name"},
		DefinedIn: []string{"/home/jdoe/src/repo/.bam.yaml"},
	}))
	golden(t, "target_detail", buf.String())
}
