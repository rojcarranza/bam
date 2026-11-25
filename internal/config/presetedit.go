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
// It is EditTargetOver for a preset no other file defines.
func EditTarget(path string, keyPath []string, name string, e PresetEdit, write bool) ([]FieldChange, error) {
	return EditTargetOver(path, keyPath, name, nil, e, write)
}

// EditTargetOver is EditTarget for a preset that other files define too:
// below are their entries, lowest precedence first, which this file's entry
// overrides. The edited preset is checked as bam will load it, merged with
// below, so a write that would break every preset command is refused. A
// removal this file cannot make because a lower file holds the value is
// refused, naming that file.
func EditTargetOver(path string, keyPath []string, name string, below []TargetLayer, e PresetEdit, write bool) ([]FieldChange, error) {
	doc, err := loadDoc(path)
	if err != nil {
		return nil, err
	}
	parent, target, err := findTarget(doc, path, keyPath, name)
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
	if err := refuseHeldBelow(decodeTarget(target), below, e, name, path); err != nil {
		return nil, err
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
	ch = editRepositories(target, effectiveRepos(below), e, ch)
	ch = editRequired(target, e, ch)
	ch = editOptions(target, e, ch)
	if ch, err = editTimeout(target, e, ch); err != nil {
		return nil, err
	}
	if err := checkEdited(target, below, name, path); err != nil {
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

// CheckEditable fails when preset name in the file at path cannot be edited
// in place, for the reasons EditTarget refuses it. It reads and writes nothing else.
func CheckEditable(path string, keyPath []string, name string) error {
	doc, err := loadDoc(path)
	if err != nil {
		return err
	}
	_, target, err := findTarget(doc, path, keyPath, name)
	if err != nil {
		return err
	}
	return refuseAnchors(target, name, path)
}

// RenameTarget renames preset name to to in the file at path and checks
// nothing else: another file's copy of a preset may be only a fragment.
func RenameTarget(path string, keyPath []string, name, to string, write bool) error {
	doc, err := loadDoc(path)
	if err != nil {
		return err
	}
	parent, target, err := findTarget(doc, path, keyPath, name)
	if err != nil {
		return err
	}
	if err := refuseAnchors(target, name, path); err != nil {
		return err
	}
	if _, err := renameKey(parent, name, to, path, nil); err != nil {
		return err
	}
	if !write {
		return nil
	}
	return saveDoc(path, doc)
}

// decodeTarget reads the preset node as bam loads it, with "variables" folded
// into Defaults. A node that does not decode gives the zero Target; checkEdited
// reports that after the edit.
func decodeTarget(n *yaml.Node) Target {
	var t Target
	_ = n.Decode(&t)
	for k, v := range t.Variables {
		if t.Defaults == nil {
			t.Defaults = StringMap{}
		}
		t.Defaults[k] = v
	}
	t.Variables = nil
	return t
}

// refuseHeldBelow fails on a removal this file cannot make: the value is not
// in this file but in a lower one, so removing it here would do nothing.
func refuseHeldBelow(top Target, below []TargetLayer, e PresetEdit, name, path string) error {
	holder := func(has func(Target) bool) string {
		for i := len(below) - 1; i >= 0; i-- {
			if has(below[i].Target) {
				return below[i].Path
			}
		}
		return ""
	}
	refuse := func(what, file string) error {
		return errs.Usagef("%s of preset %q is set in %s, not in %s", what, name, file, path).
			WithWhy("bam edits the file whose copy of the preset it uses, and removing a line there leaves the other file's value in place").
			WithTry("change it in " + file + ", or set a value here")
	}
	for _, n := range e.Unset {
		if _, here := top.Defaults[n]; here {
			continue
		}
		if f := holder(func(t Target) bool { _, ok := t.Defaults[n]; return ok }); f != "" {
			return refuse("variable "+n, f)
		}
	}
	if e.Branch != nil && *e.Branch == "" && top.Branch == "" {
		if f := holder(func(t Target) bool { return t.Branch != "" }); f != "" {
			return refuse("the branch", f)
		}
	}
	if e.Timeout != nil && *e.Timeout == "" && top.Timeout == 0 {
		if f := holder(func(t Target) bool { return t.Timeout != 0 }); f != "" {
			return refuse("the timeout", f)
		}
	}
	if e.Required != nil && len(*e.Required) == 0 && top.Required == nil {
		if f := holder(func(t Target) bool { return t.Required != nil }); f != "" {
			return refuse("required", f)
		}
	}
	for n, vals := range e.Options {
		if _, here := top.Options[n]; len(vals) == 0 && !here {
			if f := holder(func(t Target) bool { _, ok := t.Options[n]; return ok }); f != "" {
				return refuse("the options of "+n, f)
			}
		}
	}
	if e.Repositories != nil {
		want := map[string]bool{}
		for _, r := range *e.Repositories {
			want[r.Name] = true
		}
		for _, l := range below {
			for r := range l.Target.Repositories {
				if _, here := top.Repositories[r]; !want[r] && !here {
					return refuse("repository "+r, l.Path)
				}
			}
		}
	}
	return nil
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

// effectiveRepos are the repositories the lower files already give the preset.
func effectiveRepos(below []TargetLayer) map[string]string {
	out := map[string]string{}
	for _, l := range below {
		for n, b := range l.Target.Repositories {
			out[n] = b
		}
	}
	return out
}

func editRepositories(target *yaml.Node, held map[string]string, e PresetEdit, ch []FieldChange) []FieldChange {
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
		if mappingValue(repos, r.Name) == nil && held[r.Name] != r.Branch {
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

// checkEdited decodes the edited preset, merges it over the lower files'
// entries and applies the file rules to the result, so a bad edit never
// reaches the disk: repositories need a branch, and a variable with options
// must hold one of them, whichever file holds each.
func checkEdited(target *yaml.Node, below []TargetLayer, name, path string) error {
	var raw Target
	if err := target.Decode(&raw); err != nil {
		return errs.Configf("preset %q in %s would not load after the edit", name, path).WithWhy(err.Error())
	}
	var t Target
	for _, l := range below {
		t = mergeTarget(t, l.Target)
	}
	t = mergeTarget(t, decodeTarget(target))
	if t.Plan == "" {
		t.Plan = "X-X" // a lone fragment: the plan may come from another file
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
