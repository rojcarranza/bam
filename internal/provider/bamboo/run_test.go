package bamboo

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTriggerSendsVariablesInFormBody(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{"POST /rest/api/latest/queue/PROJ-BUILD": {fixture: "queue.json"}})
	b, err := c.Trigger(ctx, provider.TriggerRequest{
		PlanKey:   "PROJ-BUILD",
		Variables: map[string]string{"env": "staging", "db_password": "hunter2"},
		Secret:    map[string]bool{"db_password": true},
	})
	require.NoError(t, err)
	assert.Equal(t, provider.Build{Key: "PROJ-BUILD-45", URL: c.URL("PROJ-BUILD-45"), PlanKey: "PROJ-BUILD", Number: 45,
		State: provider.StateQueued, Reason: "Manual build"}, b)

	r := rec.all()[0]
	assert.Equal(t, "true", r.URL.Query().Get("executeAllStages"))
	assert.Empty(t, r.URL.Query().Get("bamboo.variable.env"), "variables stay out of the URL")
	assert.Contains(t, rec.form[0], "bamboo.variable.env=staging")
	assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
}

func TestTriggerRejectedNamesThePlan(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"POST /rest/api/latest/queue/PROJ-BUILD": {status: 400, body: `{"message":"Plan PROJ-BUILD is disabled"}`}})
	_, err := c.Trigger(ctx, provider.TriggerRequest{PlanKey: "PROJ-BUILD"})
	require.Error(t, err)
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "could not start build of PROJ-BUILD", e.What)
	assert.Equal(t, "Plan PROJ-BUILD is disabled", e.Why)
}

func TestStopBuild(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{"DELETE /rest/api/latest/queue/PROJ-BUILD-45": {status: 204}})
	saved := knownCaps(c, Capabilities{})
	require.NoError(t, c.StopBuild(ctx, "PROJ-BUILD-45"))
	assert.Equal(t, "DELETE", rec.all()[0].Method)
	assert.Equal(t, "yes", (*saved)[len(*saved)-1].Stop)
}

func TestStopBuildUnsupported(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"DELETE /rest/api/latest/queue/PROJ-BUILD-45": {status: 405}})
	saved := knownCaps(c, Capabilities{})
	err := c.StopBuild(ctx, "PROJ-BUILD-45")
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Equal(t, "no", (*saved)[len(*saved)-1].Stop)
}

func TestFetchLogFromEntries(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/PROJ-BUILD12-INT-44?expand=logEntries": {fixture: "log_entries.json"},
	})
	saved := knownCaps(c, Capabilities{})
	chunk, err := c.FetchLog(ctx, "PROJ-BUILD12-INT-44", provider.LogOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"Starting integration tests", "FAIL: TestLogin <timeout>", "Failing task since return code of [go test] was 1"}, chunk.Lines)
	assert.Equal(t, 3, chunk.Next)
	assert.Equal(t, "entries", (*saved)[len(*saved)-1].Log)
}

func TestFetchLogFallsBackToDownload(t *testing.T) {
	raw := "simple\t12-Sep-2026 11:00:00\tStarting integration tests\r\n" +
		"error\t12-Sep-2026 11:00:05\tFAIL: TestLogin <timeout>\n" +
		"simple\t12-Sep-2026 11:00:06\tFailing task\n"
	c, rec := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/PROJ-BUILD12-INT-44":                   {status: 400, body: `{"message":"bad expand"}`},
		"GET /download/PROJ-BUILD12-INT/build_logs/PROJ-BUILD12-INT-44.log": {body: raw},
	})
	saved := knownCaps(c, Capabilities{})
	chunk, err := c.FetchLog(ctx, "PROJ-BUILD12-INT-44", provider.LogOptions{Offset: 1})
	require.NoError(t, err)
	assert.Equal(t, []string{"FAIL: TestLogin <timeout>", "Failing task"}, chunk.Lines)
	assert.Equal(t, 3, chunk.Next)
	assert.Equal(t, "download", (*saved)[len(*saved)-1].Log)

	_, err = c.FetchLog(ctx, "PROJ-BUILD12-INT-44", provider.LogOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, countPath(rec, "/rest/api/latest/result/PROJ-BUILD12-INT-44"), "entries path not retried once download is learned")
	assert.Empty(t, rec.all()[1].Header.Get("Accept"), "raw download does not ask for JSON")
}

