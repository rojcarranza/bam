package app

import (
	"context"
	"time"

	"github.com/r0jjames/bam-cli/internal/config"
)

// TargetVar is one default of a target, safe to display.
type TargetVar struct {
	Name   string
	Value  string // literal, or the ${NAME} reference as written
	EnvRef string // NAME when Value is ${NAME}
	EnvSet bool
	Secret bool
}

// Display never reveals a secret literal or an environment value.
func (v TargetVar) Display() string {
	switch {
	case v.EnvRef != "" && v.EnvSet:
		return v.Value + " (set)"
	case v.EnvRef != "":
		return v.Value + " (unset)"
	case v.Secret:
		return MaskedDisplay
	}
	return v.Value
}

// TargetInfo describes a merged target for bam preset list/show.
type TargetInfo struct {
	Name      string
	Plan      string
	Server    string
	Branch    string
	Defaults  []TargetVar // sorted by name
	Options   map[string][]string
	Required  []string
	Watch     bool
	Timeout   time.Duration
	DefinedIn []string
}

// DescribeTargets returns every merged target, sorted by name.
func DescribeTargets(cfg *config.Config, getenv func(string) string) ([]TargetInfo, error) {
	all, err := cfg.Targets()
	if err != nil {
		return nil, err
	}
	out := make([]TargetInfo, 0, len(all))
	for _, name := range sortedNames(all) {
		out = append(out, describe(all[name], getenv))
	}
	return out, nil
}

// DescribeTarget returns one target.
func DescribeTarget(cfg *config.Config, getenv func(string) string, name string) (TargetInfo, error) {
	t, ok, err := cfg.Target(name)
	if err != nil {
		return TargetInfo{}, err
	}
	if !ok {
		return TargetInfo{}, UnknownTarget(cfg, name)
	}
	return describe(t, getenv), nil
}

func describe(t config.ResolvedTarget, getenv func(string) string) TargetInfo {
	info := TargetInfo{Name: t.Name, Plan: t.Plan, Server: t.Server, Branch: t.Branch,
		Options: t.Options, Required: t.Required, Timeout: time.Duration(t.Timeout), DefinedIn: t.DefinedIn}
	if t.Watch != nil {
		info.Watch = *t.Watch
	}
	info.Defaults = targetVars(t.Defaults, getenv)
	return info
}

// targetVars describes configured variables, sorted by name, safe to
// display: a ${NAME} reference is always secret.
func targetVars(m config.StringMap, getenv func(string) string) []TargetVar {
	var out []TargetVar
	for _, name := range sortedNames(m) {
		v := TargetVar{Name: name, Value: m[name], Secret: config.IsMaskedName(name)}
		if ref, ok := config.EnvRef(v.Value); ok {
			v.EnvRef, v.EnvSet, v.Secret = ref, getenv(ref) != "", true
		}
		out = append(out, v)
	}
	return out
}

// RefFromTarget builds a plan reference from an already-described target.
//
// It exists for the terminal UI, which refreshes its preset list from the
// files on disk and must then resolve one of those presets. Going back
// through Service.Cfg would resolve against the configuration parsed when the
// UI started, so a preset added since would be unknown, or would run on its
// old branch. TargetInfo carries the raw values, including ${ENV} references
// as written, so nothing is lost in the round trip.
func (s *Service) RefFromTarget(ctx context.Context, t TargetInfo) (PlanRef, error) {
	rt := config.ResolvedTarget{Name: t.Name, Target: config.Target{
		Plan:     t.Plan,
		Server:   t.Server,
		Branch:   t.Branch,
		Options:  config.StringListMap(t.Options),
		Required: t.Required,
	}}
	if len(t.Defaults) > 0 {
		rt.Defaults = config.StringMap{}
		for _, d := range t.Defaults {
			rt.Defaults[d.Name] = d.Value
		}
	}
	ref := PlanRef{PlanKey: t.Plan, MasterKey: t.Plan, Target: &rt}
	if t.Branch == "" {
		return ref, nil
	}
	key, err := s.ResolveBranch(ctx, t.Plan, t.Branch)
	if err != nil {
		return PlanRef{}, err
	}
	ref.PlanKey, ref.Branch = key, t.Branch
	return ref, nil
}
