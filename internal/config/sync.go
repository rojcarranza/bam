package config

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/r0jjames/bam-cli/internal/errs"
	"gopkg.in/yaml.v3"
)

// TargetEdit is what bam preset sync changes in one layer's entry of a target.
type TargetEdit struct {
	Add    []DraftVar // names to append to defaults
	Mark   []string   // names to mark as no longer declared on Plan
	Unmark []string   // names whose marker, if any, is removed
	Plan   string     // plan key named in the marker
}

// TargetChange is what an edit changed, or would change.
type TargetChange struct {
	Added    []string
	Marked   []string
	Unmarked []string
}

// Empty reports whether nothing changed.
func (c TargetChange) Empty() bool {
	return len(c.Added) == 0 && len(c.Marked) == 0 && len(c.Unmarked) == 0
}

// StaleMarker is the comment sync adds to a default the plan no longer declares.
func StaleMarker(plan string) string { return "not declared on " + plan }

var staleMarkerRe = regexp.MustCompile(`(^#\s*|;\s*)not declared on \S+`)

// SyncTarget applies e to target name under keyPath in the YAML file at path,
// keeping comments and key order. Adding a name that already exists, marking
// a marked name or unmarking an unmarked one is a no-op, so the returned
// change lists only real edits. The file is written only when write is true
// and something changed.
func SyncTarget(path string, keyPath []string, name string, e TargetEdit, write bool) (TargetChange, error) {
	var ch TargetChange
	doc, err := loadDoc(path)
	if err != nil {
		return ch, err
	}
	target, err := findTarget(doc, path, keyPath, name)
	if err != nil {
		return ch, err
	}
	key, defaults, err := targetVars(target, keyPath)
	if err != nil {
		return ch, errs.Configf("preset %q in %s: %v", name, path, err)
	}
	ch.Marked = markStale(defaults, e.Mark, StaleMarker(e.Plan))
	ch.Unmarked = unmarkStale(defaults, e.Unmark)
	if ch.Added, err = addDefaults(target, key, defaults, e.Add); err != nil {
		return ch, err
	}
	if !write || ch.Empty() {
		return ch, nil
	}
	return ch, saveDoc(path, doc)
}

// findTarget returns the mapping of target name under keyPath in doc.
func findTarget(doc *yaml.Node, path string, keyPath []string, name string) (*yaml.Node, error) {
	parent := doc.Content[0]
	for _, k := range keyPath {
		if parent = mappingValue(parent, k); parent == nil || parent.Kind != yaml.MappingNode {
			return nil, errs.Configf("no %s in %s", strings.Join(keyPath, "."), path)
		}
	}
	target := mappingValue(parent, name)
	if target == nil || target.Kind != yaml.MappingNode {
		return nil, errs.Configf("no preset %q under %s in %s", name, strings.Join(keyPath, "."), path)
	}
	return target, nil
}

// targetVars returns the key target keeps its variables under, preferring
// whichever of "variables" and "defaults" is already present, and that
// mapping, or nil when it is missing or has no value.
func targetVars(target *yaml.Node, keyPath []string) (string, *yaml.Node, error) {
	key := VarsKey(keyPath)
	if mappingValue(target, "variables") != nil {
		key = "variables"
	} else if mappingValue(target, "defaults") != nil {
		key = "defaults"
	}
	defaults := mappingValue(target, key)
	if defaults == nil || defaults.Tag == "!!null" {
		return key, nil, nil // "defaults:" with no value; ensureMapping replaces it
	}
	if defaults.Kind != yaml.MappingNode {
		return key, nil, fmt.Errorf("%s must be a mapping", key)
	}
	return key, defaults, nil
}

// markStale appends marker to the line comment of each name in defaults
// that is not marked yet, and returns the names it marked.
func markStale(defaults *yaml.Node, names []string, marker string) []string {
	var marked []string
	for _, n := range names {
		v := mappingValue(defaults, n)
		if v == nil || staleMarkerRe.MatchString(v.LineComment) {
			continue
		}
		if c := strings.TrimSpace(v.LineComment); c != "" {
			v.LineComment = c + "; " + marker
		} else {
			v.LineComment = "# " + marker
		}
		marked = append(marked, n)
	}
	return marked
}

// unmarkStale removes the stale marker from each name in defaults that has
// one, keeping the rest of its comment, and returns the names it unmarked.
func unmarkStale(defaults *yaml.Node, names []string) []string {
	var unmarked []string
	for _, n := range names {
		v := mappingValue(defaults, n)
		if v == nil || !staleMarkerRe.MatchString(v.LineComment) {
			continue
		}
		c := strings.TrimSpace(staleMarkerRe.ReplaceAllString(v.LineComment, ""))
		if c != "" && !strings.HasPrefix(c, "#") {
			c = "# " + c
		}
		v.LineComment = c
		unmarked = append(unmarked, n)
	}
	return unmarked
}

// addDefaults appends each var not already in defaults to target's key
// mapping, creating it when defaults is nil, and returns the names it added.
func addDefaults(target *yaml.Node, key string, defaults *yaml.Node, vars []DraftVar) ([]string, error) {
	var added []string
	for _, d := range vars {
		if mappingValue(defaults, d.Name) != nil {
			continue
		}
		s := yamlKey(d.Name) + ": " + quote(d.Value)
		if d.Comment != "" {
			s += "  # " + d.Comment
		}
		var snippet yaml.Node
		if err := yaml.Unmarshal([]byte(s), &snippet); err != nil {
			return added, fmt.Errorf("generated YAML for %q does not parse: %w", d.Name, err)
		}
		if defaults == nil {
			defaults = ensureMapping(target, key)
		}
		// A flow mapping such as "variables: {}" cannot hold the line
		// comments sync writes, so it becomes a block mapping.
		defaults.Style &^= yaml.FlowStyle
		m := snippet.Content[0]
		defaults.Content = append(defaults.Content, m.Content[0], m.Content[1])
		added = append(added, d.Name)
	}
	return added, nil
}
