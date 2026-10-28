package bamboo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var ctx = context.Background()

// TestRecordedShapesDecode runs the recordings from Task 9 through the same
// decoding as the hand-written fixtures. It skips when nothing is recorded.
func TestRecordedShapesDecode(t *testing.T) {
	for _, f := range []string{"recorded/results.json", "recorded/result_detail.json"} {
		if _, err := os.Stat(filepath.Join("testdata", f)); err != nil {
			t.Skipf("%s not recorded", f)
		}
	}
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/REC-PLAN":   {fixture: "recorded/results.json"},
		"GET /rest/api/latest/result/REC-PLAN-1": {fixture: "recorded/result_detail.json"},
	})
	builds, err := c.ListBuilds(ctx, "REC-PLAN", provider.ListOptions{Limit: 5})
	require.NoError(t, err)
	require.NotEmpty(t, builds)
	for _, b := range builds {
		assert.NotEmpty(t, b.Key)
		assert.NotEmpty(t, b.PlanName, b.Key)
		assert.NotEqual(t, provider.StateUnknown, b.State, b.Key)
		assert.False(t, b.StartedAt.IsZero(), b.Key)
	}
	b, err := c.GetBuild(ctx, "REC-PLAN-1")
	require.NoError(t, err)
	assert.Equal(t, "Provision Cluster", b.PlanName)
	require.NotEmpty(t, b.Stages)
	require.NotEmpty(t, b.Stages[0].Jobs)
	assert.NotEmpty(t, b.Stages[0].Jobs[0].Name)
}

func TestCurrentUserAndInfo(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/currentUser": {fixture: "currentUser.json"},
		"GET /rest/api/latest/info":        {fixture: "info.json"},
	})
	u, err := c.CurrentUser(ctx)
	require.NoError(t, err)
	assert.Equal(t, provider.User{Name: "jdoe", FullName: "J Doe"}, u)
	info, err := c.ServerInfo(ctx)
	require.NoError(t, err)
	assert.Equal(t, "9.6.2", info.Version)
}

func TestListProjects(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"GET /rest/api/latest/project": {fixture: "projects.json"}})
	ps, err := c.ListProjects(ctx)
	require.NoError(t, err)
	require.Len(t, ps, 2)
	assert.Equal(t, "PROJ", ps[0].Key)
	assert.Equal(t, "Example Project", ps[0].Name)
	assert.Equal(t, c.URL("PROJ"), ps[0].URL)
}

func TestListPlansWithLastBuild(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/project/PROJ":      {fixture: "project_plans.json"},
		"GET /rest/api/latest/result/PROJ-BUILD": {fixture: "results.json"},
		"GET /rest/api/latest/result/PROJ-OLD":   {body: `{"results":{"size":0,"result":[]}}`},
	})
	plans, err := c.ListPlans(ctx, "PROJ")
	require.NoError(t, err)
	require.Len(t, plans, 2)
	assert.Equal(t, "PROJ-BUILD", plans[0].Key)
	assert.Equal(t, "Build and test", plans[0].Name)
	assert.Equal(t, "PROJ", plans[0].ProjectKey)
	require.NotNil(t, plans[0].LastBuild)
	assert.Equal(t, 482, plans[0].LastBuild.Number)
	assert.Equal(t, provider.StateSuccess, plans[0].LastBuild.State)
	assert.Equal(t, "Manual run by J Doe", plans[0].LastBuild.Reason)
	assert.Nil(t, plans[1].LastBuild, "never built")

	for _, r := range rec.all() {
		if r.URL.Path == "/rest/api/latest/project/PROJ" {
			assert.Equal(t, "plans.plan", r.URL.Query().Get("expand"))
		}
	}
}

func TestListPlansUnknownProject(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{})
	_, err := c.ListPlans(ctx, "NOPE")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "project NOPE not found")
	assert.True(t, errors.Is(err, errs.ErrNotFound))
}

func TestGetPlanAndBranches(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/plan/PROJ-BUILD":        {fixture: "plan.json"},
		"GET /rest/api/latest/plan/PROJ-BUILD/branch": {fixture: "branches.json"},
	})
	p, err := c.GetPlan(ctx, "PROJ-BUILD")
	require.NoError(t, err)
	assert.Equal(t, "PROJ", p.ProjectKey)
	bs, err := c.ListBranches(ctx, "PROJ-BUILD")
	require.NoError(t, err)
	require.Len(t, bs, 2)
	assert.Equal(t, provider.Branch{Key: "PROJ-BUILD12", Name: "Example Project - Build and test - develop", ShortName: "develop",
		PlanKey: "PROJ-BUILD", URL: c.URL("PROJ-BUILD12")}, bs[0])
}

func TestGetPlanNotFound(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{})
	_, err := c.GetPlan(ctx, "PROJ-NOPE")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan PROJ-NOPE not found")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "bam plan list", e.Try)
}

func TestListBuildsFiltersState(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{"GET /rest/api/latest/result/PROJ-BUILD": {fixture: "results.json"}})
	bs, err := c.ListBuilds(ctx, "PROJ-BUILD", provider.ListOptions{Limit: 10, State: provider.StateFailed})
	require.NoError(t, err)
	require.Len(t, bs, 1)
	assert.Equal(t, 481, bs[0].Number)
	assert.Equal(t, "Failed", rec.all()[0].URL.Query().Get("buildstate"))
	assert.Equal(t, "results.result", rec.all()[0].URL.Query().Get("expand"))

	all, err := c.ListBuilds(ctx, "PROJ-BUILD", provider.ListOptions{})
	require.NoError(t, err)
	assert.Equal(t, []provider.State{provider.StateSuccess, provider.StateFailed, provider.StateStopped},
		[]provider.State{all[0].State, all[1].State, all[2].State})
	assert.Equal(t, "Build and test", all[0].PlanName)
}