func TestFetchLogUnknownJob(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{})
	knownCaps(c, Capabilities{})
	_, err := c.FetchLog(ctx, "PROJ-BUILD12-NOPE-44", provider.LogOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "job PROJ-BUILD12-NOPE-44 not found")
}

// TestRecordedLogEntriesDecode runs a recording of the log entries endpoint
// through FetchLog. It skips when nothing is recorded.
func TestRecordedLogEntriesDecode(t *testing.T) {
	const f = "recorded/log_entries.json"
	if _, err := os.Stat(filepath.Join("testdata", f)); err != nil {
		t.Skipf("%s not recorded", f)
	}
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/REC-PLAN-JOB1-1?expand=logEntries": {fixture: f},
	})
	knownCaps(c, Capabilities{})
	chunk, err := c.FetchLog(ctx, "REC-PLAN-JOB1-1", provider.LogOptions{})
	require.NoError(t, err)
	assert.NotEmpty(t, chunk.Lines)
}

// TestRecordedLogDownloadDecode runs a recording of the raw log download
// through FetchLog. It skips when nothing is recorded.
func TestRecordedLogDownloadDecode(t *testing.T) {
	const f = "recorded/log_download.log"
	if _, err := os.Stat(filepath.Join("testdata", f)); err != nil {
		t.Skipf("%s not recorded", f)
	}
	raw, err := os.ReadFile(filepath.Join("testdata", f))
	require.NoError(t, err)
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/REC-PLAN-JOB1-1":                {status: 400, body: `{"message":"bad expand"}`},
		"GET /download/REC-PLAN-JOB1/build_logs/REC-PLAN-JOB1-1.log": {body: string(raw)},
	})
	knownCaps(c, Capabilities{})
	chunk, err := c.FetchLog(ctx, "REC-PLAN-JOB1-1", provider.LogOptions{})
	require.NoError(t, err)
	assert.NotEmpty(t, chunk.Lines)
}

// TestRecordedStoppedState checks that a recorded stopped build maps to
// provider.StateStopped. It skips when nothing is recorded.
func TestRecordedStoppedState(t *testing.T) {
	const f = "recorded/result_stopped.json"
	if _, err := os.Stat(filepath.Join("testdata", f)); err != nil {
		t.Skipf("%s not recorded", f)
	}
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/REC-PLAN-1?expand=" + buildExpand: {fixture: f},
	})
	b, err := c.GetBuild(ctx, "REC-PLAN-1")
	require.NoError(t, err)
	assert.Equal(t, provider.StateStopped, b.State)
}

// Bamboo Data Center rejects a plan-level build key on the queue endpoint
// ("Plan PROJ-BUILD is not of type ImmutableJob"), so a stop falls back to
// the job result keys of that build.
func TestStopBuildFallsBackToJobKeys(t *testing.T) {
	detail := `{"key":"PROJ-BUILD-45","lifeCycleState":"InProgress","state":"Unknown","stages":{"stage":[
		{"name":"Build","lifeCycleState":"Finished","state":"Successful","results":{"result":[
			{"buildResultKey":"PROJ-BUILD-JOB0-45","lifeCycleState":"Finished","state":"Successful"}]}},
		{"name":"Test","lifeCycleState":"InProgress","state":"Unknown","results":{"result":[
			{"buildResultKey":"PROJ-BUILD-JOB1-45","lifeCycleState":"InProgress","state":"Unknown"}]}}]}}`
	c, rec := newTestServer(t, map[string]*route{
		"DELETE /rest/api/latest/queue/PROJ-BUILD-45":                     {status: 404, body: `{"message":"Plan PROJ-BUILD is not of type com.atlassian.bamboo.plan.cache.ImmutableJob"}`},
		"GET /rest/api/latest/result/PROJ-BUILD-45?expand=" + buildExpand: {body: detail},
		"DELETE /rest/api/latest/queue/PROJ-BUILD-JOB1-45":                {status: 204},
	})
	saved := knownCaps(c, Capabilities{})

	require.NoError(t, c.StopBuild(ctx, "PROJ-BUILD-45"))

	var deleted []string
	for _, r := range rec.all() {
		if r.Method == "DELETE" {
			deleted = append(deleted, r.URL.Path)
		}
	}
	assert.Equal(t, []string{"/rest/api/latest/queue/PROJ-BUILD-45", "/rest/api/latest/queue/PROJ-BUILD-JOB1-45"}, deleted,
		"a finished job is left alone")
	assert.Equal(t, "yes", (*saved)[len(*saved)-1].Stop)
}

