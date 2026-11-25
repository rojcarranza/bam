package config

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rojcarranza/bam/internal/errs"
	"gopkg.in/yaml.v3"
)

// VarValue is one variable bam preset edit sets.
type VarValue struct{ Name, Value string }

// RepoBranch is one repository of a preset and the branch it builds.
type RepoBranch struct{ Name, Branch string }

// PresetEdit is what bam preset edit changes in one file's entry of a preset.
// A nil pointer or map leaves that part alone; an empty value removes it.
type PresetEdit struct {
	Set          []VarValue
	Unset        []string
	Branch       *string
	Repositories *[]RepoBranch
	Required     *[]string
	Options      map[string][]string // a variable's allowed values; none removes them
	Timeout      *string             // a duration like 45m; empty removes it
	Rename       string
}

// FieldChange is one change an edit made or would make. Old and New are
// empty for a field that did not exist or no longer does.
type FieldChange struct {
	Field string `json:"field"`
	Old   string `json:"old,omitempty"`
	New   string `json:"new,omitempty"`
}

// EditTarget applies e to preset name under keyPath in the YAML file at path,
// changing only the lines it names: comments, key order and every other
// preset stay. It refuses a preset that uses YAML anchors or aliases and a
// literal value for a secret variable. The file is written only when write is
// true and something changed; the returned changes list only real edits.
func EditTarget(path string, keyPath []string, name string, e PresetEdit, write bool) ([]FieldChange, error) {
	doc, err := loadDoc(path)
	if err != nil {
		return nil, err
	}
	parent, target, err := findTargetIn(doc, path, keyPath, name)
	if err != nil {
		return nil, err
	}
	if err := refuseAnchors(target, name, path); err != nil {
		return nil, err
	}
	for _, v := range e.Set {
		if _, ok := EnvRef(v.Value); !ok && IsMaskedName(v.Name) {
			return nil, errs.Usagef("%s is a secret: bam never writes its value to a file", v.Name).
				WithWhy("a secret is stored as a ${ENV_NAME} reference and read from your shell at run time").
				WithTry(fmt.Sprintf("--var %s='${%s}'", v.Name, EnvNameFor(v.Name)))
		}
	}
	key, vars, err := targetVars(target, keyPath)
	if err != nil {
		return nil, errs.Configf("preset %q in %s: %v", name, path, err)
	}
	var ch []FieldChange
	ch, err = editVars(target, key, vars, e, ch)
	if err != nil {
		return nil, err
	}
	ch = editBranch(target, e, ch)
	ch = editRepositories(target, e, ch)
	ch = editRequired(target, e, ch)
	ch = editOptions(target, e, ch)
	if ch, err = editTimeout(target, e, ch); err != nil {
		return nil, err
	}
	if err := checkEdited(target, name, path); err != nil {
		return nil, err
	}
	if e.Rename != "" && e.Rename != name {
		if ch, err = renameKey(parent, name, e.Rename, path, ch); err != nil {
			return nil, err
		}
	}
	if !write || len(ch) == 0 {
		return ch, nil
	}
	return ch, saveDoc(path, doc)
}

func findTargetIn(doc *yaml.Node, path string, keyPath []string, name string) (parent, target *yaml.Node, err error) {
	parent = doc.Content[0]
	for _, k := range keyPath {
		if parent = mappingValue(parent, k); parent == nil || parent.Kind != yaml.MappingNode {
			return nil, nil, errs.Configf("no %s in %s", strings.Join(keyPath, "."), path)
		}
	}
	target = mappingValue(parent, name)
	if target != nil && target.Kind == yaml.AliasNode {
		return nil, nil, anchorError(name, path)
	}
	if target == nil || target.Kind != yaml.MappingNode {
		return nil, nil, errs.Configf("no preset %q under %s in %s", name, strings.Join(keyPath, "."), path)
	}
	return parent, target, nil
}

func anchorError(name, path string) error {
	return errs.Usagef("preset %q in %s uses YAML anchors or aliases", name, path).
		WithWhy("changing one line could change every preset that shares the anchor").
		WithTry("spell the preset out in " + path + ", then run bam preset edit again")
}

