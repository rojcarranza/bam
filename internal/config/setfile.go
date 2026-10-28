package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/r0jjames/bam-cli/internal/errs"
	"gopkg.in/yaml.v3"
)

// setFile is a train file: one set, with an optional name.
type setFile struct {
	Name    string   `yaml:"name,omitempty"`
	Timeout Duration `yaml:"timeout,omitempty"`
	Stages  []Stage  `yaml:"stages"`
}

// UnmarshalYAML takes a bare list of stages as shorthand for stages:, as Set does.
func (f *setFile) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.SequenceNode:
		return n.Decode(&f.Stages)
	case yaml.MappingNode:
		// Decode does not inherit the file's KnownFields; see Stage.
		if err := knownFields(n, reflect.TypeFor[setFile]()); err != nil {
			return err
		}
		type plain setFile
		return n.Decode((*plain)(f))
	}
	return fmt.Errorf("line %d: expected a train file (stages: [[preset], ...]), found a single value", n.Line)
}

// LoadSetFile reads one set from a train file and checks it as a configured
// set is checked, against the merged presets. Literal secrets are refused,
// as in the project file: a train file is shared like .bam.yaml.
func (c *Config) LoadSetFile(path string) (ResolvedSet, error) { return c.LoadSetFileNamed(path, "") }

// LoadSetFileNamed is LoadSetFile with the name a file without name: takes:
// fallback when it is set (the recorded name of a run being resumed, whose
// file may have been renamed), else the file name.
func (c *Config) LoadSetFileNamed(path, fallback string) (ResolvedSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ResolvedSet{}, errs.Configf("cannot read the train file %s", path).WithWhy(err.Error()).Wrap(err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return ResolvedSet{}, errs.Configf("%s is empty", path).WithTry("add stages: [[<preset>]]")
	}
	var f setFile
	if err := decodeStrict(data, path, &f); err != nil {
		return ResolvedSet{}, err
	}
	name := f.Name
	if name == "" {
		name = fallback
	}
	if name == "" {
		base := filepath.Base(path)
		name = strings.TrimSuffix(base, filepath.Ext(base))
		if !ValidTargetName(name) {
			return ResolvedSet{}, errs.Configf("%s: the file name %q is not a set name", path, name).
				WithTry(fmt.Sprintf("add name: <name> to %s; names match ^[a-z][a-z0-9_-]*$", path))
		}
	}
	if !ValidTargetName(name) {
		return ResolvedSet{}, errs.Configf("%s: invalid set name %q", path, name).
			WithTry("names match ^[a-z][a-z0-9_-]*$")
	}
	set := Set{Stages: f.Stages, Timeout: f.Timeout}
	if err := checkSetSecrets(path, map[string]Set{name: set}); err != nil {
		return ResolvedSet{}, err
	}
	targets, err := c.Targets()
	if err != nil {
		return ResolvedSet{}, err
	}
	rs := ResolvedSet{Name: name, Set: set, DefinedIn: []string{path}}
	if err := validateSet(rs, targets); err != nil {
		return ResolvedSet{}, err
	}
	return rs, nil
}
