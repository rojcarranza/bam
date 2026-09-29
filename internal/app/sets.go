package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/credential"
	"github.com/r0jjames/bam-cli/internal/errs"
)

// SetOptions are the command-line inputs to a set run.
type SetOptions struct {
	Vars   []string // --var name=value, in order; applies to every run
	Server string   // --server, so every run's server is chosen as bam run would
}

// SetRunPlan is one run of a stage, resolved and ready to trigger.
type SetRunPlan struct {
	Target  string
	Ref     PlanRef
	Vars    VarSet
	Timeout time.Duration // the preset's; zero means none
}

// SetPlan is a set with every run resolved and checked. RunSet does no
// further resolving.
type SetPlan struct {
	Name     string
	Origin   string
	Timeout  time.Duration
	VarNames []string // names given with --var, kept in the record for resume
	Stages   [][]SetRunPlan
	Warnings []string // each run's variable warnings, prefixed with its preset
}

// PlanSet resolves and checks every run of a set, so that every usage error
// shows before the first trigger. It triggers nothing.
func (s *Service) PlanSet(ctx context.Context, name string, o SetOptions) (SetPlan, error) {
	set, ok, err := s.Cfg.Set(name)
	if err != nil {
		return SetPlan{}, err
	}
	if !ok {
		return SetPlan{}, errs.Usagef("unknown set %q", name).WithTry("bam set list")
	}
	p := SetPlan{Name: name, Origin: s.Origin, Timeout: time.Duration(set.Timeout), VarNames: varNames(o.Vars)}
	var problems []string
	for i, st := range set.Stages {
		runs := make([]SetRunPlan, 0, len(st))
		for _, r := range st {
			rp, err := s.planRun(ctx, r, o)
			if err != nil {
				// Only a usage problem is one run's fault; anything else
				// (config, auth, Bamboo down) stops the whole set at once.
				if errs.KindOf(err) != errs.KindUsage {
					return SetPlan{}, err
				}
				problems = append(problems, fmt.Sprintf("stage %d %s: %s", i+1, r.Target, describeErr(err)))
				continue
			}
			for _, w := range rp.Vars.Warnings {
				p.Warnings = append(p.Warnings, r.Target+": "+w)
			}
			runs = append(runs, rp)
		}
		p.Stages = append(p.Stages, runs)
	}
	if len(problems) > 0 {
		return SetPlan{}, errs.Usagef("set %q cannot run: %d problem(s)", name, len(problems)).
			WithWhy(strings.Join(problems, "\n")).WithTry("fix the presets or pass --var, then try again")
	}
	return p, nil
}

// planRun resolves one run: the preset's plan and branch, then its
// variables with the set's vars laid over the preset's defaults.
func (s *Service) planRun(ctx context.Context, r config.SetRun, o SetOptions) (SetRunPlan, error) {
	t, _, err := s.Cfg.Target(r.Target)
	if err != nil {
		return SetRunPlan{}, err
	}
	server, err := s.Cfg.SelectServer(o.Server, t.Server)
	if err != nil {
		return SetRunPlan{}, err
	}
	if origin, err := credential.Origin(server.URL); err != nil || origin != s.Origin {
		return SetRunPlan{}, errs.Configf("preset %q runs on %s, not %s", r.Target, server.URL, s.Origin).
			WithWhy("a set runs on one server").WithTry("give every preset of the set the same server")
	}
	ref, err := s.ResolvePlan(ctx, r.Target, "")
	if err != nil {
		return SetRunPlan{}, err
	}
	if len(r.Vars) > 0 {
		// A copy: the set's vars must not leak into the preset itself.
		rt := *ref.Target
		merged := config.StringMap{}
		for k, v := range rt.Defaults {
			merged[k] = v
		}
		for k, v := range r.Vars {
			merged[k] = v
		}
		rt.Defaults = merged
		ref.Target = &rt
	}
	vs, err := s.ResolveVars(ctx, ref, VarOptions{Flags: o.Vars})
	if err != nil {
		return SetRunPlan{}, err
	}
	return SetRunPlan{Target: r.Target, Ref: ref, Vars: vs, Timeout: time.Duration(t.Timeout)}, nil
}

// varNames is the distinct names of --var flags, in order.
func varNames(flags []string) []string {
	out := []string{}
	for _, f := range flags {
		if name, _, ok := strings.Cut(f, "="); ok && name != "" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// describeErr is an error's What and Why on one line, for a list of problems.
func describeErr(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		if e.Why != "" {
			return e.What + " (" + e.Why + ")"
		}
		return e.What
	}
	return err.Error()
}
