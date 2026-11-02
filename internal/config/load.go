package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/r0jjames/bam-cli/internal/errs"
	"gopkg.in/yaml.v3"
)

// ProjectFileName is the committed per-repository config file.
const ProjectFileName = ".bam.yaml"

// misnamedProjectFiles are names people give the project file by mistake.
// bam reads none of them, so finding one is an error rather than silence.
// Only the dotted .bam.yml is listed: an undotted bam.yaml or bam.yml is an
// ordinary file other tools and projects may own, so bam ignores it.
var misnamedProjectFiles = []string{".bam.yml"}

// Env holds the BAM_* environment variables that affect server selection.
type Env struct {
	Server string // BAM_SERVER
	URL    string // BAM_URL
	Token  string // BAM_TOKEN
}

// PresetsSource is one file of the presets directory and its presets.
type PresetsSource struct {
	Path    string
	Presets map[string]Target
}

// Config is both layers as loaded, before merging.
type Config struct {
	Project     *ProjectFile // nil when the repository has no .bam.yaml
	Machine     *MachineFile // never nil
	Presets     *PresetsFile // never nil
	ProjectPath string
	MachinePath string
	PresetsPath string // the personal presets file, whether or not it exists
	// PresetsDirFiles are the files of PresetsDir(PresetsPath), sorted by path.
	PresetsDirFiles []PresetsSource
	RepoRoot        string // git root, or the working directory outside a repository
	Home            string // user home, for ~ in repos keys
	Env             Env
}

type LoadOptions struct {
	WorkDir     string
	Home        string
	MachineFile string
	PresetsFile string // see LocatePresets; empty means none
	Getenv      func(string) string
}

// Discover finds .bam.yaml by walking up from workDir. The walk stops at the
// git root inside a repository, else at home. repoRoot is the git root, or
// workDir outside a repository. A misnamed project file in its place, such
// as .bam.yml, is a config error.
func Discover(workDir, home string) (projectPath, repoRoot string, err error) {
	workDir, err = filepath.Abs(workDir)
	if err != nil {
		return "", "", errs.Configf("cannot resolve the working directory").Wrap(err)
	}
	gitRoot := findUp(workDir, "", ".git")
	stop := gitRoot
	repoRoot = gitRoot
	if gitRoot == "" {
		repoRoot = workDir
		stop = workDir
		if home != "" && within(workDir, filepath.Clean(home)) {
			stop = filepath.Clean(home)
		}
	}
	if dir := findUp(workDir, stop, ProjectFileName); dir != "" {
		projectPath = filepath.Join(dir, ProjectFileName)
		return projectPath, repoRoot, nil
	}
	for _, name := range misnamedProjectFiles {
		if dir := findUp(workDir, stop, name); dir != "" {
			found := filepath.Join(dir, name)
			return "", "", errs.Configf("found %s, but bam reads only %s", found, ProjectFileName).
				WithWhy("bam ignores a project config under any other name").
				WithTry(fmt.Sprintf("rename it: mv %s %s", found, filepath.Join(dir, ProjectFileName)))
		}
	}
	return projectPath, repoRoot, nil
}

