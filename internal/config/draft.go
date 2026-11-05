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
	// Commented writes the line as a YAML comment, for a secret the plan
	// already stores: uncommenting it overrides the stored value.
	Commented bool
}

// TargetDraft is a generated target, rendered with comments for a human to review.
type TargetDraft struct {
	Name     string
	Plan     string
	Branch   string // written uncommented only when the user chose one
	Vars     []DraftVar
	Required []string // variables with no plan default, written as an active required line
	Note     string   // explanation when no variables could be read
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
		line(1, "server: "+yamlKey(d.Server)+"  # the server this preset runs on")
	} else {
		line(1, "# server: work  # server alias to run on; default is the one bam picks")
	}
	if d.Branch != "" {
		line(1, "branch: "+quote(d.Branch))
	} else {
		line(1, "# branch: develop  # plan branch to run; default is the main plan")
	}
	repos := d.Repos
	if len(repos) == 0 {
		repos = []DraftRepo{{Name: "repo-a"}}
	}
	active := d.Branch != "" && len(d.Repos) > 0
	for _, r := range repos {
		active = active && r.Branch != ""
	}
	prefix, depth := "#   ", 1
	if active {
		line(1, "repositories:  # branch each repository of the plan branch builds; bam asks you to switch it when the last build differs")
		prefix, depth = "", 2
	} else {
		line(1, "# repositories:  # branch each repository of the plan branch builds; needs branch:")
	}
	for _, r := range repos {
		branch := r.Branch
		if branch == "" {
			branch = "BRANCH"
		}
		line(depth, prefix+yamlKey(r.Name)+": "+quote(branch))
	}
	var options []string
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
			if _, ref := EnvRef(v.Value); !ref && v.Value != "" {
				options = append(options, "#   "+yamlKey(v.Name)+": ["+quote(v.Value)+"]")
			}
		}
	}
	line(1, "# options:  # allowed values per variable; a run with another value is refused")
	if len(options) == 0 {
		name := "NAME"
		if len(d.Vars) > 0 {
			name = yamlKey(d.Vars[0].Name)
		}
		options = []string{"#   " + name + `: ["a", "b"]`}
	}
	for _, o := range options {
		line(1, o)
	}
	if len(d.Required) > 0 {
		line(1, "required: ["+strings.Join(d.Required, ", ")+"]  # a run with an empty value is refused")
	} else {
		name := "NAME"
		if len(d.Vars) > 0 {
			name = yamlKey(d.Vars[0].Name)
		}
		line(1, "# required: ["+name+"]  # refuse a run where these have no value")
	}
	line(1, "watch: true  # follow the build without --watch; --watch=false skips it")
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