func TestTriggerSendsTheRevisionInTheQuery(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{"POST /rest/api/latest/queue/PROJ-BUILD": {fixture: "queue_revision.json"}})
	b, err := c.Trigger(ctx, provider.TriggerRequest{PlanKey: "PROJ-BUILD", Revision: "abc1234",
		Variables: map[string]string{"env": "staging"}})
	require.NoError(t, err)
	assert.Equal(t, "PROJ-BUILD-46", b.Key)

	r := rec.all()[0]
	assert.Equal(t, "abc1234", r.URL.Query().Get("customRevision"))
	assert.Equal(t, "true", r.URL.Query().Get("executeAllStages"))
	assert.NotContains(t, rec.form[0], "customRevision", "the revision is a query parameter, not a form field")
	assert.Contains(t, rec.form[0], "bamboo.variable.env=staging")
}

func TestTriggerWithoutRevisionSendsNone(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{"POST /rest/api/latest/queue/PROJ-BUILD": {fixture: "queue.json"}})
	_, err := c.Trigger(ctx, provider.TriggerRequest{PlanKey: "PROJ-BUILD"})
	require.NoError(t, err)
	_, has := rec.all()[0].URL.Query()["customRevision"]
	assert.False(t, has)
}

// TestTriggerSendsTheRevisionVerbatim: the value is not checked or trimmed
// by bam.
func TestTriggerSendsTheRevisionVerbatim(t *testing.T) {
	for _, rev := range []string{" abc1234", "v1.2", "main", "a&b=c"} {
		c, rec := newTestServer(t, map[string]*route{"POST /rest/api/latest/queue/PROJ-BUILD": {fixture: "queue_revision.json"}})
		_, err := c.Trigger(ctx, provider.TriggerRequest{PlanKey: "PROJ-BUILD", Revision: rev})
		require.NoError(t, err, rev)
		assert.Equal(t, rev, rec.all()[0].URL.Query().Get("customRevision"), rev)
	}
}

// A server that does not know customRevision ignores it and answers with an
// ordinary trigger reason; the build is queued at the newest commit.
func TestTriggerReportsAnIgnoredRevision(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"POST /rest/api/latest/queue/PROJ-BUILD": {fixture: "queue.json"}})
	b, err := c.Trigger(ctx, provider.TriggerRequest{PlanKey: "PROJ-BUILD", Revision: "abc1234"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindBamboo, e.Kind)
	assert.Equal(t, "Bamboo ignored the revision", e.What)
	assert.Equal(t, "PROJ-BUILD-45", b.Key, "the queued build comes back so the caller can stop it")
	assert.Equal(t, provider.StateQueued, b.State)
}

func TestRecordedRevisionQueueAnswer(t *testing.T) {
	for _, f := range []string{"recorded/queue_revision.json", "recorded/queue_revision_short.json", "recorded/queue_revision_invalid.json"} {
		t.Run(f, func(t *testing.T) {
			if _, err := os.Stat(filepath.Join("testdata", f)); err != nil {
				t.Skipf("%s not recorded", f)
			}
			c, _ := newTestServer(t, map[string]*route{"POST /rest/api/latest/queue/REC-PLAN": {fixture: f}})
			b, err := c.Trigger(ctx, provider.TriggerRequest{PlanKey: "REC-PLAN", Revision: "abc1234"})
			require.NoError(t, err, "%s: Bamboo 12.1.8 took the revision", f)
			assert.NotEmpty(t, b.Key, f)
		})
	}
}