// refuseAnchors fails when any node of the preset is an anchor, an alias or a
// merge key, because a line edited there reaches other presets or is not the
// line the person sees.
func refuseAnchors(n *yaml.Node, name, path string) error {
	if n.Anchor != "" || n.Kind == yaml.AliasNode || (n.Kind == yaml.ScalarNode && n.Tag == "!!merge") {
		return anchorError(name, path)
	}
	for _, c := range n.Content {
		if err := refuseAnchors(c, name, path); err != nil {
			return err
		}
	}
	return nil
}

func editVars(target *yaml.Node, key string, vars *yaml.Node, e PresetEdit, ch []FieldChange) ([]FieldChange, error) {
	for _, v := range e.Set {
		cur := mappingValue(vars, v.Name)
		if cur == nil {
			added, err := addDefaults(target, key, vars, []DraftVar{{Name: v.Name, Value: v.Value}})
			if err != nil {
				return ch, err
			}
			if len(added) > 0 {
				vars = mappingValue(target, key)
				ch = append(ch, FieldChange{Field: "variables." + v.Name, New: v.Value})
			}
			continue
		}
		if cur.Kind != yaml.ScalarNode {
			return ch, errs.Configf("variable %s is not a single value in the preset", v.Name)
		}
		if cur.Value == v.Value {
			continue
		}
		ch = append(ch, FieldChange{Field: "variables." + v.Name, Old: cur.Value, New: v.Value})
		cur.Value, cur.Tag, cur.Style = v.Value, "!!str", yaml.DoubleQuotedStyle
	}
	for _, n := range e.Unset {
		if old := mappingValue(vars, n); old != nil && deleteKey(vars, n) {
			ch = append(ch, FieldChange{Field: "variables." + n, Old: old.Value})
		}
	}
	return ch, nil
}

// setScalar sets key in m to value in double quotes, keeping the key's
// position and comments, and reports the old value. An empty value removes it.
func setScalar(m *yaml.Node, key, value string) (old string, changed bool) {
	cur := mappingValue(m, key)
	switch {
	case cur == nil && value == "":
		return "", false
	case cur == nil:
		m.Content = append(m.Content, scalar(key), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle})
		return "", true
	case value == "":
		deleteKey(m, key)
		return cur.Value, true
	case cur.Value == value && cur.Kind == yaml.ScalarNode:
		return cur.Value, false
	}
	old = cur.Value
	cur.Kind, cur.Tag, cur.Value, cur.Style, cur.Content = yaml.ScalarNode, "!!str", value, yaml.DoubleQuotedStyle, nil
	return old, true
}

func editBranch(target *yaml.Node, e PresetEdit, ch []FieldChange) []FieldChange {
	if e.Branch == nil {
		return ch
	}
	if old, changed := setScalar(target, "branch", *e.Branch); changed {
		ch = append(ch, FieldChange{Field: "branch", Old: old, New: *e.Branch})
	}
	return ch
}

func editTimeout(target *yaml.Node, e PresetEdit, ch []FieldChange) ([]FieldChange, error) {
	if e.Timeout == nil {
		return ch, nil
	}
	if *e.Timeout != "" {
		if d, err := time.ParseDuration(*e.Timeout); err != nil || d < 0 {
			return ch, errs.Usagef("%q is not a duration like 45m or 1h30m", *e.Timeout)
		}
	}
	if old, changed := setScalar(target, "timeout", *e.Timeout); changed {
		ch = append(ch, FieldChange{Field: "timeout", Old: old, New: *e.Timeout})
	}
	return ch, nil
}

func editRepositories(target *yaml.Node, e PresetEdit, ch []FieldChange) []FieldChange {
	if e.Repositories == nil {
		return ch
	}
	repos := mappingValue(target, "repositories")
	if repos == nil || repos.Kind != yaml.MappingNode {
		if len(*e.Repositories) == 0 {
			return ch
		}
		repos = ensureMapping(target, "repositories")
	}
	want := map[string]string{}
	for _, r := range *e.Repositories {
		want[r.Name] = r.Branch
	}
	for i := 0; i+1 < len(repos.Content); {
		n := repos.Content[i].Value
		if b, ok := want[n]; !ok {
			ch = append(ch, FieldChange{Field: "repositories." + n, Old: repos.Content[i+1].Value})
			repos.Content = append(repos.Content[:i], repos.Content[i+2:]...)
			continue
		} else if old, changed := setScalar(repos, n, b); changed {
			ch = append(ch, FieldChange{Field: "repositories." + n, Old: old, New: b})
		}
		i += 2
	}
	for _, r := range *e.Repositories {
		if mappingValue(repos, r.Name) == nil {
			setScalar(repos, r.Name, r.Branch)
			ch = append(ch, FieldChange{Field: "repositories." + r.Name, New: r.Branch})
		}
	}
	if len(repos.Content) == 0 {
		deleteKey(target, "repositories")
	}
	return ch
}

