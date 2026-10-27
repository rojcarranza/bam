// Package config loads, validates, merges and edits bam's configuration
// files. It never touches the network or the keychain.
package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// ProjectFile is .bam.yaml, committed with a repository.
type ProjectFile struct {
	Version       int               `yaml:"version"`
	Servers       map[string]Server `yaml:"servers,omitempty"`
	DefaultServer string            `yaml:"default_server,omitempty"`
	Projects      []string          `yaml:"projects,omitempty"`
	// DefaultProject is the project the terminal UI opens on.
	DefaultProject string              `yaml:"default_project,omitempty"`
	Targets        map[string]Target   `yaml:"targets,omitempty"`
	Sets           map[string]Set      `yaml:"sets,omitempty"`
	Children       map[string][]string `yaml:"children,omitempty"`
}

// MachineFile is the per-machine config.yaml, never committed.
type MachineFile struct {
	Version       int                 `yaml:"version"`
	DefaultServer string              `yaml:"default_server,omitempty"`
	Servers       map[string]Server   `yaml:"servers,omitempty"`
	Targets       map[string]Target   `yaml:"targets,omitempty"`
	Sets          map[string]Set      `yaml:"sets,omitempty"`
	Repos         map[string]Repo     `yaml:"repos,omitempty"`
	Children      map[string][]string `yaml:"children,omitempty"`
	Color         string              `yaml:"color,omitempty"`
	Pager         string              `yaml:"pager,omitempty"`
	Editor        string              `yaml:"editor,omitempty"`
}

type Server struct {
	URL      string   `yaml:"url,omitempty"`
	AuthEnv  string   `yaml:"auth_env,omitempty"`
	Projects []string `yaml:"projects,omitempty"`
	// DefaultProject is the project the terminal UI opens on.
	DefaultProject string `yaml:"default_project,omitempty"`
}

// PresetsFile is the schema of the personal presets file, ~/bam/presets.yaml
// by default, and of each file in the presets directory.
// Its presets apply in every directory.
type PresetsFile struct {
	Version int               `yaml:"version,omitempty"`
	Presets map[string]Target `yaml:"presets,omitempty"`
}

// Target is a run preset.
type Target struct {
	Plan     string    `yaml:"plan,omitempty"`
	Server   string    `yaml:"server,omitempty"`
	Branch   string    `yaml:"branch,omitempty"`
	Defaults StringMap `yaml:"defaults,omitempty"`
	// Variables is the newer name of Defaults. Load moves it into Defaults,
	// so the rest of bam reads Defaults only.
	Variables StringMap     `yaml:"variables,omitempty"`
	Options   StringListMap `yaml:"options,omitempty"`
	Required  []string      `yaml:"required,omitempty"`
	Watch     *bool         `yaml:"watch,omitempty"`
	Timeout   Duration      `yaml:"timeout,omitempty"`
}

type Repo struct {
	Server   string   `yaml:"server,omitempty"`
	Projects []string `yaml:"projects,omitempty"`
	// DefaultProject is the project the terminal UI opens on.
	DefaultProject string              `yaml:"default_project,omitempty"`
	Targets        map[string]Target   `yaml:"targets,omitempty"`
	Sets           map[string]Set      `yaml:"sets,omitempty"`
	Children       map[string][]string `yaml:"children,omitempty"`
}

// StringMap decodes any YAML scalar values as strings; Bamboo variables are strings.
type StringMap map[string]string

func (m *StringMap) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: expected a mapping", n.Line)
	}
	out := StringMap{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if v.Kind != yaml.ScalarNode {
			return fmt.Errorf("line %d: value of %q must be a single value", v.Line, k.Value)
		}
		out[k.Value] = v.Value
	}
	*m = out
	return nil
}

// StringListMap decodes a mapping of name to a list of scalars.
type StringListMap map[string][]string

func (m *StringListMap) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: expected a mapping", n.Line)
	}
	out := StringListMap{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if v.Kind != yaml.SequenceNode {
			return fmt.Errorf("line %d: options for %q must be a list", v.Line, k.Value)
		}
		vals := make([]string, 0, len(v.Content))
		for _, item := range v.Content {
			if item.Kind != yaml.ScalarNode {
				return fmt.Errorf("line %d: options for %q must be single values", item.Line, k.Value)
			}
			vals = append(vals, item.Value)
		}
		out[k.Value] = vals
	}
	*m = out
	return nil
}

// Duration parses Go duration strings such as "45m".
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil || v < 0 {
		return fmt.Errorf("line %d: %q is not a duration like 45m or 1h30m", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// Set is a named release train: stages run in order, runs inside a stage
// run in parallel.
type Set struct {
	Stages  []Stage  `yaml:"stages"`
	Timeout Duration `yaml:"timeout,omitempty"`
}

// Stage is one step of a set. A bare name is shorthand for {target: name}.
type Stage []SetRun

func (s *Stage) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.SequenceNode {
		return fmt.Errorf("line %d: a stage is a list of presets", n.Line)
	}
	out := make(Stage, 0, len(n.Content))
	for _, item := range n.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			out = append(out, SetRun{Target: item.Value})
		case yaml.MappingNode:
			// Decode does not inherit the file's KnownFields, so a typo such
			// as env: instead of vars: is caught here.
			for i := 0; i+1 < len(item.Content); i += 2 {
				if k := item.Content[i]; k.Value != "target" && k.Value != "vars" && k.Value != "matrix" {
					return fmt.Errorf("line %d: unknown field %q in a stage run", k.Line, k.Value)
				}
			}
			var r SetRun
			if err := item.Decode(&r); err != nil {
				return err
			}
			out = append(out, r)
		default:
			return fmt.Errorf("line %d: a stage run is a preset name or {target, vars, matrix}", item.Line)
		}
	}
	*s = out
	return nil
}

// SetRun is one preset run of a stage, with variables that override the
// preset's defaults for this set only.
type SetRun struct {
	Target string    `yaml:"target"`
	Vars   StringMap `yaml:"vars,omitempty"`
	Matrix *Matrix   `yaml:"matrix,omitempty"`
}

// Matrix expands one set run into several. Exactly one form is set:
// Product, a map of value lists (every combination), or Sets, a list of
// variable maps (each one run).
type Matrix struct {
	Product map[string][]string
	Sets    []StringMap
}

func (m *Matrix) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.MappingNode:
		var p StringListMap
		if err := n.Decode(&p); err != nil {
			return err
		}
		if len(p) == 0 {
			return fmt.Errorf("line %d: a matrix needs at least one variable", n.Line)
		}
		for k, vs := range p {
			if len(vs) == 0 {
				return fmt.Errorf("line %d: matrix variable %q has no values", n.Line, k)
			}
		}
		m.Product = p
	case yaml.SequenceNode:
		if len(n.Content) == 0 {
			return fmt.Errorf("line %d: a matrix needs at least one variable set", n.Line)
		}
		for _, item := range n.Content {
			if item.Kind != yaml.MappingNode {
				return fmt.Errorf("line %d: a matrix entry is a map of variables", item.Line)
			}
			var vs StringMap
			if err := item.Decode(&vs); err != nil {
				return err
			}
			m.Sets = append(m.Sets, vs)
		}
	default:
		return fmt.Errorf("line %d: a matrix is a map of value lists or a list of variable maps", n.Line)
	}
	return nil
}