func TestRecordedInvalidRevisionIsNotBuilt(t *testing.T) {
	for _, f := range []string{"result_revision_not_built.json", "recorded/result_revision_invalid.json"} {
		t.Run(f, func(t *testing.T) {
			if _, err := os.Stat(filepath.Join("testdata", f)); err != nil {
				t.Skipf("%s not recorded", f)
			}
			c, _ := newTestServer(t, map[string]*route{
				"GET /rest/api/latest/result/REC-PLAN-1?expand=" + buildExpand: {fixture: f},
			})
			b, err := c.GetBuild(ctx, "REC-PLAN-1")
			require.NoError(t, err, f)
			assert.Equal(t, provider.StateNotBuilt, b.State, f)
			assert.Empty(t, b.Revisions, f)
			assert.Equal(t, "", b.Revision, f)
		})
	}
}

func TestStopBuildWithNoStoppableJobReportsNotFound(t *testing.T) {
	detail := `{"key":"PROJ-BUILD-45","lifeCycleState":"Finished","state":"Successful","stages":{"stage":[
		{"name":"Build","lifeCycleState":"Finished","state":"Successful","results":{"result":[
			{"buildResultKey":"PROJ-BUILD-JOB0-45","lifeCycleState":"Finished","state":"Successful"}]}}]}}`
	c, _ := newTestServer(t, map[string]*route{
		"DELETE /rest/api/latest/queue/PROJ-BUILD-45":                     {status: 404, body: `{"message":"not of type com.atlassian.bamboo.plan.cache.ImmutableJob"}`},
		"GET /rest/api/latest/result/PROJ-BUILD-45?expand=" + buildExpand: {body: detail},
	})
	knownCaps(c, Capabilities{})

	err := c.StopBuild(ctx, "PROJ-BUILD-45")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "queued or running build PROJ-BUILD-45")
}

func TestBuildCarriesTheDefaultRepositoryRevision(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/PROJ-BUILD-46?expand=" + buildExpand: {fixture: "result_revision.json"},
	})
	b, err := c.GetBuild(ctx, "PROJ-BUILD-46")
	require.NoError(t, err)
	assert.Equal(t, "9b30b9aba03a4f45881629b03531006b89c1e0e7", b.Revision)
}

func TestBuildWithoutARevisionHasNone(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/PROJ-BUILD-47?expand=" + buildExpand: {fixture: "result_revision_not_built.json"},
	})
	b, err := c.GetBuild(ctx, "PROJ-BUILD-47")
	require.NoError(t, err)
	assert.Equal(t, "", b.Revision)
}

// TestRecordedBuildRevision: Bamboo 12.1.8 reports the default repository's
// commit at the top level, equal to the first vcsRevisions entry.
func TestRecordedBuildRevision(t *testing.T) {
	for _, f := range []string{"result_revision.json", "recorded/result_revision.json", "recorded/result_revision_short.json", "recorded/result_detail.json"} {
		t.Run(f, func(t *testing.T) {
			if _, err := os.Stat(filepath.Join("testdata", f)); err != nil {
				t.Skipf("%s not recorded", f)
			}
			c, _ := newTestServer(t, map[string]*route{
				"GET /rest/api/latest/result/REC-PLAN-1?expand=" + buildExpand: {fixture: f},
			})
			b, err := c.GetBuild(ctx, "REC-PLAN-1")
			require.NoError(t, err, f)
			require.NotEmpty(t, b.Revisions, f)
			assert.Equal(t, b.Revisions[0].Revision, b.Revision, f)
		})
	}
}

func TestRestartBuild(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{"PUT /rest/api/latest/queue/PROJ-BUILD-45": {fixture: "queue_restart.json"}})
	saved := knownCaps(c, Capabilities{})
	b, err := c.RestartBuild(ctx, "PROJ-BUILD-45")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-BUILD-45", b.Key)
	assert.Equal(t, 45, b.Number)
	assert.Equal(t, provider.StateQueued, b.State)
	r := rec.all()[0]
	assert.Equal(t, http.MethodPut, r.Method)
	assert.Equal(t, "true", r.URL.Query().Get("executeAllStages"))
	assert.Equal(t, "yes", (*saved)[len(*saved)-1].Rerun)
}

