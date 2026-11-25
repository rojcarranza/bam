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
// them as they were; write false only reports. Sets under this repository's
// entry of the machine file are renamed; with everyRepo, a preset every
// repository shares, so are those under any other entry that defines no preset
// of that name. Train files are renamed apart, by RenameInTrainFiles.
func (c *Config) RenameRefs(from, to string, everyRepo, write bool) ([]FieldChange, error) {
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
		ch, err := renameRefsIn(doc, p, repoKey, everyRepo, from, to)
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
func renameRefsIn(doc *yaml.Node, path, repoKey string, everyRepo bool, from, to string) ([]FieldChange, error) {
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
	if repos := mappingValue(root, "repos"); repos != nil && repos.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(repos.Content); i += 2 {
			key, entry := repos.Content[i].Value, repos.Content[i+1]
			// A repository whose own entry defines a preset of this name has
			// another preset by it, so its sets keep theirs.
			if key == repoKey || (everyRepo && mappingValue(mappingValue(entry, "targets"), from) == nil) {
				setsNodes = append(setsNodes, mappingValue(entry, "sets"))
			}
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
			err := renameStageRuns(stages, from, func(n *yaml.Node) error { return hit(n, "sets."+name, seen) })
			if err != nil {
				return nil, err
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

// renameStageRuns calls hit on each scalar of the stages that names preset
// from: a bare name in a stage, or the target of a run.
func renameStageRuns(stages *yaml.Node, from string, hit func(*yaml.Node) error) error {
	for _, stage := range stages.Content {
		for _, run := range stage.Content {
			node := run
			if run.Kind == yaml.MappingNode {
				node = mappingValue(run, "target")
			}
			if node != nil && node.Kind == yaml.ScalarNode && node.Value == from {
				if err := hit(node); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// RenameInTrainFiles renames the preset from to to in the stages of each
// train file in paths, in place, so comments and key order stay. A file that
// is missing or is not a train file is skipped. Every file is checked before
// any is written; write false only reports. A name that carries an anchor
// stops the rename. A stage an alias shares is renamed once, for every use of
// it, since a train file holds one set and nothing else reads it.
func RenameInTrainFiles(paths []string, from, to string, write bool) ([]FieldChange, error) {
	type edited struct {
		path string
		doc  *yaml.Node
	}
	var all []FieldChange
	var docs []edited
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var doc yaml.Node
		if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) == 0 {
			continue
		}
		stages := doc.Content[0]
		if stages.Kind == yaml.MappingNode {
			stages = mappingValue(stages, "stages")
		}
		if stages == nil || stages.Kind != yaml.SequenceNode {
			continue
		}
		renamed := false
		err = renameStageRuns(stages, from, func(n *yaml.Node) error {
			if n.Anchor != "" {
				return errs.Usagef("train file %s names preset %q in a YAML anchor", p, from).
					WithWhy("changing the anchor could change every place that reuses it").
					WithTry("spell it out in " + p + ", then run bam preset edit again")
			}
			n.Value, renamed = to, true
			return nil
		})
		if err != nil {
			return nil, err
		}
		if renamed {
			docs = append(docs, edited{p, &doc})
			all = append(all, FieldChange{Field: "train file", Old: from, New: to, File: p})
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

// Shared says whether t has a copy outside this repository's own files: in a
// presets file or at the top of the machine file. Every repository can then
// reach it, so a rename must follow it into the sets and train files of the
// other repositories too.
func (c *Config) Shared(t ResolvedTarget) bool {
	for _, l := range t.Layers {
		if l.Path != c.ProjectPath && (len(l.KeyPath) == 0 || l.KeyPath[0] != "repos") {
			return true
		}
	}
	return false
}
