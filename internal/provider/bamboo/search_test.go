package bamboo

import (
	"errors"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (pin by name): Bamboo's plan search finds plans by name; a server
// without it is learned once and never asked again. The answer's shape is
// Bamboo's documented one (testdata/plan_search.json), not a recording.

const searchPath = "GET /rest/api/latest/search/plans"

func TestSearchPlansFindsPlansAndLeavesBranchesOut(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{searchPath: {fixture: "plan_search.json"}})
	saved := knownCaps(c, Capabilities{})
	plans, err := c.SearchPlans(ctx, "integrate next")
	require.NoError(t, err)
	require.Len(t, plans, 2, "the plan branch is not a plan to pin")
	assert.Equal(t, "PROJ-INTEG", plans[0].Key)
	assert.Equal(t, "Integrate for next release", plans[0].Name)
	assert.Equal(t, "PROJ", plans[0].ProjectKey)
	assert.Equal(t, "OPS-INTEG", plans[1].Key)
	assert.Equal(t, "integrate next", rec.all()[0].URL.Query().Get("searchTerm"))
	assert.Equal(t, "yes", (*saved)[len(*saved)-1].PlanSearch)
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