// findUp returns the first directory from start upwards that contains name.
// It never goes above stop when stop is set.
func findUp(start, stop, name string) string {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return dir
		}
		if dir == stop {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Load discovers and parses both layers and reads the BAM_* variables.
func Load(o LoadOptions) (*Config, error) {
	projectPath, root, err := Discover(o.WorkDir, o.Home)
	if err != nil {
		return nil, err
	}
	cfg := &Config{ProjectPath: projectPath, MachinePath: o.MachineFile, PresetsPath: o.PresetsFile, RepoRoot: root, Home: o.Home}
	cfg.Env = Env{Server: o.Getenv("BAM_SERVER"), URL: o.Getenv("BAM_URL"), Token: o.Getenv("BAM_TOKEN")}
	if cfg.Project, err = readProjectFile(projectPath); err != nil {
		return nil, err
	}
	if cfg.Machine, err = readMachineFile(o.MachineFile); err != nil {
		return nil, err
	}
	if err := loadPresetLayer(cfg, o.PresetsFile); err != nil {
		return nil, err
	}
	if err := validateFiles(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// readProjectFile reads the project config at path, or returns nil when there is none.
func readProjectFile(path string) (*ProjectFile, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.Configf("cannot read %s", path).Wrap(err)
	}
	if err := checkProjectCredentialKeys(data, path); err != nil {
		return nil, err
	}
	var pf ProjectFile
	if err := decodeStrict(data, path, &pf); err != nil {
		return nil, err
	}
	if err := checkDecodedProjectAuthEnv(&pf, path); err != nil {
		return nil, err
	}
	if err := checkVersion(pf.Version, path); err != nil {
		return nil, err
	}
	if err := normalizeTargets(path, "targets", pf.Targets); err != nil {
		return nil, err
	}
	return &pf, nil
}

// readMachineFile reads the machine config at path; a missing file (or no path)
// gives an empty version 1 config.
func readMachineFile(path string) (*MachineFile, error) {
	empty := &MachineFile{Version: 1}
	if path == "" {
		return empty, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return nil, errs.Configf("cannot read %s", path).Wrap(err)
	}
	var mf MachineFile
	if err := decodeStrict(data, path, &mf); err != nil {
		return nil, err
	}
	if err := checkVersion(mf.Version, path); err != nil {
		return nil, err
	}
	if err := normalizeTargets(path, "targets", mf.Targets); err != nil {
		return nil, err
	}
	for _, root := range slices.Sorted(maps.Keys(mf.Repos)) {
		if err := normalizeTargets(path, "repos."+root+".targets", mf.Repos[root].Targets); err != nil {
			return nil, err
		}
	}
	return &mf, nil
}

// loadPresetLayer fills cfg's presets file and presets directory files.
func loadPresetLayer(cfg *Config, path string) error {
	cfg.Presets = &PresetsFile{}
	if path == "" {
		return nil
	}
	pf, err := loadPresets(path)
	if err != nil {
		return err
	}
	cfg.Presets = pf
	if cfg.PresetsDirFiles, err = loadPresetsDir(PresetsDir(path)); err != nil {
		return err
	}
	return checkDuplicatePresets(cfg)
}

func decodeStrict(data []byte, path string, out any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errs.Configf("%s is empty", path).WithTry("add a line: version: 1")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		e := errs.Configf("invalid %s", path).WithWhy(plainShapes(err.Error())).Wrap(err)
		if mv := (*multiValueError)(nil); errors.As(err, &mv) {
			e = e.WithTry(fmt.Sprintf("in a preset, list allowed values under options: {%s: [a, b]} and must-set names under required: [%s]", mv.name, mv.name))
		} else if p := yamlUnknownField.FindStringSubmatch(err.Error()); p != nil {
			if t, ok := fieldPlaces[p[3]]; ok {
				if near := errs.Nearest(p[2], yamlFields(t.typ)); near != "" {
					e = e.WithTry(fmt.Sprintf("did you mean %s?", near))
				}
			}
		}
		return e
	}
	return nil
}

// yamlTypeError matches yaml.v3's "line N: cannot unmarshal !!tag[ `value`] into T".
var yamlTypeError = regexp.MustCompile("line (\\d+): cannot unmarshal !!(\\w+)( `[^`]*`)? into (\\S+)")

// yamlUnknownField matches yaml.v3's "line N: field X not found in type T".
var yamlUnknownField = regexp.MustCompile(`line (\d+): field (\S+) not found in type (\S+)`)

// fieldPlaces names, in plain words, the Go types yaml.v3 puts in its
// unknown-field errors, and keeps each type to suggest its real fields.
var fieldPlaces = map[string]struct {
	where string
	typ   reflect.Type
}{
	"config.MachineFile": {"the machine config", reflect.TypeFor[MachineFile]()},
	"config.ProjectFile": {"the project config", reflect.TypeFor[ProjectFile]()},
	"config.PresetsFile": {"the presets file", reflect.TypeFor[PresetsFile]()},
	"config.Target":      {"a preset", reflect.TypeFor[Target]()},
	"config.Server":      {"a server", reflect.TypeFor[Server]()},
	"config.Repo":        {"a repository entry", reflect.TypeFor[Repo]()},
	"config.Set":         {"a set", reflect.TypeFor[Set]()},
	"config.SetRun":      {"a stage run", reflect.TypeFor[SetRun]()},
	"config.setFile":     {"a train file", reflect.TypeFor[setFile]()},
}

// knownFields refuses mapping keys that are not yaml fields of t, in
// yaml.v3's own wording so decodeStrict words it and suggests a field.
// Node.Decode does not inherit the file's KnownFields, so types with their
// own UnmarshalYAML check here.
func knownFields(n *yaml.Node, t reflect.Type) error {
	fields := yamlFields(t)
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i]; !slices.Contains(fields, k.Value) {
			return fmt.Errorf("line %d: field %s not found in type %s", k.Line, k.Value, t)
		}
	}
	return nil
}

