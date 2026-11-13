package bamboo

import (
	"errors"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (pin by name): Bamboo's plan search finds plans by name; a server
// without it is learned once and never asked again. The answers are
// recordings of Bamboo 12.1.8 (testdata/recorded/plan_search_*.json); the one
// branch plan entry is hand-written, as the lab had no plan branch to record.

const searchPath = "GET /rest/api/latest/search/plans"

func TestSearchPlansReadsARecordedAnswer(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{searchPath: {fixture: "recorded/plan_search_two.json"}})
	saved := knownCaps(c, Capabilities{})
	plans, err := c.SearchPlans(ctx, "wait demo")
	require.NoError(t, err)
	require.Len(t, plans, 4, "a plan whose branchName is its default branch is still a plan")
	assert.Equal(t, "WAIT-HOLD", plans[0].Key)
	assert.Equal(t, "Hold", plans[0].Name)
	assert.Equal(t, "WAIT", plans[0].ProjectKey)
	assert.Equal(t, "WAIT-NOCAP", plans[1].Key)
	assert.Equal(t, "wait demo", rec.all()[0].URL.Query().Get("searchTerm"))
	assert.Equal(t, "yes", (*saved)[len(*saved)-1].PlanSearch)
}

func TestSearchPlansReadsAPlanWhoseDefaultBranchIsMain(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{searchPath: {fixture: "recorded/plan_search_one.json"}})
	knownCaps(c, Capabilities{})
	plans, err := c.SearchPlans(ctx, "Build Demo Image")
	require.NoError(t, err)
	require.Len(t, plans, 1)
	assert.Equal(t, "DEMO-BUILD", plans[0].Key)
	assert.Equal(t, "Build Demo Image", plans[0].Name)
}

func TestSearchPlansNoMatchIsEmptyNotRefused(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{searchPath: {fixture: "recorded/plan_search_none.json"}})
	saved := knownCaps(c, Capabilities{})
	plans, err := c.SearchPlans(ctx, "zzznomatch")
	require.NoError(t, err)
	assert.Empty(t, plans)
	assert.Equal(t, "yes", (*saved)[len(*saved)-1].PlanSearch)
}

func TestSearchPlansLeavesPlanBranchesOut(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{searchPath: {body: `{"searchResults":[
		{"id":"PROJ-BUILD","type":"chain","searchEntity":{"key":"PROJ-BUILD","planName":"Build","type":"chain","branchName":"main"}},
		{"id":"PROJ-BUILD0","type":"chain_branch","searchEntity":{"key":"PROJ-BUILD0","planName":"Build","type":"chain_branch","branchName":"feature-x"}}]}`}})
	knownCaps(c, Capabilities{})
	plans, err := c.SearchPlans(ctx, "build")
	require.NoError(t, err)
	require.Len(t, plans, 1, "the plan branch is not a plan to pin")
	assert.Equal(t, "PROJ-BUILD", plans[0].Key)
}

func TestSearchPlansOnAServerWithoutItIsLearnedOnce(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{searchPath: {status: 404, body: `{"message":"no"}`}})
	saved := knownCaps(c, Capabilities{})
	_, err := c.SearchPlans(ctx, "x")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Equal(t, "no", (*saved)[len(*saved)-1].PlanSearch)
	before := len(rec.all())
	_, err = c.SearchPlans(ctx, "x")
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Equal(t, before, len(rec.all()), "a learned no does not ask again")
}
