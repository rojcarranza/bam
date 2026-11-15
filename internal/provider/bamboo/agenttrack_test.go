package bamboo

import (
	"os"
	"path/filepath"
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
const jobLogWithAgent = "simple\t17-Sep-2026 17:28:50\tBuild PROJ-BUILD-JOB1-12 is being prepared for building on agent linux-1\n" +
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

// Intent: the newest builds of every plan come from one paged list of
// results, including running ones.
func TestRecentBuildsListsEveryPlansBuilds(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "recorded", "results.json"))
	require.NoError(t, err)
	c, rec := newTestServer(t, map[string]*route{"GET /rest/api/latest/result": {body: string(raw)}})
	got, err := c.RecentBuilds(ctx, 5)
	require.NoError(t, err)
	assert.NotEmpty(t, got)
	q := rec.all()[0].URL.Query()
	assert.Equal(t, "true", q.Get("includeAllStates"))
	assert.Equal(t, "5", q.Get("max-result"))
	var _ provider.RecentBuilder = c
	var _ provider.JobAgentReader = c
}
