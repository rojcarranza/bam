package bamboo

import (
	"context"
	"net/url"
	"strings"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
)

// planSearchMax is how many results one plan search asks for.
const planSearchMax = "100"

type planSearchDTO struct {
	MayHaveMore   bool `json:"mayHaveMore"`
	SearchResults []struct {
		Type         string `json:"type"`
		SearchEntity struct {
			Type        string `json:"type"`
			Key         string `json:"key"`
			PlanName    string `json:"planName"`
			ProjectName string `json:"projectName"`
			BranchName  string `json:"branchName"`
		} `json:"searchEntity"`
	} `json:"searchResults"`
}

// SearchPlans finds plans whose name matches term with Bamboo's plan search.
// Plan branches are left out: a pin names the plan. Bamboo 12 answers a
// plan's own default repository branch in branchName ("main", "Default"), so
// a branch plan is told by its entry type, not by that field. A server without the
// search is learned once, and answers ErrUnsupported afterwards. complete is
// false when Bamboo says more results may follow the one page read.
func (c *Client) SearchPlans(ctx context.Context, term string) (plans []provider.Plan, complete bool, err error) {
	unsupported := errs.Bamboof("this Bamboo does not search plans by name").Wrap(errs.ErrUnsupported)
	if c.fresh(ctx).PlanSearch == "no" {
		return nil, false, unsupported
	}
	var d planSearchDTO
	q := url.Values{"searchTerm": {term}, "fuzzy": {"true"}, "max-result": {planSearchMax}}
	if err := c.getJSON(ctx, api+"/search/plans", q, &d); err != nil {
		if isUnsupportedStatus(err) {
			c.learn(ctx, func(cp *Capabilities) { cp.PlanSearch = "no" })
			return nil, false, unsupported
		}
		return nil, false, err
	}
	if c.Capabilities().PlanSearch != "yes" {
		c.learn(ctx, func(cp *Capabilities) { cp.PlanSearch = "yes" })
	}
	for _, r := range d.SearchResults {
		e := r.SearchEntity
		if e.Key == "" || isBranchEntry(r.Type) || isBranchEntry(e.Type) {
			continue
		}
		project, _, _ := strings.Cut(e.Key, "-")
		plans = append(plans, provider.Plan{Key: e.Key, Name: e.PlanName, ProjectKey: project, URL: c.URL(e.Key)})
	}
	return plans, !d.MayHaveMore, nil
}

// isBranchEntry says whether a search entry type (Bamboo's "chain_branch")
// names a plan branch.
func isBranchEntry(t string) bool { return strings.Contains(strings.ToLower(t), "branch") }
