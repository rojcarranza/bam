package bamboo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"

	"github.com/r0jjames/bam-cli/internal/provider"
)

const api = "/rest/api/latest"

func (c *Client) CurrentUser(ctx context.Context) (provider.User, error) {
	var u userDTO
	if err := c.getJSON(ctx, api+"/currentUser", nil, &u); err != nil {
		return provider.User{}, err
	}
	return provider.User{Name: u.Name, FullName: u.FullName}, nil
}

func (c *Client) ServerInfo(ctx context.Context) (provider.ServerInfo, error) {
	var i infoDTO
	if err := c.getJSON(ctx, api+"/info", nil, &i); err != nil {
		return provider.ServerInfo{}, err
	}
	return provider.ServerInfo{Version: i.Version}, nil
}

func (c *Client) ListProjects(ctx context.Context) ([]provider.Project, error) {
	items, err := pageAll[projectDTO](ctx, c, api+"/project", nil, "projects", 0)
	if err != nil {
		return nil, err
	}
	out := make([]provider.Project, 0, len(items))
	for _, p := range items {
		out = append(out, provider.Project{Key: p.Key, Name: p.Name, URL: c.URL(p.Key)})
	}
	return out, nil
}

// ListPlans returns a project's plans with their latest build. Plans past the
// first page are fetched with Bamboo's expand range syntax plans[a:b].plan.
func (c *Client) ListPlans(ctx context.Context, project string) ([]provider.Plan, error) {
	var items []planDTO
	expand := "plans.plan"
	for {
		var res struct {
			Plans envelope `json:"plans"`
		}
		if err := c.getJSON(ctx, api+"/project/"+url.PathEscape(project), url.Values{"expand": {expand}}, &res); err != nil {
			return nil, c.notFoundAs(err, "project", project, "bam project list --all")
		}
		var page []planDTO
		if len(res.Plans.Items) > 0 {
			if err := json.Unmarshal(res.Plans.Items, &page); err != nil {
				return nil, err
			}
		}
		items = append(items, page...)
		if len(page) == 0 || len(items) >= res.Plans.Size {
			break
		}
		expand = fmt.Sprintf("plans[%d:%d].plan", len(items), len(items)+pageSize)
	}

	plans := make([]provider.Plan, len(items))
	for i, p := range items {
		name := p.ShortName
		if name == "" {
			name = p.Name
		}
		plans[i] = provider.Plan{Key: p.Key, Name: name, ProjectKey: project, URL: c.URL(p.Key)}
	}
	return plans, c.fillLastBuilds(ctx, plans)
}

// fillLastBuilds fetches the latest build of each plan, eight at a time.
func (c *Client) fillLastBuilds(ctx context.Context, plans []provider.Plan) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	sem := make(chan struct{}, 8)
	for i := range plans {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			builds, err := c.ListBuilds(ctx, plans[i].Key, provider.ListOptions{Limit: 1})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			if len(builds) > 0 {
				b := builds[0]
				plans[i].LastBuild = &provider.BuildSummary{Key: b.Key, Number: b.Number, State: b.State, FinishedAt: b.FinishedAt, Reason: b.Reason}
			}
		}(i)
	}
	wg.Wait()
	return firstErr
}

func (c *Client) GetPlan(ctx context.Context, key string) (provider.Plan, error) {
	var p planDTO
	if err := c.getJSON(ctx, api+"/plan/"+url.PathEscape(key), nil, &p); err != nil {
		return provider.Plan{}, c.notFoundAs(err, "plan", key, "bam plan list")
	}
	name := p.ShortName
	if name == "" {
		name = p.Name
	}
	return provider.Plan{Key: p.Key, Name: name, ProjectKey: p.ProjectKey, URL: c.URL(p.Key)}, nil
}

func (c *Client) ListBranches(ctx context.Context, key string) ([]provider.Branch, error) {
	items, err := pageAll[branchDTO](ctx, c, api+"/plan/"+url.PathEscape(key)+"/branch", nil, "branches", 0)
	if err != nil {
		return nil, c.notFoundAs(err, "plan", key, "bam plan list")
	}
	out := make([]provider.Branch, 0, len(items))
	for _, b := range items {
		out = append(out, provider.Branch{Key: b.Key, Name: b.Name, ShortName: b.ShortName, PlanKey: key, URL: c.URL(b.Key)})
	}
	return out, nil
}

var buildStateParam = map[provider.State]string{
	provider.StateSuccess: "Successful",
	provider.StateFailed:  "Failed",
}

func (c *Client) ListBuilds(ctx context.Context, key string, o provider.ListOptions) ([]provider.Build, error) {
	q := url.Values{"expand": {"results.result"}}
	if p, ok := buildStateParam[o.State]; ok {
		q.Set("buildstate", p)
	}
	if o.Unfinished {
		q.Set("includeAllStates", "true")
	}
	limit := o.Limit
	if limit == 0 {
		limit = 10
	}
	fetch := limit
	if o.State != "" {
		fetch = limit * 20 // also filtered client-side, since a server may ignore buildstate
	}
	items, err := pageAll[resultDTO](ctx, c, api+"/result/"+url.PathEscape(key), q, "results", fetch)
	if err != nil {
		return nil, c.notFoundAs(err, "plan", key, "bam plan list")
	}
	var out []provider.Build
	for _, r := range items {
		b := c.mapBuild(r)
		if o.State != "" && b.State != o.State {
			continue
		}
		out = append(out, b)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (c *Client) GetBuild(ctx context.Context, key string) (provider.Build, error) {
	var r resultDTO
	if err := c.getJSON(ctx, api+"/result/"+url.PathEscape(key), url.Values{"expand": {buildExpand}}, &r); err != nil {
		return provider.Build{}, c.notFoundAs(err, "build", key, "")
	}
	b := c.mapBuild(r)
	if b.State == provider.StateFailed {
		b.FailedTests = c.failedTests(ctx, b)
	}
	return b, nil
}