func seqValues(n *yaml.Node) []string {
	if n == nil {
		return nil
	}
	var out []string
	for _, c := range n.Content {
		out = append(out, c.Value)
	}
	return out
}

// setList sets key in m to a list of values and reports the old ones. An
// empty list removes the key. A list that already holds the values is kept.
func setList(m *yaml.Node, key string, values []string) (old []string, changed bool) {
	cur := mappingValue(m, key)
	old = seqValues(cur)
	if slices.Equal(old, values) && (cur == nil || cur.Kind == yaml.SequenceNode) {
		return old, false
	}
	if len(values) == 0 {
		deleteKey(m, key)
		return old, true
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	if cur != nil && cur.Kind == yaml.SequenceNode {
		seq.Style = cur.Style
	}
	for _, v := range values {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Style: yaml.DoubleQuotedStyle})
	}
	if cur != nil {
		seq.HeadComment, seq.LineComment, seq.FootComment = cur.HeadComment, cur.LineComment, cur.FootComment
		*cur = *seq
	} else {
		m.Content = append(m.Content, scalar(key), seq)
	}
	return old, true
}

func editRequired(target *yaml.Node, e PresetEdit, ch []FieldChange) []FieldChange {
	if e.Required == nil {
		return ch
	}
	if old, changed := setList(target, "required", *e.Required); changed {
		ch = append(ch, FieldChange{Field: "required", Old: strings.Join(old, ", "), New: strings.Join(*e.Required, ", ")})
	}
	return ch
}

func editOptions(target *yaml.Node, e PresetEdit, ch []FieldChange) []FieldChange {
	for _, n := range sortedKeys(e.Options) {
		opts := mappingValue(target, "options")
		if opts == nil || opts.Kind != yaml.MappingNode {
			if len(e.Options[n]) == 0 {
				continue
			}
			opts = ensureMapping(target, "options")
		}
		opts.Style &^= yaml.FlowStyle
		if old, changed := setList(opts, n, e.Options[n]); changed {
			ch = append(ch, FieldChange{Field: "options." + n, Old: strings.Join(old, ", "), New: strings.Join(e.Options[n], ", ")})
		}
		if len(opts.Content) == 0 {
			deleteKey(target, "options")
		}
	}
	return ch
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// checkEdited decodes the edited preset and applies the file rules to it, so
// a bad edit never reaches the disk: repositories need a branch, and a
// variable with options must hold one of them.
func checkEdited(target *yaml.Node, name, path string) error {
	var t Target
	if err := target.Decode(&t); err != nil {
		return errs.Configf("preset %q in %s would not load after the edit", name, path).WithWhy(err.Error())
	}
	for k, v := range t.Variables {
		if t.Defaults == nil {
			t.Defaults = StringMap{}
		}
		t.Defaults[k] = v
	}
	if t.Plan == "" {
		t.Plan = "X-X" // the plan may come from another layer
	}
	t.Server = ""
	if err := validateTarget(ResolvedTarget{Name: name, Target: t, DefinedIn: []string{path}}, nil); err != nil {
		return errs.Usagef("%s", err.Error()).WithWhy("nothing was written")
	}
	return nil
}

func renameKey(parent *yaml.Node, from, to, path string, ch []FieldChange) ([]FieldChange, error) {
	if !ValidTargetName(to) {
		return ch, errs.Usagef("%q is not a valid preset name", to).WithTry("lowercase letters, digits and - or _")
	}
	if mappingValue(parent, to) != nil {
		return ch, errs.Usagef("preset %q already exists in %s", to, path)
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == from {
			parent.Content[i].Value = to
			return append(ch, FieldChange{Field: "name", Old: from, New: to}), nil
		}
	}
	return ch, nil
}
