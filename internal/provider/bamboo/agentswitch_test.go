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
// to the agent's id. Bamboo 12.1.8 answers it 200 with the agent's new
// enabled value (testdata/recorded/agent_{disable,enable}.json).
func TestSetAgentEnabledPutsToTheAgentID(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{
		"PUT /rest/api/latest/agent/2/enable":  {fixture: "recorded/agent_enable.json"},
		"PUT /rest/api/latest/agent/1/disable": {fixture: "recorded/agent_disable.json"},
	})
	require.NoError(t, c.SetAgentEnabled(ctx, provider.Agent{ID: 2, Name: "linux-2"}, true))
	require.NoError(t, c.SetAgentEnabled(ctx, provider.Agent{ID: 1, Name: "linux-1"}, false))
	reqs := rec.all()
	require.Len(t, reqs, 2)
	assert.Equal(t, "/rest/api/latest/agent/2/enable", reqs[0].URL.Path)
	assert.Equal(t, http.MethodPut, reqs[1].Method)
}

// Intent (issue 231): Bamboo 12.1.8 refuses the switch with a 403 naming the
// permission it needs, for a user without it and for an administrator whose
// token is limited to read or trigger
// (testdata/recorded/agent_switch_forbidden.json). bam passes that reason
// on, names the agent, and does not claim only administrators can do it.
func TestSetAgentEnabledRefusalPassesOnBambooReason(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"PUT /rest/api/latest/agent/1/disable": {status: 403, fixture: "recorded/agent_switch_forbidden.json"}})
	err := c.SetAgentEnabled(ctx, provider.Agent{ID: 1, Name: "linux-1"}, false)
	require.Error(t, err)
	assert.Equal(t, errs.KindAuth, errs.KindOf(err))
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "403 from "+c.base.Host+": Bamboo refused to disable linux-1", e.What)
	assert.Equal(t, "Bamboo says: You don't have global restricted administration permission", e.Why)
	assert.Contains(t, e.Try, "restricted administrator")
	assert.Contains(t, e.Try, "not one limited to read or trigger")
}

// A 404 on the PUT follows a GET /agent that just listed the agent, so it
// means the server has no such REST resource, not that the agent is gone.
// The bodies are Bamboo 12.1.8's own answers to a PUT on a path under
// /agent/{id} it does not serve (404) and to the switch sent as POST (405).
func TestSetAgentEnabledNotFoundAndNotAllowedAreUnsupported(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"PUT /rest/api/latest/agent/1/disable": {status: 404, body: `{"message":"HTTP 404 Not Found","status-code":404,"sub-code":-1}`},
		"PUT /rest/api/latest/agent/2/disable": {status: 405},
	})
	err := c.SetAgentEnabled(ctx, provider.Agent{ID: 1, Name: "linux-1"}, false)
	assert.ErrorIs(t, err, errs.ErrUnsupported)
	assert.Contains(t, err.Error(), "cannot disable an agent over REST")
	assert.NotContains(t, err.Error(), "gone")
	err = c.SetAgentEnabled(ctx, provider.Agent{ID: 2, Name: "linux-2"}, false)
	assert.ErrorIs(t, err, errs.ErrUnsupported)
}
