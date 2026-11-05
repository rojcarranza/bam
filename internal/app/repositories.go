package app

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// Repository branches of a plan branch. Bamboo has no REST call that changes
// which branch a plan branch's repository builds; a person sets it on the
// plan branch's Repositories tab. bam reads the branch each repository built
// in the newest finished job, tells the person when it differs from the
// preset's repositories, and triggers once they have switched it.

// builtLookback is how many of the newest builds bam reads to find a finished job.
const builtLookback = 10

// ParseRepositoryFlag reads one --repository value: NAME=BRANCH.
func ParseRepositoryFlag(s string) (name, branch string, err error) {
	name, branch, ok := strings.Cut(s, "=")
	if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(branch) == "" {
		return "", "", errs.Usagef("--repository %q is not NAME=BRANCH", s).
			WithTry("--repository repo-a=feature/PROJ-101, with the name as the Repositories tab shows it")
	}
	return name, branch, nil
}

// RepositoryWants merges the preset's repositories with --repository flags:
// a flag beats the preset's entry for the same name.
func RepositoryWants(flags []string, t *config.ResolvedTarget) (map[string]string, error) {
	out := map[string]string{}
	if t != nil {
		maps.Copy(out, t.Repositories)
	}
	seen := map[string]bool{}
	for _, f := range flags {
		name, branch, err := ParseRepositoryFlag(f)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, errs.Usagef("--repository %s is given twice", name)
		}
		seen[name] = true
		out[name] = branch
	}
	return out, nil
}

// RepoChange is one repository whose built branch is not the wanted one.
type RepoChange struct {
	Name  string
	Built string // "" when bam could not read it
	Want  string
}

// RepoCheck is what the run must have switched on the Repositories tab.
type RepoCheck struct {
	PlanKey  string // the plan branch
	URL      string // its Repositories tab
	BuildKey string // the build whose job was read; "" when no job had finished
	Changes  []RepoChange
}

// Needed reports whether the person has to switch something.
func (c RepoCheck) Needed() bool { return len(c.Changes) > 0 }

// Known reports whether bam read what the last build used.
func (c RepoCheck) Known() bool { return c.BuildKey != "" }

// BuiltRepositories returns the branch each repository built in the newest
// build of planKey that has a finished job, and that build's key. It
// returns "" and nil when no build has one.
func (s *Service) BuiltRepositories(ctx context.Context, planKey string) (map[string]string, string, error) {
	builds, err := s.P.ListBuilds(ctx, planKey, provider.ListOptions{Limit: builtLookback})
	if err != nil {
		return nil, "", err
	}
	for _, b := range builds {
		full, err := s.P.GetBuild(ctx, b.Key)
		if err != nil {
			return nil, "", err
		}
		if built := firstJobBranches(full); built != nil {
			return built, b.Key, nil
		}
	}
	return nil, "", nil
}

// firstJobBranches is the repository branches of the first job, in stage
// order, that recorded any: only a finished job has them.
func firstJobBranches(b provider.Build) map[string]string {
	for _, st := range b.Stages {
		for _, j := range st.Jobs {
			if len(j.Branches) > 0 {
				return j.Branches
			}
		}
	}
	return nil
}

func repoChanges(want, built map[string]string) []RepoChange {
	var out []RepoChange
	for _, name := range slices.Sorted(maps.Keys(want)) {
		if built[name] != want[name] {
			out = append(out, RepoChange{Name: name, Built: built[name], Want: want[name]})
		}
	}
	return out
}

// CheckRepositories compares want with what the plan branch built last. A
// name the last build does not list is a usage error, before anything is
// triggered. With no finished job bam cannot tell, so every entry is a change.
func (s *Service) CheckRepositories(ctx context.Context, ref PlanRef, want map[string]string) (RepoCheck, error) {
	if len(want) == 0 {
		return RepoCheck{}, nil
	}
	if ref.Branch == "" {
		return RepoCheck{}, errs.Usagef("repositories need a plan branch").
			WithWhy("a repository's branch is set on a plan branch's Repositories tab, not on the main plan").
			WithTry("add --branch NAME, or branch: to the preset")
	}
	c := RepoCheck{PlanKey: ref.PlanKey, URL: s.P.RepositoriesURL(ref.PlanKey)}
	built, key, err := s.BuiltRepositories(ctx, ref.PlanKey)
	if err != nil {
		return RepoCheck{}, err
	}
	if built != nil {
		for _, name := range slices.Sorted(maps.Keys(want)) {
			if _, ok := built[name]; !ok {
				return RepoCheck{}, errs.Usagef("%s does not build a repository called %s", ref.PlanKey, name).
					WithWhy(fmt.Sprintf("its last build, %s, built %s", key, strings.Join(slices.Sorted(maps.Keys(built)), ", "))).
					WithTry("name a repository as the Repositories tab shows it: " + c.URL)
			}
		}
	}
	c.BuildKey, c.Changes = key, repoChanges(want, built)
	return c, nil
}

// ConfirmRepositories reads the branches build built after its first job
// finished. ok is false while no job has finished; changes lists the
// repositories that built another branch than want.
func (s *Service) ConfirmRepositories(ctx context.Context, buildKey string, want map[string]string) (changes []RepoChange, ok bool, err error) {
	b, err := s.P.GetBuild(ctx, buildKey)
	if err != nil {
		return nil, false, err
	}
	built := firstJobBranches(b)
	if built == nil {
		return nil, false, nil
	}
	return repoChanges(want, built), true, nil
}
