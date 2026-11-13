package bamboo

import (
	"context"
	"net/url"
	"strings"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
)

type planSearchDTO struct {
	SearchResults []struct {
		SearchEntity struct {
			Key         string `json:"key"`
			PlanName    string `json:"planName"`
			ProjectName string `json:"projectName"`
			BranchName  string `json:"branchName"`
		} `json:"searchEntity"`
	} `json:"searchResults"`
}

// SearchPlans finds plans whose name matches term with Bamboo's plan search.
// Plan branches are left out: a pin names the plan. A server without the
// search is learned once, and answers ErrUnsupported afterwards.
func (c *Client) SearchPlans(ctx context.Context, term string) ([]provider.Plan, error) {
	unsupported := errs.Bamboof("this Bamboo does not search plans by name").Wrap(errs.ErrUnsupported)
	if c.fresh(ctx).PlanSearch == "no" {
		return nil, unsupported
	}
	var d planSearchDTO
	q := url.Values{"searchTerm": {term}, "fuzzy": {"true"}}
	if err := c.getJSON(ctx, api+"/search/plans", q, &d); err != nil {
		if isUnsupportedStatus(err) {
			c.learn(ctx, func(cp *Capabilities) { cp.PlanSearch = "no" })
			return nil, unsupported
		}
		return nil, err
	}
	if c.Capabilities().PlanSearch != "yes" {
		c.learn(ctx, func(cp *Capabilities) { cp.PlanSearch = "yes" })
	}
	var out []provider.Plan
	for _, r := range d.SearchResults {
		e := r.SearchEntity
		if e.Key == "" || e.BranchName != "" {
			continue
		}
		project, _, _ := strings.Cut(e.Key, "-")
		out = append(out, provider.Plan{Key: e.Key, Name: e.PlanName, ProjectKey: project, URL: c.URL(e.Key)})
	}
	return out, nil
}
