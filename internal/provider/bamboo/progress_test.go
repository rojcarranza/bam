package bamboo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildProgressMapsRunningStatus(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/status/PROJ-PLAN-44": {fixture: "status_running.json"},
	})
	knownCaps(c, Capabilities{Progress: "yes"})

	p, err := c.BuildProgress(context.Background(), "PROJ-PLAN-44")

	require.NoError(t, err)
	assert.Equal(t, provider.Progress{
		Valid:     true,
		Average:   3 * time.Minute,
		Elapsed:   99 * time.Second,
		Remaining: 81 * time.Second,
		Percent:   0.55,
		Stage:     "Deploy",
		Source:    "bamboo",
	}, p)
}

func TestBuildProgressIsEmptyForFinishedOrInvalid(t *testing.T) {
	for name, body := range map[string]string{
		"finished": `{"currentStage":"","finished":true,"progress":{"isValid":true,"averageBuildDuration":180000,"buildTime":180000,"percentageCompleted":1}}`,
		"invalid":  `{"currentStage":"Build","finished":false,"progress":{"isValid":false,"averageBuildDuration":0,"buildTime":3000}}`,
		"absent":   `{"currentStage":"Build","finished":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := newTestServer(t, map[string]*route{
				"GET /rest/api/latest/result/status/PROJ-PLAN-44": {body: body},
			})
			knownCaps(c, Capabilities{Progress: "yes"})

			p, err := c.BuildProgress(context.Background(), "PROJ-PLAN-44")

			require.NoError(t, err)
			assert.Equal(t, provider.Progress{}, p)
		})
	}
}

func TestBuildProgressFallsBackToElapsedOverAverage(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/status/PROJ-PLAN-44": {body: `{"finished":false,"progress":{"isValid":true,"averageBuildDuration":200000,"buildTime":50000}}`},
	})
	knownCaps(c, Capabilities{Progress: "yes"})

	p, err := c.BuildProgress(context.Background(), "PROJ-PLAN-44")

	require.NoError(t, err)
	assert.InDelta(t, 0.25, p.Percent, 0.0001)
}

func TestBuildProgressClampsOverrun(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/status/PROJ-PLAN-44": {body: `{"finished":false,"progress":{"isValid":true,"averageBuildDuration":60000,"buildTime":90000,"percentageCompleted":1.5}}`},
	})
	knownCaps(c, Capabilities{Progress: "yes"})

	p, err := c.BuildProgress(context.Background(), "PROJ-PLAN-44")

	require.NoError(t, err)
	assert.Equal(t, 1.0, p.Percent)
	assert.Equal(t, time.Duration(0), p.Remaining)
}

func TestBuildProgressLearnsUnsupportedOn404(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/status/PROJ-PLAN-44": {status: 404, body: `{"message":"no"}`},
	})
	saved := knownCaps(c, Capabilities{})

	_, err := c.BuildProgress(context.Background(), "PROJ-PLAN-44")

	require.Error(t, err)
	assert.True(t, errors.Is(err, errs.ErrUnsupported), "want ErrUnsupported, got %v", err)
	assert.Equal(t, "no", c.Capabilities().Progress)
	require.NotEmpty(t, *saved)
	assert.Equal(t, "no", (*saved)[len(*saved)-1].Progress)
	assert.Len(t, rec.all(), 1)
}

// notBuilding is Bamboo 9.6's answer for a result it is not building at
// that moment, such as one that just ended.
const notBuildingBody = `{"message":"Result PROJ-PLAN-44 not building.","status-code":404}`

func TestBuildProgressIsEmptyForAResultNotBuilding(t *testing.T) {
	for _, known := range []string{"", "yes"} {
		t.Run("known="+known, func(t *testing.T) {
			c, _ := newTestServer(t, map[string]*route{
				"GET /rest/api/latest/result/status/PROJ-PLAN-44": {status: 404, body: notBuildingBody},
			})
			knownCaps(c, Capabilities{Progress: known})

			p, err := c.BuildProgress(context.Background(), "PROJ-PLAN-44")

			require.NoError(t, err)
			assert.Equal(t, provider.Progress{}, p)
			assert.Equal(t, "yes", c.Capabilities().Progress, "the endpoint answered, so it is there")
		})
	}
}

func TestBuildProgressKeepsAKnownEndpointOnAnyBuild404(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/status/PROJ-PLAN-44": {status: 404, body: `{"message":"gone","status-code":404}`},
	})
	knownCaps(c, Capabilities{Progress: "yes"})

	p, err := c.BuildProgress(context.Background(), "PROJ-PLAN-44")

	require.NoError(t, err)
	assert.Equal(t, provider.Progress{}, p)
	assert.Equal(t, "yes", c.Capabilities().Progress, "one build's 404 does not take the endpoint away")
}

func TestBuildProgressSkipsRequestWhenKnownUnsupported(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{})
	knownCaps(c, Capabilities{Progress: "no"})

	_, err := c.BuildProgress(context.Background(), "PROJ-PLAN-44")

	assert.True(t, errors.Is(err, errs.ErrUnsupported), "want ErrUnsupported, got %v", err)
	assert.Empty(t, rec.all())
}

func TestBuildProgressKeepsCapabilityUnknownOnServerError(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/status/PROJ-PLAN-44": {status: 500, body: `{"message":"boom"}`},
	})
	knownCaps(c, Capabilities{})

	_, err := c.BuildProgress(context.Background(), "PROJ-PLAN-44")

	require.Error(t, err)
	assert.False(t, errors.Is(err, errs.ErrUnsupported), "500 must not mean unsupported")
	assert.Empty(t, c.Capabilities().Progress)
}

func TestBuildProgressLearnsSupportedOnFirstSuccess(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/status/PROJ-PLAN-44": {fixture: "status_running.json"},
	})
	saved := knownCaps(c, Capabilities{})

	_, err := c.BuildProgress(context.Background(), "PROJ-PLAN-44")

	require.NoError(t, err)
	assert.Equal(t, "yes", c.Capabilities().Progress)
	require.NotEmpty(t, *saved)
}

// TestRecordedStatusDecode runs a recording of the progress endpoint through
// BuildProgress, so the hand-written fixture cannot drift from the server's
// shape unnoticed. It skips when nothing is recorded.
func TestRecordedStatusDecode(t *testing.T) {
	const f = "recorded/status_running.json"
	if _, err := os.Stat(filepath.Join("testdata", f)); err != nil {
		t.Skipf("%s not recorded", f)
	}
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/status/REC-PLAN-1": {fixture: f},
	})
	knownCaps(c, Capabilities{})

	p, err := c.BuildProgress(context.Background(), "REC-PLAN-1")

	require.NoError(t, err)
	assert.True(t, p.Valid, "a recorded running build must carry an estimate")
	assert.Positive(t, p.Average)
}
