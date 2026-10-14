package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func globOf(matches ...string) func(string) ([]string, error) {
	return func(string) ([]string, error) { return matches, nil }
}

func TestLocatePresetsDefaultsToHome(t *testing.T) {
	home := t.TempDir()
	loc := LocatePresets(PresetsLookup{Home: home, Getenv: noEnv})
	assert.Equal(t, filepath.Join(home, "bam", "presets.yaml"), loc.Path)
	assert.False(t, loc.Exists)
	assert.Empty(t, loc.Note)
}

func TestLocatePresetsEnvOverride(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "bam", "presets.yaml"), "presets: {}\n")
	loc := LocatePresets(PresetsLookup{Home: home, Getenv: envOf(map[string]string{"BAM_PRESETS": "/sync/bam/presets.yaml"})})
	assert.Equal(t, "/sync/bam/presets.yaml", loc.Path)
}

func TestLocatePresetsWSLUsesTheWindowsHome(t *testing.T) {
	home := t.TempDir()
	win := filepath.Join(t.TempDir(), "Users", "jdoe", "bam", "presets.yaml")
	writeFile(t, win, "presets: {}\n")
	wsl := envOf(map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})

	loc := LocatePresets(PresetsLookup{Home: home, Getenv: wsl, Glob: globOf(win)})
	assert.Equal(t, win, loc.Path)
	assert.True(t, loc.Exists)

	// A file in the WSL home wins over the Windows one.
	own := filepath.Join(home, "bam", "presets.yaml")
	writeFile(t, own, "presets: {}\n")
	loc = LocatePresets(PresetsLookup{Home: home, Getenv: wsl, Glob: globOf(win)})
	assert.Equal(t, own, loc.Path)
}

func TestLocatePresetsWSLAmbiguous(t *testing.T) {
	home := t.TempDir()
	wsl := envOf(map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
	loc := LocatePresets(PresetsLookup{Home: home, Getenv: wsl,
		Glob: globOf("/mnt/c/Users/jdoe/bam/presets.yaml", "/mnt/c/Users/ops/bam/presets.yaml")})
	assert.Equal(t, filepath.Join(home, "bam", "presets.yaml"), loc.Path)
	assert.Contains(t, loc.Note, "/mnt/c/Users/jdoe/bam/presets.yaml")
	assert.Contains(t, loc.Note, "BAM_PRESETS")
}

func TestLocatePresetsIgnoresTheGlobOutsideWSL(t *testing.T) {
	home := t.TempDir()
	loc := LocatePresets(PresetsLookup{Home: home, Getenv: noEnv, Glob: globOf("/mnt/c/Users/jdoe/bam/presets.yaml")})
	assert.Equal(t, filepath.Join(home, "bam", "presets.yaml"), loc.Path)
}

func TestLoadPresetsFileMergesBetweenMachineAndRepos(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".bam.yaml"), `version: 1
servers:
  work:
    url: https://bamboo.example.com
targets:
  prov:
    plan: PROJ-PROV
    defaults: {a: project, b: project, c: project}
`)
	machine := filepath.Join(dir, "m.yaml")
	writeFile(t, machine, `version: 1
targets:
  prov:
    defaults: {b: machine}
repos:
  `+dir+`:
    targets:
      prov:
        defaults: {c: repos}
`)
	presets := filepath.Join(dir, "bam", "presets.yaml")
	writeFile(t, presets, `presets:
  prov:
    variables: {b: presets, c: presets}
  mine:
    plan: PROJ-BUILD
`)
	cfg, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: machine, PresetsFile: presets, Getenv: noEnv})
	require.NoError(t, err)
	assert.Equal(t, presets, cfg.PresetsPath)

	all, err := cfg.Targets()
	require.NoError(t, err)
	assert.Equal(t, StringMap{"a": "project", "b": "presets", "c": "repos"}, all["prov"].Defaults)
	assert.Equal(t, "PROJ-BUILD", all["mine"].Plan)
	assert.Equal(t, []string{"presets"}, all["mine"].Layers[0].KeyPath)
	assert.Equal(t, presets, all["mine"].DefinedIn[0])
}

