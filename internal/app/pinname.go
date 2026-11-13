package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
)

// maxFits is how many plans a name that fits several is listed with.
const maxFits = 10

// FindPlanByName resolves a plan name to one plan. A name that matches a
// plan's whole name, ignoring case, wins; otherwise the plans whose name
// holds every word of name. It uses the server's plan search when there is
// one, and adds the plans of the configured projects when there is none, the
// search finds no fit, or the search says more plans may match than it
// answered. Anything but exactly one fit is a usage error that says which.
func (s *Service) FindPlanByName(ctx context.Context, name string) (provider.Plan, error) {
	words := strings.Fields(name)
	if len(words) == 0 {
		return provider.Plan{}, errs.Usagef("a pin cannot be empty")
	}
	name = strings.Join(words, " ")
	var found []provider.Plan
	searched, complete := false, false
	if ps, ok := s.P.(provider.PlanSearcher); ok {
		var err error
		found, complete, err = ps.SearchPlans(ctx, name)
		switch {
		case err == nil:
			searched = true
		case !errors.Is(err, errs.ErrUnsupported):
			return provider.Plan{}, err
		}
	}
	fits := planFits(found, name, words)
	if len(fits) == 0 || !complete {
		projects := s.ProjectKeys()
		all := found
		for _, p := range projects {
			plans, err := s.P.ListPlans(ctx, p)
			if err != nil {
				return provider.Plan{}, err
			}
			all = append(all, plans...)
		}
		fits = planFits(all, name, words)
		if len(fits) == 0 && !searched && len(projects) == 0 {
			return provider.Plan{}, errs.Usagef("cannot look up the plan name %q", name).
				WithWhy("this Bamboo has no plan search and no projects are configured to read plans from").
				WithTry("use a plan key like PROJ-PLAN, or add the project: bam server add --project KEY")
		}
	}
	partial := searched && !complete
	switch len(fits) {
	case 0:
		e := errs.Usagef("no plan is named or has a name containing %q", name).
			WithTry("bam plan list PROJECT, or a plan key like PROJ-PLAN")
		if partial {
			e = e.WithWhy("Bamboo's plan search found more plans for it than bam reads; give more of the name")
		}
		return provider.Plan{}, e
	case 1:
		if !partial || strings.EqualFold(fits[0].Name, name) {
			return fits[0], nil
		}
	}
	lines := make([]string, 0, maxFits+1)
	for i, p := range fits {
		if i == maxFits {
			lines = append(lines, fmt.Sprintf("and %d more", len(fits)-maxFits))
			break
		}
		lines = append(lines, p.Key+"  "+p.Name)
	}
	noun := "plans"
	if len(fits) == 1 {
		noun = "plan"
	}
	if partial {
		noun += " or more"
	}
	return provider.Plan{}, errs.Usagef("%q fits %d %s", name, len(fits), noun).
		WithWhy(strings.Join(lines, "\n  ")).
		WithTry("pin one of them by its plan key, or give more of its name")
}

// planFits keeps the plans an exact name picks, else those whose name holds
// every word, both ignoring case, without repeating a key. Disabled plans
// count: a pin may be set up before the plan is enabled.
func planFits(plans []provider.Plan, name string, words []string) []provider.Plan {
	var exact, part []provider.Plan
	seen := map[string]bool{}
	for _, p := range plans {
		if seen[p.Key] {
			continue
		}
		seen[p.Key] = true
		low := strings.ToLower(p.Name)
		if strings.EqualFold(p.Name, name) {
			exact = append(exact, p)
			continue
		}
		all := true
		for _, w := range words {
			if !strings.Contains(low, strings.ToLower(w)) {
				all = false
				break
			}
		}
		if all {
			part = append(part, p)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return part
}

// PlanName is the name of the plan a plan key or plan branch key belongs to.
func (s *Service) PlanName(ctx context.Context, planKey string) (string, error) {
	p, err := s.P.GetPlan(ctx, planKey)
	if err != nil {
		return "", err
	}
	return p.Name, nil
}
