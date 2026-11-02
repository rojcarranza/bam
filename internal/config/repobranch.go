package config

import (
	"sort"
	"strings"
)

// RepoBranch is one repository branch of a run (issue #130): build Branch
// of Repository, on Plan only when it is set, else on every plan of the run
// whose default repository Repository is.
type RepoBranch struct {
	Plan       string
	Repository string
	Branch     string
}

// Key is the repo_branches key that names r: REPO, or PLAN:REPO.
func (r RepoBranch) Key() string {
	if r.Plan == "" {
		return r.Repository
	}
	return r.Plan + ":" + r.Repository
}

// ParseRepoKey reads a repo_branches key, or what --repo-branch has before
// its "=": REPO, or PLAN:REPO when the text before the first colon is a plan
// key. A repository name may itself hold a colon. Branch is left empty.
func ParseRepoKey(key string) (RepoBranch, bool) {
	var r RepoBranch
	if plan, repo, ok := strings.Cut(key, ":"); ok && childPlanKeyRe.MatchString(plan) {
		r.Plan, key = plan, repo
	}
	if strings.TrimSpace(key) == "" {
		return RepoBranch{}, false
	}
	r.Repository = key
	return r, true
}

// RepoBranchList is the target's repo_branches: entries for every plan of
// the run first, then entries for one plan, each sorted by key, so the first
// names the run's plan branch. Keys that do not parse are left out;
// validation reports them.
func (t Target) RepoBranchList() []RepoBranch {
	keys := make([]string, 0, len(t.RepoBranches))
	for k := range t.RepoBranches {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []RepoBranch
	for _, k := range keys {
		r, ok := ParseRepoKey(k)
		if !ok {
			continue
		}
		r.Branch = t.RepoBranches[k]
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Plan == "" && out[j].Plan != "" })
	return out
}
