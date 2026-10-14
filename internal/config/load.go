package config

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/r0jjames/bam-cli/internal/errs"
	"gopkg.in/yaml.v3"
)

// ProjectFileName is the committed per-repository config file.
const ProjectFileName = ".bam.yaml"

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
// workDir outside a repository.
func Discover(workDir, home string) (projectPath, repoRoot string, err error) {
	workDir, err = filepath.Abs(workDir)
	if err != nil {
		return "", "", err
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
		return nil, errs.Configf("cannot resolve the working directory").Wrap(err)
	}
	cfg := &Config{ProjectPath: projectPath, MachinePath: o.MachineFile, PresetsPath: o.PresetsFile, RepoRoot: root, Home: o.Home}
	cfg.Env = Env{Server: o.Getenv("BAM_SERVER"), URL: o.Getenv("BAM_URL"), Token: o.Getenv("BAM_TOKEN")}

	if projectPath != "" {
		data, err := os.ReadFile(projectPath)
		if err != nil {
			return nil, errs.Configf("cannot read %s", projectPath).Wrap(err)
		}
		if err := checkProjectCredentialKeys(data, projectPath); err != nil {
			return nil, err
		}
		var pf ProjectFile
		if err := decodeStrict(data, projectPath, &pf); err != nil {
			return nil, err
		}
		if err := checkDecodedProjectAuthEnv(&pf, projectPath); err != nil {
			return nil, err
		}
		if err := checkVersion(pf.Version, projectPath); err != nil {
			return nil, err
		}
		if err := normalizeTargets(projectPath, "targets", pf.Targets); err != nil {
			return nil, err
		}
		cfg.Project = &pf
	}

	cfg.Machine = &MachineFile{Version: 1}
	if o.MachineFile != "" {
		data, err := os.ReadFile(o.MachineFile)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, errs.Configf("cannot read %s", o.MachineFile).Wrap(err)
		default:
			var mf MachineFile
			if err := decodeStrict(data, o.MachineFile, &mf); err != nil {
				return nil, err
			}
			if err := checkVersion(mf.Version, o.MachineFile); err != nil {
				return nil, err
			}
			if err := normalizeTargets(o.MachineFile, "targets", mf.Targets); err != nil {
				return nil, err
			}
			for _, root := range sortedKeys(mf.Repos) {
				if err := normalizeTargets(o.MachineFile, "repos."+root+".targets", mf.Repos[root].Targets); err != nil {
					return nil, err
				}
			}
			cfg.Machine = &mf
		}
	}

	cfg.Presets = &PresetsFile{}
	if o.PresetsFile != "" {
		pf, err := loadPresets(o.PresetsFile)
		if err != nil {
			return nil, err
		}
		cfg.Presets = pf
		if cfg.PresetsDirFiles, err = loadPresetsDir(PresetsDir(o.PresetsFile)); err != nil {
			return nil, err
		}
		if err := checkDuplicatePresets(cfg); err != nil {
			return nil, err
		}
	}
	if err := validateFiles(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func decodeStrict(data []byte, path string, out any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errs.Configf("%s is empty", path).WithTry("add a line: version: 1")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return errs.Configf("invalid %s", path).WithWhy(err.Error()).Wrap(err)
	}
	return nil
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
		if st, err := os.Stat(path); err != nil || st.IsDir() {
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
		for _, name := range sortedKeys(s.Presets) {
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
	for _, name := range sortedKeys(targets) {
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