func TestRestartBuildUnsupported(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"PUT /rest/api/latest/queue/PROJ-BUILD-45": {status: 405}})
	saved := knownCaps(c, Capabilities{})
	_, err := c.RestartBuild(ctx, "PROJ-BUILD-45")
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Contains(t, err.Error(), "cannot restart a build over REST")
	assert.Equal(t, "no", (*saved)[len(*saved)-1].Rerun)
}

func TestRestartBuildKnownUnsupportedSendsNothing(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{})
	knownCaps(c, Capabilities{Rerun: "no"})
	_, err := c.RestartBuild(ctx, "PROJ-BUILD-45")
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Empty(t, rec.all())
}

func TestRestartBuildRefusedNamesTheBuild(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"PUT /rest/api/latest/queue/PROJ-BUILD-45": {status: 400,
		body: `{"message":"Build PROJ-BUILD-45 cannot be restarted"}`}})
	knownCaps(c, Capabilities{})
	_, err := c.RestartBuild(ctx, "PROJ-BUILD-45")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindBamboo, e.Kind)
	assert.Equal(t, "Bamboo would not restart PROJ-BUILD-45", e.What)
	assert.Equal(t, "Build PROJ-BUILD-45 cannot be restarted", e.Why)
	assert.False(t, errors.Is(err, errs.ErrUnsupported))
}

func TestRestartBuildOfAMissingBuild(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"PUT /rest/api/latest/queue/PROJ-BUILD-45": {status: 404, body: `{"message":"not found"}`}})
	knownCaps(c, Capabilities{})
	_, err := c.RestartBuild(ctx, "PROJ-BUILD-45")
	assert.True(t, errors.Is(err, errs.ErrNotFound))
}

// TestRecordedRestart checks the hand-written shapes against what the
// recorder's -probe-rerun recorded. It skips until that probe has run.
func TestRecordedRestart(t *testing.T) {
	const answer = "recorded/queue_restart.json"
	if _, err := os.Stat(filepath.Join("testdata", answer)); err != nil {
		t.Skipf("%s not recorded", answer)
	}
	c, _ := newTestServer(t, map[string]*route{"PUT /rest/api/latest/queue/REC-PLAN-1": {fixture: answer}})
	knownCaps(c, Capabilities{})
	b, err := c.RestartBuild(ctx, "REC-PLAN-1")
	require.NoError(t, err)
	assert.NotEmpty(t, b.Key)

	results, _ := filepath.Glob(filepath.Join("testdata", "recorded", "result_restart_*.json"))
	for _, f := range results {
		rel, _ := filepath.Rel("testdata", f)
		c, _ := newTestServer(t, map[string]*route{"GET /rest/api/latest/result/REC-PLAN-1?expand=" + buildExpand: {fixture: rel}})
		b, err := c.GetBuild(ctx, "REC-PLAN-1")
		require.NoError(t, err, rel)
		assert.NotEmpty(t, b.State, rel)
	}
}

// Bamboo's restart answer is not recorded yet: any 2xx means the restart
// was accepted, whatever its body, and Service.Restart's poll confirms it.
func TestRestartBuildAcceptsAnAnswerWithoutABuild(t *testing.T) {
	for name, rt := range map[string]*route{
		"no content":   {status: 204},
		"other object": {body: `{"message":"restarted"}`},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := newTestServer(t, map[string]*route{"PUT /rest/api/latest/queue/PROJ-BUILD-45": rt})
			saved := knownCaps(c, Capabilities{})
			b, err := c.RestartBuild(ctx, "PROJ-BUILD-45")
			require.NoError(t, err)
			assert.Equal(t, "PROJ-BUILD-45", b.Key)
			assert.Equal(t, "PROJ-BUILD", b.PlanKey)
			assert.Equal(t, provider.StateQueued, b.State)
			assert.Equal(t, "yes", (*saved)[len(*saved)-1].Rerun)
		})
	}
}
