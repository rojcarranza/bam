package bamboo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
)

// activityPath is the list behind Bamboo's Build activity page: each job and
// deployment that runs now, with its agent, the moment it starts. Bamboo does
// not document it, so bam learns once whether a server gives it.
const activityPath = "/build/ajax/getDashboardSummary.action"

type activityDTO struct {
	Status string            `json:"status"` // "OK" when Bamboo answered the list
	Builds []activityItemDTO `json:"builds"`
}

type activityItemDTO struct {
	ItemType string `json:"itemType"` // BUILD or DEPLOYMENT
	// ResultKey is a job's result key; a deployment's holds its ids.
	ResultKey     string `json:"resultKey"`
	PlanResultKey string `json:"planResultKey"`
	PlanKey       string `json:"planKey"` // the plan branch's key on a branch
	// ResultID, EnvironmentName, DeploymentProjectName and VersionName name a
	// deployment.
	ResultID              int64  `json:"resultId"`
	EnvironmentName       string `json:"environmentName"`
	DeploymentProjectName string `json:"deploymentProjectName"`
	VersionName           string `json:"versionName"`
	Agent                 *struct {
		Name string `json:"name"`
	} `json:"agent"` // null while the job waits for an agent
}

// Activity lists what each agent runs now from the Build activity list. A
// server that has no such list, refuses it or answers with something other
// than the list is learned as lacking it, and answers ErrUnsupported until the
// capabilities go stale.
func (c *Client) Activity(ctx context.Context) ([]provider.AgentActivity, error) {
	unsupported := errs.Bamboof("this Bamboo does not list what its agents run").Wrap(errs.ErrUnsupported)
	caps := c.fresh(ctx)
	if caps.Activity == "no" {
		return nil, unsupported
	}
	body, err := c.do(ctx, request{method: http.MethodGet, path: activityPath})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if isUnsupportedStatus(err) || statusOf(err) == http.StatusForbidden {
			c.learn(ctx, func(cp *Capabilities) { cp.Activity = "no" })
			return nil, unsupported
		}
		return nil, err
	}
	var d activityDTO
	if json.Unmarshal(body, &d) != nil || d.Status != "OK" {
		// A login page, or a list of another shape.
		c.learn(ctx, func(cp *Capabilities) { cp.Activity = "no" })
		return nil, unsupported
	}
	if caps.Activity != "yes" {
		c.learn(ctx, func(cp *Capabilities) { cp.Activity = "yes" })
	}
	var out []provider.AgentActivity
	for _, it := range d.Builds {
		if it.Agent == nil || it.Agent.Name == "" {
			continue
		}
		switch it.ItemType {
		case "BUILD":
			if it.ResultKey == "" || it.PlanResultKey == "" {
				continue
			}
			out = append(out, provider.AgentActivity{Agent: it.Agent.Name, Job: it.ResultKey,
				Build: it.PlanResultKey, PlanKey: it.PlanKey})
		case "DEPLOYMENT":
			if it.ResultID == 0 {
				continue
			}
			id := strconv.FormatInt(it.ResultID, 10)
			out = append(out, provider.AgentActivity{Agent: it.Agent.Name, Deploy: &provider.Deployment{
				ID: id, Project: it.DeploymentProjectName, Environment: it.EnvironmentName, Release: it.VersionName,
				URL: c.base.String() + "/deploy/viewDeploymentResult.action?deploymentResultId=" + id}})
		}
	}
	return out, nil
}

type deployResultDTO struct {
	StartedDate  int64 `json:"startedDate"`  // when it was asked for, in milliseconds
	ExecutedDate int64 `json:"executedDate"` // when its agent began it
}

// DeploymentStarted is when the deployment began on its agent, or when it was
// asked for while no agent has begun it.
func (c *Client) DeploymentStarted(ctx context.Context, id string) (time.Time, error) {
	var d deployResultDTO
	if err := c.getJSON(ctx, api+"/deploy/result/"+url.PathEscape(id), nil, &d); err != nil {
		return time.Time{}, err
	}
	ms := d.ExecutedDate
	if ms == 0 {
		ms = d.StartedDate
	}
	if ms == 0 {
		return time.Time{}, nil
	}
	return time.UnixMilli(ms).UTC(), nil
}
