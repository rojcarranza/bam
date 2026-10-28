package app

import (
	"context"
	"errors"
	"sort"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// Change is one value that differs between two builds. Was or Now is empty
// when the value exists in only one of them.
type Change struct {
	Name string
	Was  string
	Now  string
}

// Comparison is what changed between a build and the last successful build
// of the same plan or branch before it.
type Comparison struct {
	Since     provider.Build // zero Key when no earlier build succeeded
	Revisions []Change       // by repository
	Variables []Change       // nil when the server cannot read build variables back
}

// compareWindow is how many successful builds are read to find one older than b.
const compareWindow = 25

// CompareLastSuccess diffs b's revisions and variables against the last
// successful build before it. Secret values are masked.
func (s *Service) CompareLastSuccess(ctx context.Context, b provider.Build) (Comparison, error) {
	var c Comparison
	ok, err := s.P.ListBuilds(ctx, b.PlanKey, provider.ListOptions{State: provider.StateSuccess, Limit: compareWindow})
	if err != nil {
		return c, err
	}
	for _, o := range ok {
		if o.Number < b.Number {
			c.Since = o
			break
		}
	}
	if c.Since.Key == "" {
		return c, nil
	}
	// The result list carries no revisions.
	if c.Since, err = s.P.GetBuild(ctx, c.Since.Key); err != nil {
		return c, err
	}
	c.Revisions = changes(revisionMap(c.Since.Revisions), revisionMap(b.Revisions), false)
	was, err := s.P.BuildVariables(ctx, c.Since.Key)
	if errors.Is(err, errs.ErrUnsupported) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	now, err := s.P.BuildVariables(ctx, b.Key)
	if err != nil {
		return c, err
	}
	c.Variables = changes(was, now, true)
	return c, nil
}

func revisionMap(rs []provider.Revision) map[string]string {
	m := map[string]string{}
	for _, r := range rs {
		m[r.Repository] = r.Revision
	}
	return m
}

// changes lists the names whose values differ, sorted by name.
func changes(was, now map[string]string, mask bool) []Change {
	out := []Change{}
	for name := range union(was, now) {
		w, n := was[name], now[name]
		if w == n {
			continue
		}
		if mask && (IsSecretName(name) || w == MaskedDisplay || n == MaskedDisplay) {
			w, n = maskSet(w), maskSet(n)
		}
		out = append(out, Change{Name: name, Was: w, Now: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func union(a, b map[string]string) map[string]bool {
	u := map[string]bool{}
	for k := range a {
		u[k] = true
	}
	for k := range b {
		u[k] = true
	}
	return u
}

// maskSet hides a value but keeps whether it was set.
func maskSet(v string) string {
	if v == "" {
		return ""
	}
	return MaskedDisplay
}
