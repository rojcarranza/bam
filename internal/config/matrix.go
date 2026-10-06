package config

import (
	"math"
	"sort"
	"strings"
)

// maxStageRuns caps how many runs one stage may expand to.
const maxStageRuns = 50

// ExpandedRun is one run of a stage after matrix expansion.
type ExpandedRun struct {
	Name   string // the preset, or preset[k=v,…] for a matrix run
	Target string
	Vars   StringMap // the set run's vars with the matrix values laid over them
}

// Expand returns the runs a set run stands for: itself when it has no
// matrix, else one per matrix entry, in order (a product varies its last
// sorted key fastest).
func (r SetRun) Expand() []ExpandedRun {
	if r.Matrix == nil {
		return []ExpandedRun{{Name: r.Target, Target: r.Target, Vars: r.Vars}}
	}
	var entries []StringMap
	if r.Matrix.Sets != nil {
		entries = r.Matrix.Sets
	} else {
		entries = product(r.Matrix.Product)
	}
	out := make([]ExpandedRun, 0, len(entries))
	for _, e := range entries {
		vars := StringMap{}
		for k, v := range r.Vars {
			vars[k] = v
		}
		for k, v := range e {
			vars[k] = v
		}
		out = append(out, ExpandedRun{Name: runName(r.Target, e), Target: r.Target, Vars: vars})
	}
	return out
}

func product(p map[string][]string) []StringMap {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []StringMap{{}}
	for _, k := range keys {
		var next []StringMap
		for _, partial := range out {
			for _, v := range p[k] {
				e := StringMap{}
				for pk, pv := range partial {
					e[pk] = pv
				}
				e[k] = v
				next = append(next, e)
			}
		}
		out = next
	}
	return out
}

// runName is preset[k=v,…] over the entry's own keys, sorted. A secret
// named key's literal value is masked: names are printed and recorded.
func runName(target string, e StringMap) string {
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		v := e[k]
		if _, ref := EnvRef(v); !ref && IsMaskedName(k) {
			v = "********"
		}
		parts[i] = k + "=" + v
	}
	return target + "[" + strings.Join(parts, ",") + "]"
}

// Size is how many runs Expand would return, computed without building them,
// so an oversized product is refused cheaply. It saturates at math.MaxInt64.
func (r SetRun) Size() int64 {
	switch {
	case r.Matrix == nil:
		return 1
	case r.Matrix.Sets != nil:
		return int64(len(r.Matrix.Sets))
	}
	n := int64(1)
	for _, vs := range r.Matrix.Product {
		l := int64(len(vs))
		if l > 0 && n > math.MaxInt64/l {
			return math.MaxInt64
		}
		n *= l
	}
	return n
}

// values calls f with every variable a set run sets, its vars and every
// matrix value, without expanding the matrix. Keys go in sorted order, so
// callers see the same sequence on every run.
func (r SetRun) values(f func(name, value string)) {
	for _, k := range sortedKeys(r.Vars) {
		f(k, r.Vars[k])
	}
	if r.Matrix == nil {
		return
	}
	for _, k := range sortedKeys(r.Matrix.Product) {
		for _, v := range r.Matrix.Product[k] {
			f(k, v)
		}
	}
	for _, e := range r.Matrix.Sets {
		for _, k := range sortedKeys(e) {
			f(k, e[k])
		}
	}
}
