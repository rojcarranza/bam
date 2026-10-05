package config

import (
	"fmt"

	"github.com/r0jjames/bam-cli/internal/errs"
)

// ResolvedSet is a set from the highest layer that defines it. Sets are
// replaced whole, never merged: merging stage lists would hide which stages
// run.
type ResolvedSet struct {
	Name string
	Set
	DefinedIn []string // every layer that defines the set, lowest precedence first; the last one won
}

// Sets returns every set, checked against the merged targets. The layers are
// the ones targets use: project, machine top level, machine repos entry.
func (c *Config) Sets() (map[string]ResolvedSet, error) {
	out := map[string]ResolvedSet{}
	layer := func(label string, sets map[string]Set) {
		for name, s := range sets {
			r := out[name]
			r.Name, r.Set = name, s
			r.DefinedIn = append(r.DefinedIn, label)
			out[name] = r
		}
	}
	if c.Project != nil {
		layer(c.ProjectPath, c.Project.Sets)
	}
	layer(c.MachinePath, c.Machine.Sets)
	if _, repo, ok := c.repoEntry(); ok {
		layer(c.MachinePath+" (repos)", repo.Sets)
	}
	if len(out) == 0 {
		return out, nil
	}
	targets, err := c.Targets()
	if err != nil {
		return nil, err
	}
	for _, name := range sortedKeys(out) {
		if err := validateSet(out[name], targets); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Set returns one set.
func (c *Config) Set(name string) (ResolvedSet, bool, error) {
	all, err := c.Sets()
	if err != nil {
		return ResolvedSet{}, false, err
	}
	s, ok := all[name]
	return s, ok, nil
}

func validateSet(s ResolvedSet, targets map[string]ResolvedTarget) error {
	where := s.DefinedIn[len(s.DefinedIn)-1]
	fail := func(format string, a ...any) *errs.Error {
		return errs.Configf("set %q "+format, append([]any{s.Name}, a...)...).WithWhy("defined in " + where)
	}
	if len(s.Stages) == 0 {
		return fail("has no stages").WithTry(fmt.Sprintf("add stages: [[<preset>]] under sets.%s", s.Name))
	}
	server, serverFrom := "", ""
	for i, st := range s.Stages {
		if len(st) == 0 {
			return fail("stage %d has no runs", i+1)
		}
		seen := map[string]bool{}
		count := 0
		for _, r := range st {
			t, ok := targets[r.Target]
			if !ok {
				return fail("names unknown preset %q in stage %d", r.Target, i+1).WithTry("bam target list")
			}
			for _, x := range r.Expand() {
				if seen[x.Name] {
					return fail("lists run %q twice in stage %d", x.Name, i+1)
				}
				seen[x.Name] = true
				count++
			}
			if t.Server == "" {
				continue
			}
			if server != "" && t.Server != server {
				return fail("mixes servers: presets %q and %q use servers %q and %q", serverFrom, r.Target, server, t.Server).
					WithTry("a set runs on one server; make one set per server")
			}
			server, serverFrom = t.Server, r.Target
		}
		if count > maxStageRuns {
			return fail("stage %d expands to %d runs; the limit is %d", i+1, count, maxStageRuns).
				WithTry("split the matrix across stages, or list fewer variable sets")
		}
	}
	return nil
}
