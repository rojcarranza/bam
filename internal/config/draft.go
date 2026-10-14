package config

import (
	"encoding/json"
	"regexp"
	"strings"
)

// DraftVar is one variable of a generated target.
type DraftVar struct {
	Name    string
	Value   string // literal value or ${ENV} reference
	Comment string // trailing comment explaining where the value came from
}

// TargetDraft is a generated target, rendered with comments for a human to review.
type TargetDraft struct {
	Name     string
	Plan     string
	Branch   string // written uncommented only when the user chose one
	Vars     []DraftVar
	Required []string // suggestion, written commented out
	Note     string   // explanation when no variables could be read
}

var plainKeyRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func yamlKey(k string) string {
	if plainKeyRe.MatchString(k) {
		return k
	}
	return quote(k)
}

// quote returns a double-quoted YAML string; JSON strings are valid YAML.
func quote(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Render returns "name:" and the target body, every line prefixed by indent.
func (d TargetDraft) Render(indent string) string { return d.RenderAs(indent, "defaults") }

// RenderAs is Render with the variables map under varsKey: "defaults" in
// .bam.yaml and config.yaml, "variables" in the presets file.
func (d TargetDraft) RenderAs(indent, varsKey string) string {
	var b strings.Builder
	line := func(depth int, s string) {
		b.WriteString(indent + strings.Repeat("  ", depth) + s + "\n")
	}
	line(0, yamlKey(d.Name)+":")
	line(1, "plan: "+d.Plan)
	if d.Note != "" {
		line(1, "# "+d.Note)
	}
	if d.Branch != "" {
		line(1, "branch: "+quote(d.Branch))
	} else {
		line(1, "# branch: develop")
	}
	if len(d.Vars) > 0 {
		line(1, varsKey+":")
		var options []string
		for _, v := range d.Vars {
			s := yamlKey(v.Name) + ": " + quote(v.Value)
			if v.Comment != "" {
				s += "  # " + v.Comment
			}
			line(2, s)
			if _, ref := EnvRef(v.Value); !ref && v.Value != "" {
				options = append(options, "#   "+yamlKey(v.Name)+": ["+quote(v.Value)+"]")
			}
		}
		if len(options) > 0 {
			line(1, "# options:")
			for _, o := range options {
				line(1, o)
			}
		}
	}
	if len(d.Required) > 0 {
		line(1, "# required: ["+strings.Join(d.Required, ", ")+"]")
	}
	line(1, "# watch: true")
	return b.String()
}

// DraftYAML returns a standalone document holding only this target, as the
// map at key ("targets" or "presets") would hold it.
func DraftYAML(key string, d TargetDraft) string {
	return key + ":\n" + d.RenderAs("  ", VarsKey([]string{key}))
}

// VarsKey is the variables key a preset uses under keyPath: "variables" in
// the presets file, "defaults" elsewhere.
func VarsKey(keyPath []string) string {
	if len(keyPath) == 1 && keyPath[0] == "presets" {
		return "variables"
	}
	return "defaults"
}
