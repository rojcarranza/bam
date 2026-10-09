package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// probePlans records how Bamboo pages a project's plans, for issue #43: the
// first page of GET project/{key}?expand=plans.plan, the second page asked
// for with the expand range plans[25:125].plan, and the second page of the
// server-wide GET plan as a fallback. Plans are replaced by synthetic ones
// (see synthPlans), so a work server's plan names never reach a recording.
// It triggers nothing.
func (r *recorder) probePlans(project string) error {
	steps := []struct {
		file, path string
		query      url.Values
	}{
		{"plans_page1.json", "/rest/api/latest/project/" + project, url.Values{"expand": {"plans.plan"}}},
		{"plans_page2.json", "/rest/api/latest/project/" + project, url.Values{"expand": {"plans[25:125].plan"}}},
		{"plans_all_page2.json", "/rest/api/latest/plan", url.Values{"start-index": {"25"}, "max-result": {"25"}}},
	}
	ids := map[string]int{}
	for _, s := range steps {
		body, status, err := r.call(http.MethodGet, s.path, s.query)
		if err != nil {
			return fmt.Errorf("GET %s: %w", s.path, err)
		}
		if status != http.StatusOK {
			return fmt.Errorf("GET %s: status %d", s.path, status)
		}
		before := len(ids)
		synth, err := synthPlans(body, ids, project)
		if err != nil {
			return fmt.Errorf("%s: %w", s.file, err)
		}
		var page struct {
			Plans struct {
				Size      int               `json:"size"`
				MaxResult int               `json:"max-result"`
				Plan      []json.RawMessage `json:"plan"`
			} `json:"plans"`
		}
		_ = json.Unmarshal(synth, &page)
		fmt.Printf("%-28s %d plans (%d new), size %d, max-result %d\n",
			s.file, len(page.Plans.Plan), len(ids)-before, page.Plans.Size, page.Plans.MaxResult)
		if err := writeScrubbed(r.out, s.file, synth, r.scrub); err != nil {
			return err
		}
	}
	return nil
}

// synthPlans keeps the paging envelope of a plans response (size,
// start-index, max-result, expand) and the project's key and name, and
// replaces every plan with a synthetic one named after its number in ids.
// ids maps a real plan key to its number and is shared across pages, so the
// same plan gets the same synthetic key in every file. A plan of project
// keeps that key, which the scrubber then renames; a plan of any other
// project gets OPS, since the scrubber may not know that project's key.
func synthPlans(body []byte, ids map[string]int, project string) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, k := range []string{"key", "name", "expand"} {
		if v, ok := root[k]; ok {
			out[k] = v
		}
	}
	env, _ := root["plans"].(map[string]any)
	plans := map[string]any{}
	for _, k := range []string{"size", "start-index", "max-result", "expand"} {
		if v, ok := env[k]; ok {
			plans[k] = v
		}
	}
	items, _ := env["plan"].([]any)
	synth := make([]any, 0, len(items))
	for _, item := range items {
		p, _ := item.(map[string]any)
		key, _ := p["key"].(string)
		owner, _ := p["projectKey"].(string)
		if owner != project {
			owner = "OPS"
		}
		n, ok := ids[key]
		if !ok {
			n = len(ids) + 1
			ids[key] = n
		}
		short := fmt.Sprintf("P%d", n)
		plan := map[string]any{
			"key":        owner + "-" + short,
			"shortKey":   short,
			"name":       fmt.Sprintf("%s - Plan %d", owner, n),
			"shortName":  fmt.Sprintf("Plan %d", n),
			"projectKey": owner,
		}
		if v, ok := p["enabled"]; ok {
			plan["enabled"] = v
		}
		synth = append(synth, plan)
	}
	plans["plan"] = synth
	out["plans"] = plans
	return prettyJSON(out)
}
