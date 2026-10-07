package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// mentionsBuild reports whether text names build key exactly: not as part
// of a longer key or a longer build number.
func mentionsBuild(text, key string) bool {
	re := regexp.MustCompile(`(^|[^A-Za-z0-9-])` + regexp.QuoteMeta(key) + `([^0-9]|$)`)
	return re.MatchString(text)
}

// childResults is the keys of the results in a result list whose build
// reason names parent: the builds parent triggered as its children.
func childResults(body []byte, parent string) ([]string, error) {
	var list struct {
		Results struct {
			Result []struct {
				Key    string `json:"buildResultKey"`
				Reason string `json:"buildReason"`
			} `json:"result"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parse results: %w", err)
	}
	var keys []string
	for _, r := range list.Results.Result {
		if r.Key != parent && mentionsBuild(r.Reason, parent) {
			keys = append(keys, r.Key)
		}
	}
	return keys, nil
}

// probeChildren records what Bamboo tells about the builds a parent plan's
// newest build triggered, for the build tree design (issue #27): the REST
// paths that mention dependencies, the plan and the parent build expanded,
// the server's recent results, and the result of each child build found by
// its build reason. It triggers nothing.
func (r *recorder) probeChildren(plan string) error {
	wadl, status, err := r.callAccept(http.MethodGet, "/rest/api/latest/application.wadl", nil, "")
	switch {
	case err != nil || status != 200:
		fmt.Printf("%-32s %d: no WADL\n", "children_wadl.txt", status)
	default:
		s, err := summarizeWADL(wadl)
		if err != nil {
			return err
		}
		text := strings.Join(s.Related, "\n") + "\n"
		fmt.Printf("%-32s %d paths mention dependencies, children, parents or triggers\n", "children_wadl.txt", len(s.Related))
		if err := os.WriteFile(filepath.Join(r.out, "children_wadl.txt"), r.scrub.Scrub([]byte(text)), 0o644); err != nil {
			return err
		}
	}
	parent, err := r.latestBuild(plan)
	if err != nil {
		return err
	}
	resultsQuery := url.Values{"expand": {"results.result"}, "max-result": {"100"}, "includeAllStates": {"true"}}
	// The dependency search is the plan picker of the UI; it wants a term.
	search := url.Values{"searchTerm": {strings.SplitN(plan, "-", 2)[0]}}
	detail := url.Values{"expand": {"stages.stage.results.result,vcsRevisions,labels"}}
	steps := []struct {
		file, path string
		query      url.Values
	}{
		{"children_plan.json", "/rest/api/latest/plan/" + plan, url.Values{"expand": {"stages,branches,variableContext"}}},
		{"children_plan_dependencies.json", "/rest/api/latest/plan/" + plan + "/dependencies", nil},
		{"children_dependency_child.json", "/rest/api/latest/dependency/search/" + plan + "/child", search},
		{"children_dependency_parent.json", "/rest/api/latest/dependency/search/" + plan + "/parent", search},
		{"children_parent_result.json", "/rest/api/latest/result/" + parent, detail},
		{"children_results.json", "/rest/api/latest/result", resultsQuery},
	}
	for _, s := range steps {
		if err := r.record(s.file, http.MethodGet, s.path, s.query); err != nil {
			return err
		}
	}
	// The server-wide result list holds each plan's newest build only, so
	// each plan's own recent builds are searched for the parent's key.
	plans, err := r.planKeys()
	if err != nil {
		return err
	}
	// A term naming another project tells whether the dependency search
	// lists the plan's dependencies or every plan matching the term.
	own := strings.SplitN(plan, "-", 2)[0]
	for _, p := range plans {
		if other := strings.SplitN(p, "-", 2)[0]; other != own {
			if err := r.record("children_dependency_child_other.json", http.MethodGet,
				"/rest/api/latest/dependency/search/"+plan+"/child", url.Values{"searchTerm": {other}}); err != nil {
				return err
			}
			break
		}
	}
	var children []string
	for _, p := range plans {
		body, status, err := r.call(http.MethodGet, "/rest/api/latest/result/"+p, url.Values{"expand": {"results.result"}, "max-result": {"25"}})
		if err != nil || status != 200 {
			continue
		}
		keys, err := childResults(body, parent)
		if err != nil {
			return err
		}
		children = append(children, keys...)
	}
	if len(children) == 0 {
		fmt.Printf("no build of the server's %d plans names %s in its build reason\n", len(plans), parent)
		return nil
	}
	fmt.Printf("%s triggered: %s\n", parent, strings.Join(children, ", "))
	for i, key := range children {
		if err := r.record(fmt.Sprintf("children_child_%d.json", i), http.MethodGet, "/rest/api/latest/result/"+key, detail); err != nil {
			return err
		}
	}
	return nil
}

// planKeys is the key of every plan on the server, page by page: a server
// may return fewer plans per page than asked for.
func (r *recorder) planKeys() ([]string, error) {
	var keys []string
	for {
		q := url.Values{"max-result": {"100"}, "start-index": {strconv.Itoa(len(keys))}}
		body, status, err := r.call(http.MethodGet, "/rest/api/latest/plan", q)
		if err != nil || status != 200 {
			return nil, fmt.Errorf("list plans: status %d: %v", status, err)
		}
		var page struct {
			Plans struct {
				Size int `json:"size"`
				Plan []struct {
					Key string `json:"key"`
				} `json:"plan"`
			} `json:"plans"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parse plans: %w", err)
		}
		for _, p := range page.Plans.Plan {
			keys = append(keys, p.Key)
		}
		if len(page.Plans.Plan) == 0 || len(keys) >= page.Plans.Size {
			return keys, nil
		}
	}
}
