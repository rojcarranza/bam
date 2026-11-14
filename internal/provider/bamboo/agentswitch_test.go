package bamboo

import (
	"net/http"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (issue 217, BAM-34 P6): enabling or disabling an agent is one PUT
// to the agent's id; a refusal says an administrator is needed.
func TestSetAgentEnabledPutsToTheAgentID(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{
		"PUT /rest/api/latest/agent/2/enable":  {status: 204},
		"PUT /rest/api/latest/agent/1/disable": {status: 204},
	})
	require.NoError(t, c.SetAgentEnabled(ctx, provider.Agent{ID: 2, Name: "linux-2"}, true))
	require.NoError(t, c.SetAgentEnabled(ctx, provider.Agent{ID: 1, Name: "linux-1"}, false))
	reqs := rec.all()
	require.Len(t, reqs, 2)
	assert.Equal(t, "/rest/api/latest/agent/2/enable", reqs[0].URL.Path)
	assert.Equal(t, http.MethodPut, reqs[1].Method)
}

func TestSetAgentEnabledRefusalNeedsAnAdministrator(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"PUT /rest/api/latest/agent/1/disable": {status: 403, body: `{"message":"forbidden"}`}})
	err := c.SetAgentEnabled(ctx, provider.Agent{ID: 1, Name: "linux-1"}, false)
	require.Error(t, err)
	assert.Equal(t, errs.KindAuth, errs.KindOf(err))
	assert.Contains(t, err.Error(), "administrator")
}

// A 404 on the PUT follows a GET /agent that just listed the agent, so it
// means the server has no such REST resource, not that the agent is gone.
func TestSetAgentEnabledNotFoundAndNotAllowedAreUnsupported(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"PUT /rest/api/latest/agent/1/disable": {status: 404, body: `{"message":"no"}`},
		"PUT /rest/api/latest/agent/2/disable": {status: 405, body: `{}`},
	})
	err := c.SetAgentEnabled(ctx, provider.Agent{ID: 1, Name: "linux-1"}, false)
	assert.ErrorIs(t, err, errs.ErrUnsupported)
	assert.Contains(t, err.Error(), "cannot disable an agent over REST")
	assert.NotContains(t, err.Error(), "gone")
	err = c.SetAgentEnabled(ctx, provider.Agent{ID: 2, Name: "linux-2"}, false)
	assert.ErrorIs(t, err, errs.ErrUnsupported)
}
