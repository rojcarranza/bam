package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/r0jjames/bam-cli/internal/errs"
	"gopkg.in/yaml.v3"
)

// MigrateMove is a target that moved, or would move, to the presets file.
type MigrateMove struct {
	Name string
	From string // mapping path in the machine file, e.g. "targets" or "repos.<root>.targets"
}

// MigrateSkip is a target that stays in the machine file, and why.
type MigrateSkip struct {
	Name   string
	From   string
	Reason string
}

// MigrateResult lists what MigrateTargets moved and what it left.
type MigrateResult struct {
	Moved   []MigrateMove
	Skipped []MigrateSkip
}

// migrateSource is one target entry in the machine file's YAML tree.
type migrateSource struct {
	from   string
	parent *yaml.Node // the targets mapping holding it
	key    *yaml.Node
	value  *yaml.Node
}

// MigrateTargets moves the machine file's targets, top-level and per
// repository, into the presets file under presets:, renaming defaults to
// variables and keeping comments. A name already in the presets file, or
// defined more than once in the machine file, is left where it is. The files
// are written only when write is true and something moved; a new presets
// file gets mode 0600.
func MigrateTargets(machinePath, presetsPath string, write bool) (MigrateResult, error) {
	var res MigrateResult
	if _, err := os.Stat(machinePath); errors.Is(err, fs.ErrNotExist) {
		return res, nil
	}
	mdoc, err := loadDoc(machinePath)
	if err != nil {
		return res, err
	}
	root := mdoc.Content[0]

	var sources []migrateSource
	collect := func(from string, targets *yaml.Node) {
		if targets == nil || targets.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(targets.Content); i += 2 {
			sources = append(sources, migrateSource{from: from, parent: targets, key: targets.Content[i], value: targets.Content[i+1]})
		}
	}
	collect("targets", mappingValue(root, "targets"))
	repos := mappingValue(root, "repos")
	if repos != nil && repos.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(repos.Content); i += 2 {
			collect("repos."+repos.Content[i].Value+".targets", mappingValue(repos.Content[i+1], "targets"))
		}
	}
	if len(sources) == 0 {
		return res, nil
	}

	// Refuse a presets file bam cannot load before touching it, so a value
	// such as "presets: []" is reported instead of replaced.
	if _, err := loadPresets(presetsPath); err != nil {
		return res, err
	}
	pdoc, err := loadDocVersion(presetsPath, false)
	if err != nil {
		return res, err
	}
	if v := mappingValue(pdoc.Content[0], "presets"); v != nil && v.Kind != yaml.MappingNode && v.Tag != "!!null" {
		return res, errs.Configf("%s: presets must be a mapping", presetsPath)
	}
	presets := ensureMapping(pdoc.Content[0], "presets")

	count := map[string]int{}
	for _, s := range sources {
		count[s.key.Value]++
	}
	for _, s := range sources {
		name := s.key.Value
		switch {
		case count[name] > 1:
			res.Skipped = append(res.Skipped, MigrateSkip{Name: name, From: s.from,
				Reason: fmt.Sprintf("defined in %d places in %s; move it by hand", count[name], machinePath)})
			continue
		case mappingValue(presets, name) != nil:
			res.Skipped = append(res.Skipped, MigrateSkip{Name: name, From: s.from,
				Reason: "already in " + presetsPath})
			continue
		case usesAnchors(s.key) || usesAnchors(s.value):
			res.Skipped = append(res.Skipped, MigrateSkip{Name: name, From: s.from,
				Reason: "uses YAML anchors or aliases; move it by hand"})
			continue
		}
		if k := mappingKey(s.value, "defaults"); k != nil && mappingValue(s.value, "variables") == nil {
			k.Value = "variables"
		}
		presets.Content = append(presets.Content, s.key, s.value)
		deleteKey(s.parent, name)
		res.Moved = append(res.Moved, MigrateMove{Name: name, From: s.from})
	}
	if !write || len(res.Moved) == 0 {
		return res, nil
	}

	pruneEmptyTargets(root)
	if err := checkPresetsDoc(pdoc, presetsPath); err != nil {
		return res, err
	}
	if err := checkMachineDoc(mdoc, machinePath); err != nil {
		return res, err
	}
	// The presets file first: if the machine file then fails to save, the
	// presets exist twice with the same values rather than not at all.
	if err := saveDocMode(presetsPath, pdoc, 0o600); err != nil {
		return res, err
	}
	return res, saveDoc(machinePath, mdoc)
}

// mappingKey returns the key node for key in mapping m.
func mappingKey(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i]
		}
	}
	return nil
}

// pruneEmptyTargets removes targets maps a migration emptied, then repos
// entries left with nothing, then an empty repos map.
func pruneEmptyTargets(root *yaml.Node) {
	empty := func(n *yaml.Node) bool { return n != nil && n.Kind == yaml.MappingNode && len(n.Content) == 0 }
	if empty(mappingValue(root, "targets")) {
		deleteKey(root, "targets")
	}
	repos := mappingValue(root, "repos")
	if repos == nil || repos.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(repos.Content); {
		entry := repos.Content[i+1]
		if empty(mappingValue(entry, "targets")) {
			deleteKey(entry, "targets")
		}
		if empty(entry) {
			repos.Content = append(repos.Content[:i], repos.Content[i+2:]...)
			continue
		}
		i += 2
	}
	if empty(repos) {
		deleteKey(root, "repos")
	}
}

// checkPresetsDoc decodes the document about to be written as a presets
// file, so a migration never writes a file bam would then refuse to load.
func checkPresetsDoc(doc *yaml.Node, path string) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	var pf PresetsFile
	if err := decodeStrict(buf.Bytes(), path, &pf); err != nil {
		return errs.Configf("migrating would leave %s invalid", path).WithWhy(strings.TrimSpace(err.Error())).Wrap(err)
	}
	return normalizeTargets(path, "presets", pf.Presets)
}

// usesAnchors reports whether n or anything under it defines an anchor, is
// an alias, or holds a merge key. Moving such a node to another file could
// leave an alias without its anchor.
func usesAnchors(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	if n.Anchor != "" || n.Kind == yaml.AliasNode || n.Tag == "!!merge" {
		return true
	}
	for _, c := range n.Content {
		if usesAnchors(c) {
			return true
		}
	}
	return false
}

// checkMachineDoc decodes the machine file about to be written, so a
// migration never leaves it unloadable.
func checkMachineDoc(doc *yaml.Node, path string) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	var mf MachineFile
	if err := decodeStrict(buf.Bytes(), path, &mf); err != nil {
		return errs.Configf("migrating would leave %s invalid", path).WithWhy(strings.TrimSpace(err.Error())).Wrap(err)
	}
	return nil
}
