package bamboo

import (
	"errors"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-11 P2): say why a build waits - the queue in order, and a
// server without the queue list is learned once and cached.
func TestQueueListsBuildsInOrder(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"GET /rest/api/latest/queue": {fixture: "queue_list.json"}})
	saved := knownCaps(c, Capabilities{})
	q, err := c.Queue(ctx)
	require.NoError(t, err)
	require.Len(t, q, 3)
	assert.Equal(t, provider.QueueEntry{Key: "PROJ-PROV-9", PlanKey: "PROJ-PROV", Number: 9, Reason: "Code has changed"}, q[1])
	assert.Equal(t, "yes", (*saved)[len(*saved)-1].Queue)
}

func TestQueueLearnsOnceThatTheServerHasNoQueueList(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{"GET /rest/api/latest/queue": {status: 404, body: `{"message":"no"}`}})
	saved := knownCaps(c, Capabilities{})
	_, err := c.Queue(ctx)
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Equal(t, "no", (*saved)[len(*saved)-1].Queue)
	before := len(rec.all())
	_, err = c.Queue(ctx)
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Equal(t, before, len(rec.all()), "a learned no does not ask again")
}

func TestAgentsMapOnlineEnabledBusy(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"GET /rest/api/latest/agent": {fixture: "agents.json"}})
	as, err := c.Agents(ctx)
	require.NoError(t, err)
	require.Len(t, as, 3)
	assert.Equal(t, provider.Agent{ID: 2, Name: "linux-2", Type: "REMOTE", Online: true, Enabled: false, Busy: false}, as[1])
	assert.False(t, as[2].Online)
}
