package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/r0jjames/bam-cli/internal/errs"
	"gopkg.in/yaml.v3"
)

// WriteTarget adds target d under keyPath in the YAML file at path, keeping
// existing comments and key order. The file is created when missing.
func WriteTarget(path string, keyPath []string, d TargetDraft, force bool) error {
	doc, err := loadDocVersion(path, VarsKey(keyPath) == "defaults")
	if err != nil {
		return err
	}
	parent := doc.Content[0]
	for _, k := range keyPath {
		parent = ensureMapping(parent, k)
	}
	if mappingValue(parent, d.Name) != nil && !force {
		return errs.Usagef("preset %q already exists in %s", d.Name, path).WithTry("pass --force to replace it")
	}
	var snippet yaml.Node
	if err := yaml.Unmarshal([]byte(d.RenderAs("", VarsKey(keyPath))), &snippet); err != nil {
		return fmt.Errorf("generated YAML for preset %q does not parse: %w", d.Name, err)
	}
	m := snippet.Content[0]
	setKey(parent, m.Content[0], m.Content[1])
	if VarsKey(keyPath) == "variables" {
		return saveDocMode(path, doc, 0o600) // the presets file may hold literal secrets
	}
	return saveDoc(path, doc)
}

// SetMachineServer adds or replaces servers.<alias> in the machine file.
func SetMachineServer(path, alias string, s Server) error {
	doc, err := loadDoc(path)
	if err != nil {
		return err
	}
	servers := ensureMapping(doc.Content[0], "servers")
	var body yaml.Node
	if err := body.Encode(s); err != nil {
		return err
	}
	setKey(servers, scalar(alias), &body)
	return saveDoc(path, doc)
}

// RemoveMachineServer deletes servers.<alias> from the machine file, along
// with a default_server or repos.<root>.server that names it.
func RemoveMachineServer(path, alias string) error {
	doc, err := loadDoc(path)
	if err != nil {
		return err
	}
	servers := mappingValue(doc.Content[0], "servers")
	if !deleteKey(servers, alias) {
		return errs.Usagef("no server alias %q in %s", alias, path).WithTry("bam server list")
	}
	// A default or repos entry still naming the alias would fail every command.
	if v := mappingValue(doc.Content[0], "default_server"); v != nil && v.Value == alias {
		deleteKey(doc.Content[0], "default_server")
	}
	if repos := mappingValue(doc.Content[0], "repos"); repos != nil {
		for i := 1; i < len(repos.Content); i += 2 {
			if v := mappingValue(repos.Content[i], "server"); v != nil && v.Value == alias {
				deleteKey(repos.Content[i], "server")
			}
		}
	}
	return saveDoc(path, doc)
}

// InitFile is what bam init writes besides targets.
type InitFile struct {
	ServerAlias string
	ServerURL   string
	Projects    []string
}

// WriteInitFile writes a new .bam.yaml.
func WriteInitFile(path string, f InitFile, drafts []TargetDraft, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		return errs.Usagef("%s already exists", path).WithTry("pass --force to overwrite it, or use bam preset add")
	}
	var b strings.Builder
	b.WriteString("version: 1\n")
	b.WriteString("servers:\n  " + yamlKey(f.ServerAlias) + ":\n    url: " + quote(f.ServerURL) + "\n")
	b.WriteString("default_server: " + yamlKey(f.ServerAlias) + "\n")
	if len(f.Projects) > 0 {
		b.WriteString("projects: [" + strings.Join(f.Projects, ", ") + "]\n")
	}
	if len(drafts) > 0 {
		b.WriteString("targets:\n")
		for _, d := range drafts {
			b.WriteString(d.Render("  "))
		}
	}
	var check yaml.Node
	if err := yaml.Unmarshal([]byte(b.String()), &check); err != nil {
		return fmt.Errorf("generated .bam.yaml does not parse: %w", err)
	}
	return writeAtomic(path, []byte(b.String()), 0o644)
}

func loadDoc(path string) (*yaml.Node, error) { return loadDocVersion(path, true) }

// loadDocVersion is loadDoc; a new document starts with "version: 1" only
// when version is true.
func loadDocVersion(path string, version bool) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, errs.Configf("cannot read %s", path).Wrap(err)
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, errs.Configf("invalid %s", path).WithWhy(err.Error()).Wrap(err)
		}
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if version {
			setKey(root, scalar("version"), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"})
		}
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, errs.Configf("%s must be a YAML mapping", path)
	}
	return &doc, nil
}

func saveDoc(path string, doc *yaml.Node) error { return saveDocMode(path, doc, 0o644) }

// saveDocMode is saveDoc with the mode a new file gets; an existing file
// keeps its own.
func saveDocMode(path string, doc *yaml.Node, newMode fs.FileMode) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	mode := newMode
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	return writeAtomic(path, buf.Bytes(), mode)
}

func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return errs.Configf("cannot create %s", filepath.Dir(path)).Wrap(err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bam-*.tmp")
	if err != nil {
		return errs.Configf("cannot write %s", path).Wrap(err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errs.Configf("cannot write %s", path).Wrap(err)
	}
	if err := tmp.Close(); err != nil {
		return errs.Configf("cannot write %s", path).Wrap(err)
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return errs.Configf("cannot write %s", path).Wrap(err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errs.Configf("cannot write %s", path).Wrap(err)
	}
	return nil
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

// ensureMapping returns the mapping under key, creating or replacing a null value.
func ensureMapping(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			if v.Kind != yaml.MappingNode {
				v = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				m.Content[i+1] = v
			}
			return v
		}
	}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content, scalar(key), v)
	return v
}

func setKey(m *yaml.Node, key, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key.Value {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content, key, value)
}

func deleteKey(m *yaml.Node, key string) bool {
	if m == nil {
		return false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}
