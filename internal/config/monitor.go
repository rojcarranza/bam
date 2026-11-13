package config

import (
	"maps"
	"slices"
	"strings"

	"github.com/rojcarranza/bam/internal/errs"
	"gopkg.in/yaml.v3"
)

// PinGroup is one named group of pinned pipelines, in the order the files
// list them.
type PinGroup struct {
	Name string
	Pins []string
}

// Monitor returns the pinned pipelines of bam top: the project's monitor:
// groups, then the machine's, merged by group name. A pin is a plan key
// (PROJ-PLAN), a plan with a branch (PROJ-PLAN@branch) or the name of a
// preset. The result is checked: group names, pin shapes and no pin twice.
// Whether a preset exists depends on the repository bam runs in, so an
// unknown preset is left for the board to report on that pin alone.
func (c *Config) Monitor() ([]PinGroup, error) {
	byName := map[string][]string{}
	layer := func(path string, m map[string][]string) error {
		for _, name := range slices.Sorted(maps.Keys(m)) {
			if !ValidTargetName(name) {
				return errs.Configf("%s: monitor group %q is not a valid name", path, name).
					WithWhy("group names are lowercase letters, digits, - and _, starting with a letter")
			}
			if len(m[name]) == 0 {
				return errs.Configf("%s: monitor group %q has no pins", path, name).
					WithTry("list plan keys or presets under monitor." + name + ", or remove the group")
			}
			seen := map[string]bool{}
			for _, pin := range m[name] {
				if seen[pin] {
					return errs.Configf("%s: monitor group %q pins %s twice", path, name, pin)
				}
				seen[pin] = true
				if !slices.Contains(byName[name], pin) {
					byName[name] = append(byName[name], pin)
				}
			}
		}
		return nil
	}
	if c.Project != nil {
		if err := layer(c.ProjectPath, c.Project.Monitor); err != nil {
			return nil, err
		}
	}
	if err := layer(c.MachinePath, c.Machine.Monitor); err != nil {
		return nil, err
	}
	if len(byName) == 0 {
		return nil, nil
	}
	var out []PinGroup
	where := map[string]string{} // pin → group that holds it
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		for _, pin := range byName[name] {
			if why := checkPin(pin); why != "" {
				return nil, errs.Configf("monitor group %q: %s", name, why).
					WithTry("a pin is a plan key like PROJ-PLAN, PROJ-PLAN@branch, or a preset name")
			}
			if g, dup := where[pin]; dup {
				return nil, errs.Configf("monitor: %s is pinned in %q and in %q", pin, g, name)
			}
			where[pin] = name
		}
		out = append(out, PinGroup{Name: name, Pins: byName[name]})
	}
	return out, nil
}

// SplitPin splits a pin into its plan key or preset name and its branch.
func SplitPin(pin string) (plan, branch string) {
	plan, branch, _ = strings.Cut(pin, "@")
	return plan, branch
}

// checkPin says what is wrong with the shape of pin, or "" when it is a plan
// key, a plan key with a branch, or a preset name.
func checkPin(pin string) string {
	plan, branch := SplitPin(pin)
	switch {
	case childPlanKeyRe.MatchString(plan):
		if strings.Contains(pin, "@") && branch == "" {
			return pin + " names no branch after @"
		}
		return ""
	case branch == "" && ValidTargetName(plan):
		return ""
	}
	return pin + " is not a plan key, a plan with a branch, or a preset"
}

// CheckPin says why pin cannot be pinned: it is not a plan key, a plan key
// with a branch, or an existing preset. It returns nil for a good pin.
func (c *Config) CheckPin(pin string) error {
	if why := checkPin(pin); why != "" {
		return errs.Usagef("%s", why).WithTry("a pin is a plan key like PROJ-PLAN, PROJ-PLAN@branch, or a preset name")
	}
	if plan, _ := SplitPin(pin); !childPlanKeyRe.MatchString(plan) {
		targets, err := c.Targets()
		if err != nil {
			return err
		}
		if _, ok := targets[plan]; !ok {
			return errs.Usagef("%s is not a preset", pin).WithTry("bam preset list, or a plan key like PROJ-PLAN")
		}
	}
	return nil
}

// ProjectPins reports whether the project file's monitor: holds pin.
func (c *Config) ProjectPins(pin string) bool {
	if c.Project == nil {
		return false
	}
	for _, pins := range c.Project.Monitor {
		if slices.Contains(pins, pin) {
			return true
		}
	}
	return false
}

// AddPin adds pin to group in the monitor: map of the YAML file at path,
// keeping the file's comments. It does nothing when the group has the pin.
func AddPin(path, group, pin string) error {
	doc, err := loadDoc(path)
	if err != nil {
		return err
	}
	groups := ensureMapping(doc.Content[0], "monitor")
	list := mappingValue(groups, group)
	if list == nil || list.Kind != yaml.SequenceNode {
		list = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		setKey(groups, scalar(group), list)
	}
	for _, n := range list.Content {
		if n.Value == pin {
			return nil
		}
	}
	list.Content = append(list.Content, scalar(pin))
	return saveDoc(path, doc)
}

// RemovePin removes pin from the monitor: map of the YAML file at path and
// drops a group left empty, keeping the rest of the file and its comments.
// It reports false when the file did not hold the pin.
func RemovePin(path, pin string) (bool, error) {
	doc, err := loadDoc(path)
	if err != nil {
		return false, err
	}
	groups := mappingValue(doc.Content[0], "monitor")
	if groups == nil {
		return false, nil
	}
	found := false
	for i := 0; i+1 < len(groups.Content); {
		list := groups.Content[i+1]
		list.Content = slices.DeleteFunc(list.Content, func(n *yaml.Node) bool {
			if n.Value == pin {
				found = true
				return true
			}
			return false
		})
		if len(list.Content) == 0 {
			groups.Content = slices.Delete(groups.Content, i, i+2)
			continue
		}
		i += 2
	}
	if !found {
		return false, nil
	}
	if len(groups.Content) == 0 {
		deleteKey(doc.Content[0], "monitor")
	}
	return true, saveDoc(path, doc)
}
