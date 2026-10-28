package main

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/bamboo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testStub serves the real fixtures over a real HTTP listener, with a clock
// the test moves by hand, and returns a bam client pointed at it.
func testStub(t *testing.T) (*stub, *bamboo.Client) {
	t.Helper()
	s := newStub(filepath.Join("..", "..", "internal", "provider", "bamboo", "testdata"))
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)

	c, err := bamboo.New(bamboo.Options{BaseURL: srv.URL, Token: "devtoken", HTTP: srv.Client()})
	require.NoError(t, err)
	return s, c
}

// advance moves the stub's clock, which is what ages a simulated build.
func advance(s *stub, d time.Duration) {
	at := s.now().Add(d)
	s.now = func() time.Time { return at }
}

// TestClientReadsEveryPanelTheUINeeds is the point of the stub: bam's own
// client, not a hand-written request, has to understand what it serves.
func TestClientReadsEveryPanelTheUINeeds(t *testing.T) {
	_, c := testStub(t)
	ctx := context.Background()

	user, err := c.CurrentUser(ctx)
	require.NoError(t, err)
	assert.Equal(t, "jdoe", user.Name)

	info, err := c.ServerInfo(ctx)
	require.NoError(t, err)
	assert.Equal(t, "9.6.2", info.Version)

	projects, err := c.ListProjects(ctx)
	require.NoError(t, err)
	require.Len(t, projects, 2)

	plans, err := c.ListPlans(ctx, "OPS")
	require.NoError(t, err)
	require.NotEmpty(t, plans)
	assert.Equal(t, "OPS-BUILD", plans[0].Key, "a fixture plan is retargeted to the project asked for")
	require.NotNil(t, plans[0].LastBuild)

	builds, err := c.ListBuilds(ctx, "OPS-BUILD", provider.ListOptions{Limit: 10})
	require.NoError(t, err)
	require.Len(t, builds, 3)
	assert.Equal(t, provider.StateSuccess, builds[0].State)
	assert.Equal(t, provider.StateFailed, builds[1].State)

	build, err := c.GetBuild(ctx, "OPS-BUILD-481")
	require.NoError(t, err)
	require.NotEmpty(t, build.Stages)

	vars, err := c.ListVariables(ctx, "OPS-BUILD")
	require.NoError(t, err)
	require.Len(t, vars, 3)
	assert.True(t, vars[2].Masked, "a secret stays masked in the fixture")
}

// TestTriggeredBuildRunsToSuccess covers what no fixture can: a build that
// changes state while bam watches it.
func TestTriggeredBuildRunsToSuccess(t *testing.T) {
	s, c := testStub(t)
	ctx := context.Background()

	b, err := c.Trigger(ctx, provider.TriggerRequest{PlanKey: "PROJ-BUILD", Variables: map[string]string{"cluster_type": "k8s"}})
	require.NoError(t, err)
	require.Equal(t, "PROJ-BUILD-901", b.Key)
	assert.Equal(t, provider.StateQueued, b.State)

	got, err := c.GetBuild(ctx, b.Key)
	require.NoError(t, err)
	assert.Equal(t, provider.StateQueued, got.State)

	advance(s, s.queued)
	got, err = c.GetBuild(ctx, b.Key)
	require.NoError(t, err)
	assert.Equal(t, provider.StateRunning, got.State)

	advance(s, s.running)
	got, err = c.GetBuild(ctx, b.Key)
	require.NoError(t, err)
	assert.Equal(t, provider.StateSuccess, got.State)
	assert.False(t, got.FinishedAt.IsZero())

	// A finished build keeps its duration: watching it an hour later must
	// not show it an hour longer.
	advance(s, time.Hour)
	later, err := c.GetBuild(ctx, b.Key)
	require.NoError(t, err)
	assert.Equal(t, got.FinishedAt, later.FinishedAt)
	assert.Equal(t, got.Duration, later.Duration)
}

