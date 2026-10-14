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
	parent := doc.Content[0]
	for _, k := range keyPath {
		if parent = mappingValue(parent, k); parent == nil || parent.Kind != yaml.MappingNode {
			return ch, errs.Configf("no %s in %s", strings.Join(keyPath, "."), path)
		}
	}
	target := mappingValue(parent, name)
	if target == nil || target.Kind != yaml.MappingNode {
		return ch, errs.Configf("no preset %q under %s in %s", name, strings.Join(keyPath, "."), path)
	}

	key := VarsKey(keyPath)
	if mappingValue(target, "variables") != nil {
		key = "variables"
	} else if mappingValue(target, "defaults") != nil {
		key = "defaults"
	}
	defaults := mappingValue(target, key)
	if defaults != nil && defaults.Tag == "!!null" {
		defaults = nil // "defaults:" with no value; ensureMapping replaces it
	}
	if defaults != nil && defaults.Kind != yaml.MappingNode {
		return ch, errs.Configf("preset %q in %s: %s must be a mapping", name, path, key)
	}
	marker := StaleMarker(e.Plan)
	for _, n := range e.Mark {
		v := mappingValue(defaults, n)
		if v == nil || staleMarkerRe.MatchString(v.LineComment) {
			continue
		}
		if c := strings.TrimSpace(v.LineComment); c != "" {
			v.LineComment = c + "; " + marker
		} else {
			v.LineComment = "# " + marker
		}
		ch.Marked = append(ch.Marked, n)
	}
	for _, n := range e.Unmark {
		v := mappingValue(defaults, n)
		if v == nil || !staleMarkerRe.MatchString(v.LineComment) {
			continue
		}
		c := strings.TrimSpace(staleMarkerRe.ReplaceAllString(v.LineComment, ""))
		if c != "" && !strings.HasPrefix(c, "#") {
			c = "# " + c
		}
		v.LineComment = c
		ch.Unmarked = append(ch.Unmarked, n)
	}
	for _, d := range e.Add {
		if mappingValue(defaults, d.Name) != nil {
			continue
		}
		s := yamlKey(d.Name) + ": " + quote(d.Value)
		if d.Comment != "" {
			s += "  # " + d.Comment
		}
		var snippet yaml.Node
		if err := yaml.Unmarshal([]byte(s), &snippet); err != nil {
			return ch, fmt.Errorf("generated YAML for %q does not parse: %w", d.Name, err)
		}
		if defaults == nil {
			defaults = ensureMapping(target, key)
		}
		m := snippet.Content[0]
		defaults.Content = append(defaults.Content, m.Content[0], m.Content[1])
		ch.Added = append(ch.Added, d.Name)
	}
	if !write || ch.Empty() {
		return ch, nil
	}
	return ch, saveDoc(path, doc)
}