func TestLoadPresetsFileOptional(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "bam", "presets.yaml")
	cfg, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: filepath.Join(dir, "m.yaml"), PresetsFile: missing, Getenv: noEnv})
	require.NoError(t, err)
	assert.Equal(t, missing, cfg.PresetsPath)

	writeFile(t, missing, "\n")
	_, err = Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: filepath.Join(dir, "m.yaml"), PresetsFile: missing, Getenv: noEnv})
	require.NoError(t, err, "an empty presets file is fine")
}

func TestLoadPresetsFileErrors(t *testing.T) {
	for name, body := range map[string]string{
		"version 2":     "version: 2\npresets: {}\n",
		"targets key":   "targets: {}\n",
		"both var keys": "presets:\n  x:\n    plan: PROJ-X\n    variables: {a: b}\n    defaults: {a: c}\n",
		"bad name":      "presets:\n  Bad Name:\n    plan: PROJ-X\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			presets := filepath.Join(dir, "presets.yaml")
			writeFile(t, presets, body)
			_, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: filepath.Join(dir, "m.yaml"), PresetsFile: presets, Getenv: noEnv})
			require.Error(t, err)
			assert.Equal(t, errs.KindConfig, errs.KindOf(err))
			assert.Contains(t, err.Error(), presets)
		})
	}
}

func TestVariablesKeyWorksInOldFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".bam.yaml"), "version: 1\ntargets:\n  x:\n    plan: PROJ-X\n    variables: {a: b}\n")
	cfg, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: filepath.Join(dir, "m.yaml"), Getenv: noEnv})
	require.NoError(t, err)
	all, err := cfg.Targets()
	require.NoError(t, err)
	assert.Equal(t, StringMap{"a": "b"}, all["x"].Defaults)
}

func TestWriteTargetToPresetsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bam", "presets.yaml")
	d := TargetDraft{Name: "mine", Plan: "PROJ-PROV", Vars: []DraftVar{{Name: "cluster_type", Value: "k8s", Comment: "plan default"}}}
	require.NoError(t, WriteTarget(path, []string{"presets"}, d, false))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(data)
	assert.Contains(t, s, "presets:\n  mine:\n    plan: PROJ-PROV\n")
	assert.Contains(t, s, "    variables:\n      cluster_type: \"k8s\"")
	assert.NotContains(t, s, "defaults:")
	assert.NotContains(t, s, "version:", "the presets file needs no version line")
	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), st.Mode().Perm(), "a new presets file may hold secrets, so only its owner reads it")

		require.NoError(t, os.Chmod(path, 0o640))
		require.NoError(t, WriteTarget(path, []string{"presets"}, TargetDraft{Name: "two", Plan: "PROJ-BUILD"}, false))
		st, err = os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o640), st.Mode().Perm(), "an existing file keeps its mode")
	}

	cfg, err := Load(LoadOptions{WorkDir: filepath.Dir(path), Home: filepath.Dir(path), MachineFile: filepath.Join(filepath.Dir(path), "m.yaml"), PresetsFile: path, Getenv: noEnv})
	require.NoError(t, err)
	all, err := cfg.Targets()
	require.NoError(t, err)
	assert.Equal(t, "k8s", all["mine"].Defaults["cluster_type"])
}

func TestSyncTargetAddsToVariables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.yaml")
	writeFile(t, path, "presets:\n  mine:\n    plan: PROJ-PROV\n    variables:\n      a: \"1\"\n")
	_, err := SyncTarget(path, []string{"presets"}, "mine", TargetEdit{Add: []DraftVar{{Name: "b", Value: "2"}}}, true)
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "    variables:\n      a: \"1\"\n      b: \"2\"\n")
	assert.NotContains(t, string(data), "defaults")

	writeFile(t, path, "presets:\n  mine:\n    plan: PROJ-PROV\n")
	_, err = SyncTarget(path, []string{"presets"}, "mine", TargetEdit{Add: []DraftVar{{Name: "b", Value: "2"}}}, true)
	require.NoError(t, err)
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "    variables:\n      b: \"2\"\n")
}

