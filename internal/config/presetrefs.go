package config

import (
	"os"
	"slices"

	"github.com/rojcarranza/bam/internal/errs"
	"gopkg.in/yaml.v3"
)

// RenameRefs renames the preset from to to wherever the project file and the
// machine file name it: the stages of the sets that apply here, and the pins
// of bam top. Each reference is changed in place, so comments and key order
// stay. Both files are checked before either is written, so a refusal leaves
// them as they were; write false only reports. A train file given to bam set
// run --file is not a configured file and is left alone.
func (c *Config) RenameRefs(from, to string, write bool) ([]FieldChange, error) {
	repoKey, _, _ := c.repoEntry()
	var paths []string
	for _, p := range []string{c.ProjectPath, c.MachinePath} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	type edited struct {
		path string
		doc  *yaml.Node
	}
	var all []FieldChange
	var docs []edited
	for _, p := range paths {
		doc, err := loadDoc(p)
		if err != nil {
			return nil, err
		}
		ch, err := renameRefsIn(doc, p, repoKey, from, to)
		if err != nil {
			return nil, err
		}
		if len(ch) > 0 {
			docs = append(docs, edited{p, doc})
			all = append(all, ch...)
		}
	}
	if write {
		for _, d := range docs {
			if err := saveDoc(d.path, d.doc); err != nil {
				return all, err
			}
		}
	}
	return all, nil
}

// renameRefsIn renames the references in one document and lists them, one
// change per set and per pin group.
func renameRefsIn(doc *yaml.Node, path, repoKey, from, to string) ([]FieldChange, error) {
	root := doc.Content[0]
	var ch []FieldChange
	hit := func(n *yaml.Node, field string, seen map[string]bool) error {
		if n.Anchor != "" {
			return errs.Usagef("%s names preset %q in a YAML anchor", path, from).
				WithWhy("changing the anchor could change every place that reuses it").
				WithTry("spell it out in " + path + ", then run bam preset edit again")
		}
		n.Value = to
		if !seen[field] {
			seen[field] = true
			ch = append(ch, FieldChange{Field: field, Old: from, New: to, File: path})
		}
		return nil
	}
	seen := map[string]bool{}
	setsNodes := []*yaml.Node{mappingValue(root, "sets")}
	if repoKey != "" {
		if repos := mappingValue(root, "repos"); repos != nil {
			setsNodes = append(setsNodes, mappingValue(mappingValue(repos, repoKey), "sets"))
		}
	}
	for _, sets := range setsNodes {
		if sets == nil || sets.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(sets.Content); i += 2 {
			name, set := sets.Content[i].Value, sets.Content[i+1]
			stages := set
			if set.Kind == yaml.MappingNode {
				stages = mappingValue(set, "stages")
			}
			if stages == nil || stages.Kind != yaml.SequenceNode {
				continue
			}
			for _, stage := range stages.Content {
				for _, run := range stage.Content {
					node := run
					if run.Kind == yaml.MappingNode {
						node = mappingValue(run, "target")
					}
					if node != nil && node.Kind == yaml.ScalarNode && node.Value == from {
						if err := hit(node, "sets."+name, seen); err != nil {
							return nil, err
						}
					}
				}
			}
		}
		// An alias hides a stage from the walk above; decoding the edited
		// sets finds any run that still names the old preset.
		var decoded map[string]Set
		if sets.Decode(&decoded) == nil {
			for name, s := range decoded {
				for _, st := range s.Stages {
					if slices.ContainsFunc(st, func(r SetRun) bool { return r.Target == from }) {
						return nil, errs.Usagef("set %q in %s names preset %q through a YAML alias", name, path, from).
							WithWhy("changing one line could change every set that shares the alias").
							WithTry("spell the stage out in " + path + ", then run bam preset edit again")
					}
				}
			}
		}
	}
	groups := mappingValue(root, "monitor")
	if groups != nil && groups.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(groups.Content); i += 2 {
			pins := groups.Content[i+1]
			for _, pin := range pins.Content {
				if pin.Kind == yaml.ScalarNode && pin.Value == from {
					if err := hit(pin, "monitor."+groups.Content[i].Value, seen); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	// An alias hides a group or a pin from the walk, as it hides a stage.
	var pins map[string][]string
	if groups != nil && groups.Decode(&pins) == nil {
		for name, list := range pins {
			if slices.Contains(list, from) {
				return nil, errs.Usagef("monitor group %q in %s pins preset %q through a YAML alias", name, path, from).
					WithWhy("changing one line could change every group that shares the alias").
					WithTry("spell the pins out in " + path + ", then run bam preset edit again")
			}
		}
	}
	return ch, nil
}