// TestTriggerWithARevisionReportsCustomRevisionBuild: bam.Client.Trigger
// treats any trigger reason other than "Custom revision build" as Bamboo
// having ignored the revision and stops the build it just queued. The stub
// must answer the reason a real Bamboo Data Center gives when customRevision
// is honored, or `bam run --revision` against the local stub always fails.
func TestTriggerWithARevisionReportsCustomRevisionBuild(t *testing.T) {
	_, c := testStub(t)
	ctx := context.Background()

	b, err := c.Trigger(ctx, provider.TriggerRequest{PlanKey: "PROJ-BUILD", Revision: "abc1234"})
	require.NoError(t, err)
	assert.Equal(t, provider.StateQueued, b.State)
}

func TestCancelledBuildReportsStopped(t *testing.T) {
	s, c := testStub(t)
	ctx := context.Background()

	b, err := c.Trigger(ctx, provider.TriggerRequest{PlanKey: "PROJ-BUILD"})
	require.NoError(t, err)
	require.NoError(t, c.StopBuild(ctx, b.Key))

	got, err := c.GetBuild(ctx, b.Key)
	require.NoError(t, err)
	assert.Equal(t, provider.StateStopped, got.State)

	advance(s, time.Hour)
	later, err := c.GetBuild(ctx, b.Key)
	require.NoError(t, err)
	assert.Equal(t, got.Duration, later.Duration)
}

func TestLogsComeFromTheFixture(t *testing.T) {
	_, c := testStub(t)
	chunk, err := c.FetchLog(context.Background(), "PROJ-BUILD12-INT-44", provider.LogOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, chunk.Lines)
	assert.Contains(t, chunk.Lines[1], "FAIL: TestLogin")
}

// TestNoTokenIsRefused: bam's own 401 handling is what tells a missing token
// from a broken server, so the stub must not answer an unauthenticated call.
func TestNoTokenIsRefused(t *testing.T) {
	s := newStub(filepath.Join("..", "..", "internal", "provider", "bamboo", "testdata"))
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + api + "/info")
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assert.Equal(t, 401, resp.StatusCode)
}

func TestRunningBuildReportsProgress(t *testing.T) {
	s, c := testStub(t)
	ctx := context.Background()
	b, err := c.Trigger(ctx, provider.TriggerRequest{PlanKey: "PROJ-BUILD"})
	require.NoError(t, err)

	advance(s, s.queued+s.running/2)
	p, err := c.BuildProgress(ctx, b.Key)
	require.NoError(t, err)
	assert.True(t, p.Valid)
	assert.Equal(t, s.running, p.Average)
	assert.InDelta(t, 0.5, p.Percent, 0.1)

	advance(s, s.running)
	done, err := c.BuildProgress(ctx, b.Key)
	require.NoError(t, err)
	assert.False(t, done.Valid, "a finished build has no estimate")
}

// TestFailedTestsComePerJobLikeBamboo12: a build's result carries no tests
// and its failed job's result does, as on Bamboo 12, so trying bam locally
// takes the same path as a real server.
func TestFailedTestsComePerJobLikeBamboo12(t *testing.T) {
	_, c := testStub(t)
	b, err := c.GetBuild(context.Background(), "PROJ-BUILD12-44")
	require.NoError(t, err)
	assert.Equal(t, []string{"app.ApiTest.parses_config", "app.ApiTest.talks_to_bamboo"}, b.FailedTests)
	assert.Equal(t, "jobs", c.Capabilities().FailedTestsFrom)
}

// TestBuildDetailIsTheBuildAskedFor keeps a retargeted build from reading as
// the recorded one: its number, plan and job keys follow the key asked for.
func TestBuildDetailIsTheBuildAskedFor(t *testing.T) {
	_, c := testStub(t)
	ctx := context.Background()

	b, err := c.GetBuild(ctx, "OPS-BUILD-481")
	require.NoError(t, err)
	assert.Equal(t, 481, b.Number)
	assert.Equal(t, "OPS-BUILD", b.PlanKey)
	assert.Empty(t, b.Branch, "only the recorded branch plan is a branch")
	require.NotEmpty(t, b.Stages)
	assert.Equal(t, "OPS-BUILD-JOB1-481", b.Stages[0].Jobs[0].Key)

	b, err = c.GetBuild(ctx, "PROJ-BUILD12-7")
	require.NoError(t, err)
	assert.Equal(t, 7, b.Number)
	assert.Equal(t, "develop", b.Branch)
	assert.Equal(t, "PROJ-BUILD12-INT-7", b.Stages[1].Jobs[1].Key)
}
