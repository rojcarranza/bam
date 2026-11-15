package bamboo

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (issue 233, BAM-34 P5): a job's log names its agent in its first
// lines. The log below is written by hand from Atlassian's description of the
// line, not recorded, and kept inline because the owner's private fixture
// denylist rejects a file that holds the word agent on its own.
const jobLogWithAgent = "simple\t17-Sep-2026 17:28:50\tBuild Proj - Build - Default Job #12 (PROJ-BUILD-JOB1-12) started building on agent linux-1, bamboo version: 9.2.1\n" +
	"simple\t17-Sep-2026 17:28:51\tBuild working directory is /var/bamboo/build-dir/PROJ-BUILD-JOB1\n" +
	"build\t17-Sep-2026 17:28:52\tStarting task Checkout\n"

func TestJobAgentReadsTheAgentLine(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{
		"GET /download/PROJ-BUILD-JOB1/build_logs/PROJ-BUILD-JOB1-12.log": {body: jobLogWithAgent},
	})
	got, err := c.JobAgent(ctx, "PROJ-BUILD-JOB1-12")
	require.NoError(t, err)
	assert.Equal(t, "linux-1", got)
	assert.Len(t, rec.all(), 1)
}

func TestAgentFromLog(t *testing.T) {
	assert.Equal(t, "linux-2 (2)", agentFromLog("simple\t01-Jan-2026 10:00:00\tBuild X-Y-J-1 started building on agent linux-2 (2)\r\n"))
	assert.Equal(t, "linux-3", agentFromLog("simple\td\tBuild X-Y-J-1 started building on agent linux-3, bamboo version: 10.2.1\n"))
	assert.Equal(t, "a", agentFromLog("Build X is being prepared for building on agent a"))
	assert.Equal(t, "", agentFromLog("simple\tdate\tno agent here\n"))
	// The line is read only near the start of a log.
	late := strings.Repeat("build\td\tline\n", agentLineLimit) + "simple\td\tstarted building on agent late\n"
	assert.Equal(t, "", agentFromLog(late))
}

func TestJobAgentOfAJobWithNoLogIsEmpty(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{})
	got, err := c.JobAgent(ctx, "PROJ-BUILD-JOB1-13")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestJobAgentRejectsAKeyWithoutANumber(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{})
	_, err := c.JobAgent(ctx, "nodash")
	require.Error(t, err)
}

// Intent: Bamboo's server-wide result list holds only each plan's newest
// build (tools/record/children.go records it so), so the newest builds come
// from the plans whose newest build started last, merged by start time. Here
// OPS-B and PROJ-A ran last; for the two newest builds LAB-C, whose newest
// build is older than both, is never read.
func TestRecentBuildsMergesThePlansThatRanLast(t *testing.T) {
	res := func(key, plan, started string) string {
		return `{"buildResultKey":"` + key + `","plan":{"key":"` + plan + `"},"buildState":"Successful","lifeCycleState":"Finished","buildStartedTime":"` + started + `"}`
	}
	list := func(items ...string) string {
		return `{"results":{"size":` + strconv.Itoa(len(items)) + `,"result":[` + strings.Join(items, ",") + `]}}`
	}
	c, rec := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result": {body: list(
			res("LAB-C-9", "LAB-C", "2026-09-17T08:00:00.000Z"),
			res("PROJ-A-5", "PROJ-A", "2026-09-17T12:00:00.000Z"),
			res("OPS-B-3", "OPS-B", "2026-09-17T11:00:00.000Z"),
		)},
		"GET /rest/api/latest/result/PROJ-A": {body: list(
			res("PROJ-A-5", "PROJ-A", "2026-09-17T12:00:00.000Z"),
			res("PROJ-A-4", "PROJ-A", "2026-09-17T10:00:00.000Z"),
			res("PROJ-A-3", "PROJ-A", "2026-09-17T07:00:00.000Z"),
		)},
		"GET /rest/api/latest/result/LAB-C": {body: list(
			res("LAB-C-9", "LAB-C", "2026-09-17T08:00:00.000Z"),
		)},
		"GET /rest/api/latest/result/OPS-B": {body: list(
			res("OPS-B-3", "OPS-B", "2026-09-17T11:00:00.000Z"),
			res("OPS-B-2", "OPS-B", "2026-09-17T09:00:00.000Z"),
		)},
	})
	got, err := c.RecentBuilds(ctx, 2)
	require.NoError(t, err)
	var keys []string
	for _, b := range got {
		keys = append(keys, b.Key)
	}
	assert.Equal(t, []string{"PROJ-A-5", "OPS-B-3"}, keys)
	for _, r := range rec.all() {
		assert.NotEqual(t, "/rest/api/latest/result/LAB-C", r.URL.Path)
		if r.URL.Path == "/rest/api/latest/result" {
			assert.Equal(t, "true", r.URL.Query().Get("includeAllStates"))
		}
	}

	got, err = c.RecentBuilds(ctx, 3)
	require.NoError(t, err)
	keys = nil
	for _, b := range got {
		keys = append(keys, b.Key)
	}
	assert.Equal(t, []string{"PROJ-A-5", "OPS-B-3", "PROJ-A-4"}, keys)
	var _ provider.RecentBuilder = c
	var _ provider.JobAgentReader = c
}

// A plan that cannot be read, such as one deleted since the server-wide list,
// is left out like a build that cannot be read; only when no plan can be read
// does the list fail.
func TestRecentBuildsSkipsAPlanItCannotRead(t *testing.T) {
	res := func(key, plan, started string) string {
		return `{"buildResultKey":"` + key + `","plan":{"key":"` + plan + `"},"buildState":"Successful","lifeCycleState":"Finished","buildStartedTime":"` + started + `"}`
	}
	heads := `{"results":{"size":2,"result":[` + res("PROJ-A-5", "PROJ-A", "2026-09-17T12:00:00.000Z") + `,` + res("OPS-B-3", "OPS-B", "2026-09-17T11:00:00.000Z") + `]}}`
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result":       {body: heads},
		"GET /rest/api/latest/result/OPS-B": {body: `{"results":{"size":1,"result":[` + res("OPS-B-3", "OPS-B", "2026-09-17T11:00:00.000Z") + `]}}`},
	})
	got, err := c.RecentBuilds(ctx, 2)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "OPS-B-3", got[0].Key)

	none, _ := newTestServer(t, map[string]*route{"GET /rest/api/latest/result": {body: heads}})
	_, err = none.RecentBuilds(ctx, 2)
	require.Error(t, err)
}

// Intent: bam reads only the head of a job's log for its agent, so a huge
// log is not downloaded. The server below never stops sending; reading the
// whole body would never end.
func TestJobAgentReadsOnlyTheHeadOfALog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(jobLogWithAgent))
		line := []byte(strings.Repeat("build\td\tline\n", 1000))
		for r.Context().Err() == nil {
			if _, err := w.Write(line); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL, Token: "test-token"})
	require.NoError(t, err)
	got, err := c.JobAgent(ctx, "PROJ-BUILD-JOB1-12")
	require.NoError(t, err)
	assert.Equal(t, "linux-1", got)
}