func TestGetBuildMapsEverything(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{"GET /rest/api/latest/result/PROJ-BUILD12-44": {fixture: "result_detail.json"}})
	b, err := c.GetBuild(ctx, "PROJ-BUILD12-44")
	require.NoError(t, err)

	assert.Equal(t, "PROJ-BUILD12-44", b.Key)
	assert.Equal(t, c.URL("PROJ-BUILD12-44"), b.URL)
	assert.Equal(t, "PROJ-BUILD12", b.PlanKey)
	assert.Equal(t, "develop", b.Branch)
	assert.Equal(t, "PROJ-BUILD", b.MasterKey)
	assert.Equal(t, "Build and test", b.PlanName, "a branch build is named after its master plan, not the branch")
	assert.Equal(t, 44, b.Number)
	assert.Equal(t, provider.StateFailed, b.State)
	assert.Equal(t, "Custom build by J Doe", b.Reason)
	assert.True(t, b.CustomBuild)
	assert.Equal(t, []string{"nightly"}, b.Labels)
	assert.Equal(t, "a1b2c3d", b.Revisions[0].Short())
	assert.Equal(t, "app", b.Revisions[0].Repository)
	assert.Equal(t, 220*time.Second, b.Duration)
	assert.True(t, time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC).Equal(b.StartedAt))

	require.Len(t, b.Stages, 3)
	assert.Equal(t, provider.StateSuccess, b.Stages[0].State)
	assert.Equal(t, provider.StateFailed, b.Stages[1].State)
	assert.Equal(t, 31*time.Second, b.Stages[1].Duration, "stage duration is its longest job")
	assert.Equal(t, provider.StateNotBuilt, b.Stages[2].State)
	assert.Equal(t, provider.Job{Key: "PROJ-BUILD12-INT-44", Name: "Integration", State: provider.StateFailed,
		Duration: 31 * time.Second, URL: c.URL("PROJ-BUILD12-INT-44")}, b.Stages[1].Jobs[1])

	assert.Equal(t, buildExpand, rec.all()[0].URL.Query().Get("expand"))
}

func TestGetBuildNotFound(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{})
	_, err := c.GetBuild(ctx, "PROJ-BUILD-9")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build PROJ-BUILD-9 not found")
	assert.True(t, errors.Is(err, errs.ErrNotFound))
}

// TestRecordedChildReason checks the reason of a recorded child build maps to
// the plain text the build tree matches on.
func TestRecordedChildReason(t *testing.T) {
	if _, err := os.Stat(filepath.Join("testdata", "recorded", "children_child_0.json")); err != nil {
		t.Skip("children_child_0.json not recorded")
	}
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/LAB-CHILD-1": {fixture: "recorded/children_child_0.json"},
	})
	b, err := c.GetBuild(ctx, "LAB-CHILD-1")
	require.NoError(t, err)
	assert.Equal(t, "Child of LAB-PARENT-2", b.Reason)
}

func TestListBuildsAsksForUnfinishedBuildsOnlyWhenTold(t *testing.T) {
	// Without includeAllStates, Bamboo returns finished builds only, so a
	// queued or running child would never reach a build tree.
	c, rec := newTestServer(t, map[string]*route{"GET /rest/api/latest/result/OPS-DEPLOY": {fixture: "results_unfinished.json"}})
	bs, err := c.ListBuilds(ctx, "OPS-DEPLOY", provider.ListOptions{Unfinished: true})
	require.NoError(t, err)
	assert.Equal(t, "true", rec.all()[0].URL.Query().Get("includeAllStates"))
	require.Len(t, bs, 3)
	assert.Equal(t, []provider.State{provider.StateQueued, provider.StateRunning, provider.StateSuccess},
		[]provider.State{bs[0].State, bs[1].State, bs[2].State})
	assert.Equal(t, "Child of PROJ-API-142", bs[1].Reason)

	_, err = c.ListBuilds(ctx, "OPS-DEPLOY", provider.ListOptions{})
	require.NoError(t, err)
	assert.False(t, rec.all()[1].URL.Query().Has("includeAllStates"))
}

func TestGetBuildReadsTheParentFromMetadata(t *testing.T) {
	// A restart rewrites the reason to "Rebuilt by ..."; the parent is still
	// in the build's metadata.
	c, rec := newTestServer(t, map[string]*route{"GET /rest/api/latest/result/OPS-DEPLOY-88": {fixture: "result_rebuilt.json"}})
	b, err := c.GetBuild(ctx, "OPS-DEPLOY-88")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-API-142", b.Parent)
	assert.Contains(t, strings.Split(rec.all()[0].URL.Query().Get("expand"), ","), "metadata")
}

func TestCurrentUserNotFoundPointsAtTheURL(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{})
	_, err := c.CurrentUser(ctx)
	require.ErrorIs(t, err, errs.ErrNotFound)
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindBamboo, e.Kind)
	assert.Equal(t, "no Bamboo REST API at "+strings.TrimSuffix(c.URL(""), "/browse/"), e.What)
	assert.Contains(t, e.Try, "context path")
}
