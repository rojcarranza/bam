package config

import (
	"regexp"

	"github.com/r0jjames/bam-cli/internal/errs"
)

// childPlanKeyRe is a plan key, PROJ-PLAN (or a branch plan key, PROJ-PLAN12).
var childPlanKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*$`)

// Children is the children: map of every layer, project, then machine, then
// the machine's entry for this repository; a later layer's list replaces an
// earlier one for the same plan. Each key and value must be a plan key.
func (c *Config) Children() (map[string][]string, error) {
	out := map[string][]string{}
	layer := func(path, where string, m map[string][]string) error {
		for _, k := range sortedKeys(m) {
			if !childPlanKeyRe.MatchString(k) {
				return errs.Configf("%s: %s: %q is not a plan key", path, where, k).
					WithWhy("children maps a plan key like PROJ-API to the plans it triggers")
			}
			for _, v := range m[k] {
				switch {
				case !childPlanKeyRe.MatchString(v):
					return errs.Configf("%s: %s.%s: %q is not a plan key", path, where, k, v)
				case v == k:
					return errs.Configf("%s: %s.%s lists itself", path, where, k)
				}
			}
			out[k] = append([]string(nil), m[k]...)
		}
		return nil
	}
	if c.Project != nil {
		if err := layer(c.ProjectPath, "children", c.Project.Children); err != nil {
			return nil, err
		}
	}
	if err := layer(c.MachinePath, "children", c.Machine.Children); err != nil {
		return nil, err
	}
	if key, repo, ok := c.repoEntry(); ok {
		if err := layer(c.MachinePath, "repos."+key+".children", repo.Children); err != nil {
			return nil, err
		}
	}
	return out, nil
}