func TestPresetsDirIsBesideThePresetsFile(t *testing.T) {
	assert.Equal(t, filepath.Join("/sync", "bam", "presets"), PresetsDir(filepath.Join("/sync", "bam", "presets.yaml")))
}

func TestLocatePresetsHomeDirectoryWithoutFile(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "bam", "presets"), 0o755))
	win := "/mnt/c/Users/jdoe/bam/presets.yaml"
	wsl := envOf(map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
	loc := LocatePresets(PresetsLookup{Home: home, Getenv: wsl, Glob: globOf(win)})
	assert.Equal(t, filepath.Join(home, "bam", "presets.yaml"), loc.Path, "a home presets directory wins over a Windows file")
	assert.True(t, loc.Exists)
}

func TestLocatePresetsWSLFindsAWindowsDirectory(t *testing.T) {
	home := t.TempDir()
	winBam := filepath.Join(t.TempDir(), "Users", "jdoe", "bam")
	require.NoError(t, os.MkdirAll(filepath.Join(winBam, "presets"), 0o755))
	wsl := envOf(map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
	glob := func(p string) ([]string, error) {
		if p == WSLPresetsDirGlob {
			return []string{filepath.Join(winBam, "presets")}, nil
		}
		return nil, nil
	}
	loc := LocatePresets(PresetsLookup{Home: home, Getenv: wsl, Glob: glob})
	assert.Equal(t, filepath.Join(winBam, "presets.yaml"), loc.Path)
	assert.True(t, loc.Exists)
	assert.Equal(t, filepath.Join(winBam, "presets"), PresetsDir(loc.Path))
}

func TestLoadPresetsDirectory(t *testing.T) {
	dir := t.TempDir()
	presets := filepath.Join(dir, "bam", "presets.yaml")
	writeFile(t, presets, "presets:\n  build:\n    plan: PROJ-BUILD\n")
	writeFile(t, filepath.Join(dir, "bam", "presets", "proj.yaml"), "presets:\n  deploy-qa:\n    plan: PROJ-DEPLOY\n    variables: {env: qa}\n")
	writeFile(t, filepath.Join(dir, "bam", "presets", "ops.yml"), "version: 1\npresets:\n  prov:\n    plan: OPS-PROV\n")
	writeFile(t, filepath.Join(dir, "bam", "presets", "README.md"), "not yaml: [\n")
	writeFile(t, filepath.Join(dir, "bam", "presets", ".proj.yaml"), "not yaml: [\n")
	writeFile(t, filepath.Join(dir, "bam", "presets", "old", "x.yaml"), "not yaml: [\n")
	writeFile(t, filepath.Join(dir, "bam", "presets", "empty.yaml"), "")

	cfg, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: filepath.Join(dir, "m.yaml"), PresetsFile: presets, Getenv: noEnv})
	require.NoError(t, err)
	all, err := cfg.Targets()
	require.NoError(t, err)
	require.Len(t, all, 3)
	proj := filepath.Join(dir, "bam", "presets", "proj.yaml")
	assert.Equal(t, []string{proj}, all["deploy-qa"].DefinedIn)
	assert.Equal(t, TargetLayer{Path: proj, KeyPath: []string{"presets"}, Target: all["deploy-qa"].Layers[0].Target}, all["deploy-qa"].Layers[0])
	assert.Equal(t, StringMap{"env": "qa"}, all["deploy-qa"].Defaults)
	assert.Equal(t, []string{filepath.Join(dir, "bam", "presets", "ops.yml")}, all["prov"].DefinedIn)
	assert.Equal(t, []string{presets}, all["build"].DefinedIn)
	assert.True(t, cfg.IsPresetsPath(proj))
	assert.True(t, cfg.IsPresetsPath(presets))
	assert.False(t, cfg.IsPresetsPath(filepath.Join(dir, "m.yaml")))
}

