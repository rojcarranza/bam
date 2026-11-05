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
	Comment string // trailing warning, only "not declared by the plan"
	// Commented writes the line as a YAML comment, for a secret the plan
	// already stores: uncommenting it overrides the stored value.
	Commented bool
}

// TargetDraft is a generated target, rendered for a human to review.
type TargetDraft struct {
	Name   string
	Plan   string
	Branch string // written uncommented only when the user chose one
	Vars   []DraftVar
	// BuildPlan is the plan or plan branch key whose builds the preset runs;
	// it is not rendered.
	BuildPlan string
	Note      string // explanation when no variables could be read
	// Server is the alias written as an active server line; empty writes a
	// commented example instead.
	Server string
	// Repos are the repositories of the plan branch with the branch each
	// builds now. They are written active only when Branch is set and every
	// repository has a branch; otherwise commented. Empty writes a placeholder.
	Repos []DraftRepo
}

// DraftRepo is one repository of a generated target.
type DraftRepo struct {
	Name   string
	Branch string // empty when bam could not read it
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
	if d.Server != "" {
		line(1, "server: "+yamlKey(d.Server))
	}
	if d.Branch != "" {
		line(1, "branch: "+quote(d.Branch))
	}
	// Repositories are written only with a plan branch. When bam cannot read
	// a repository's branch they are commented, so the names are not lost.
	if len(d.Repos) > 0 && d.Branch != "" {
		active := true
		for _, r := range d.Repos {
			active = active && r.Branch != ""
		}
		prefix, depth := "# ", 1
		if active {
			prefix, depth = "", 2
		}
		line(1, prefix+"repositories:")
		for _, r := range d.Repos {
			branch := r.Branch
			if branch == "" {
				branch = "BRANCH"
			}
			line(depth, prefix+yamlKey(r.Name)+": "+quote(branch))
		}
	}
	if len(d.Vars) > 0 {
		line(1, varsKey+":")
		for _, v := range d.Vars {
			s := yamlKey(v.Name) + ": " + quote(v.Value)
			if v.Commented {
				s = "# " + s
			}
			if v.Comment != "" {
				s += "  # " + v.Comment
			}
			line(2, s)
		}
	}
	line(1, "watch: true")
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