// yamlFields lists the yaml field names of a struct type.
func yamlFields(t reflect.Type) []string {
	var out []string
	for f := range t.Fields() {
		if name, _, _ := strings.Cut(f.Tag.Get("yaml"), ","); name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}

// plainShapes rewrites yaml type and unknown-field errors, which name Go
// types, into plain words; lines it does not recognise stay as they are.
func plainShapes(msg string) string {
	msg = yamlUnknownField.ReplaceAllStringFunc(msg, func(m string) string {
		p := yamlUnknownField.FindStringSubmatch(m)
		place, ok := fieldPlaces[p[3]]
		if !ok {
			return m
		}
		return fmt.Sprintf("line %s: unknown field %q in %s", p[1], p[2], place.where)
	})
	found := map[string]string{"seq": "a list", "map": "a mapping"}
	expected := map[string]string{
		"config.Target": "a preset (plan: KEY, ...)",
		"config.Server": "a server (url: URL, ...)",
		"config.Repo":   "a repository entry (server: ALIAS, ...)",
		"[]string":      "a list",
		"string":        "a single value",
		"int":           "a number",
		"bool":          "true or false",
	}
	return yamlTypeError.ReplaceAllStringFunc(msg, func(m string) string {
		p := yamlTypeError.FindStringSubmatch(m)
		want, ok := expected[p[4]]
		if strings.HasPrefix(p[4], "map[") {
			want, ok = "a mapping", true
		}
		if !ok {
			return m
		}
		got, ok := found[p[2]]
		if !ok {
			got = "a single value"
		}
		return "line " + p[1] + ": expected " + want + ", found " + got + p[3]
	})
}

func checkVersion(v int, path string) error {
	if v == 0 {
		return errs.Configf("missing version in %s", path).WithTry("add a line: version: 1")
	}
	if v != 1 {
		return errs.Configf("%s has version %d", path, v).
			WithWhy("this bam understands version 1").
			WithTry("upgrade bam, or check the file")
	}
	return nil
}

// loadPresets reads the presets file. A missing or empty file has no presets;
// version is optional there, since people write the file by hand.
func loadPresets(path string) (*PresetsFile, error) {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &PresetsFile{}, nil
	case err != nil:
		return nil, errs.Configf("cannot read %s", path).Wrap(err)
	case len(bytes.TrimSpace(data)) == 0:
		return &PresetsFile{}, nil
	}
	var pf PresetsFile
	if err := decodeStrict(data, path, &pf); err != nil {
		return nil, err
	}
	if pf.Version != 0 {
		if err := checkVersion(pf.Version, path); err != nil {
			return nil, err
		}
	}
	if err := normalizeTargets(path, "presets", pf.Presets); err != nil {
		return nil, err
	}
	return &pf, nil
}

// loadPresetsDir reads every *.yaml and *.yml in dir, sorted by name. A
// missing directory has no presets; other files, subdirectories and hidden
// files are ignored.
func loadPresetsDir(dir string) ([]PresetsSource, error) {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, errs.Configf("cannot read the presets directory %s", dir).Wrap(err)
	}
	var out []PresetsSource
	for _, e := range entries {
		if !IsPresetsFileName(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		st, err := os.Stat(path)
		if err != nil {
			return nil, errs.Configf("cannot read %s", path).Wrap(err)
		}
		if st.IsDir() {
			continue
		}
		pf, err := loadPresets(path)
		if err != nil {
			return nil, err
		}
		out = append(out, PresetsSource{Path: path, Presets: pf.Presets})
	}
	return out, nil
}

// IsPresetsFileName reports whether a file in the presets directory is read:
// a .yaml or .yml file that is not hidden.
func IsPresetsFileName(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return !strings.HasPrefix(name, ".") && (ext == ".yaml" || ext == ".yml")
}

// presetsSources lists the presets file and every file of the presets
// directory, the presets file first.
func (c *Config) presetsSources() []PresetsSource {
	var out []PresetsSource
	if c.Presets != nil {
		out = append(out, PresetsSource{Path: c.PresetsPath, Presets: c.Presets.Presets})
	}
	return append(out, c.PresetsDirFiles...)
}

// IsPresetsPath reports whether path is the presets file or a file of the
// presets directory.
func (c *Config) IsPresetsPath(path string) bool {
	for _, s := range c.presetsSources() {
		if s.Path == path {
			return true
		}
	}
	return false
}

// checkDuplicatePresets refuses a preset name defined in two presets files:
// names are global, and no presets file outranks another.
func checkDuplicatePresets(c *Config) error {
	seen := map[string]string{}
	for _, s := range c.presetsSources() {
		for _, name := range slices.Sorted(maps.Keys(s.Presets)) {
			if first, ok := seen[name]; ok {
				return errs.Configf("preset %q is defined in both %s and %s", name, first, s.Path).
					WithWhy("preset names are shared by every presets file, so one name can live in one file only").
					WithTry("remove or rename one of them")
			}
			seen[name] = s.Path
		}
	}
	return nil
}

// normalizeTargets moves variables into defaults, its older name, and
// rejects a preset that sets both.
func normalizeTargets(path, where string, targets map[string]Target) error {
	for _, name := range slices.Sorted(maps.Keys(targets)) {
		t := targets[name]
		if t.Variables == nil {
			continue
		}
		if t.Defaults != nil {
			return errs.Configf("%s: %s.%s has both variables and defaults", path, where, name).
				WithWhy("they are two names for the same map").
				WithTry("keep variables and move the defaults entries into it")
		}
		t.Defaults, t.Variables = t.Variables, nil
		targets[name] = t
	}
	return nil
}