func TestLoadPresetsDirectoryWithoutPresetsFile(t *testing.T) {
	dir := t.TempDir()
	presets := filepath.Join(dir, "bam", "presets.yaml")
	writeFile(t, filepath.Join(dir, "bam", "presets", "proj.yaml"), "presets:\n  deploy-qa:\n    plan: PROJ-DEPLOY\n")
	cfg, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: filepath.Join(dir, "m.yaml"), PresetsFile: presets, Getenv: noEnv})
	require.NoError(t, err)
	_, ok, err := cfg.Target("deploy-qa")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestLoadPresetsDirectoryDuplicateNames(t *testing.T) {
	for name, other := range map[string]string{
		"file and directory":  "presets.yaml",
		"two directory files": filepath.Join("presets", "ops.yaml"),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			presets := filepath.Join(dir, "bam", "presets.yaml")
			proj := filepath.Join(dir, "bam", "presets", "proj.yaml")
			writeFile(t, proj, "presets:\n  deploy-qa:\n    plan: PROJ-DEPLOY\n")
			writeFile(t, filepath.Join(dir, "bam", other), "presets:\n  deploy-qa:\n    plan: OPS-DEPLOY\n")
			_, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: filepath.Join(dir, "m.yaml"), PresetsFile: presets, Getenv: noEnv})
			require.Error(t, err)
			assert.Equal(t, errs.KindConfig, errs.KindOf(err))
			assert.Contains(t, err.Error(), proj)
			assert.Contains(t, err.Error(), filepath.Join(dir, "bam", other))
		})
	}
}

func TestLoadPresetsDirectoryErrorsNameTheFile(t *testing.T) {
	for name, body := range map[string]string{
		"malformed": "presets: [\n",
		"bad name":  "presets:\n  Bad Name:\n    plan: PROJ-X\n",
		"unknown":   "targets: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			bad := filepath.Join(dir, "bam", "presets", "proj.yaml")
			writeFile(t, bad, body)
			_, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: filepath.Join(dir, "m.yaml"),
				PresetsFile: filepath.Join(dir, "bam", "presets.yaml"), Getenv: noEnv})
			require.Error(t, err)
			assert.Equal(t, errs.KindConfig, errs.KindOf(err))
			assert.Contains(t, err.Error(), bad)
		})
	}
}

func TestLoadPresetsDirectoryFollowsTheOverride(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "bam", "presets", "proj.yaml"), "presets:\n  home-only:\n    plan: PROJ-X\n")
	sync := t.TempDir()
	writeFile(t, filepath.Join(sync, "presets", "ops.yaml"), "presets:\n  synced:\n    plan: OPS-X\n")
	loc := LocatePresets(PresetsLookup{Home: home, Getenv: envOf(map[string]string{"BAM_PRESETS": filepath.Join(sync, "presets.yaml")})})
	assert.True(t, loc.Exists, "the directory alone counts")
	cfg, err := Load(LoadOptions{WorkDir: home, Home: home, MachineFile: filepath.Join(home, "m.yaml"), PresetsFile: loc.Path, Getenv: noEnv})
	require.NoError(t, err)
	all, err := cfg.Targets()
	require.NoError(t, err)
	assert.Contains(t, all, "synced")
	assert.NotContains(t, all, "home-only")
}

func TestIsPresetsFileName(t *testing.T) {
	for name, want := range map[string]bool{"proj.yaml": true, "ops.yml": true, "OPS.YAML": true, ".proj.yaml": false, "notes.txt": false, "proj.yaml.bak": false} {
		assert.Equal(t, want, IsPresetsFileName(name), name)
	}
}
