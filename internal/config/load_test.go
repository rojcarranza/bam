package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverStopsAtGitRoot(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "src", "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	writeFile(t, filepath.Join(home, "src", ".bam.yaml"), "version: 1\n") // above the git root: ignored
	sub := filepath.Join(repo, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	path, root, err := Discover(sub, home)
	require.NoError(t, err)
	assert.Equal(t, "", path)
	assert.Equal(t, repo, root)

	writeFile(t, filepath.Join(repo, ".bam.yaml"), "version: 1\n")
	path, root, err = Discover(sub, home)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(repo, ".bam.yaml"), path)
	assert.Equal(t, repo, root)
}

func TestDiscoverWithoutGitStopsAtHome(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	work := filepath.Join(home, "notes", "x")
	require.NoError(t, os.MkdirAll(work, 0o755))
	writeFile(t, filepath.Join(base, ".bam.yaml"), "version: 1\n") // above home: ignored

	path, root, err := Discover(work, home)
	require.NoError(t, err)
	assert.Equal(t, "", path)
	assert.Equal(t, work, root)

	writeFile(t, filepath.Join(home, ".bam.yaml"), "version: 1\n")
	path, _, err = Discover(work, home)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".bam.yaml"), path)
}

func TestDiscoverRejectsAMisnamedProjectFile(t *testing.T) {
	for _, name := range []string{".bam.yml", "bam.yaml", "bam.yml"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			repo := filepath.Join(home, "repo")
			sub := filepath.Join(repo, "a")
			require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
			require.NoError(t, os.MkdirAll(sub, 0o755))
			writeFile(t, filepath.Join(repo, name), "version: 1\n")

			_, _, err := Discover(sub, home)
			var e *errs.Error
			require.ErrorAs(t, err, &e)
			assert.Equal(t, errs.KindConfig, e.Kind)
			assert.Equal(t, "found "+filepath.Join(repo, name)+", but bam reads only .bam.yaml", e.What)
			assert.Equal(t, "rename it: mv "+filepath.Join(repo, name)+" "+filepath.Join(repo, ".bam.yaml"), e.Try)

			// The real file wins; the stray one is then left alone.
			writeFile(t, filepath.Join(repo, ".bam.yaml"), "version: 1\n")
			path, _, err := Discover(sub, home)
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(repo, ".bam.yaml"), path)
		})
	}
}

func TestLoadParsesBothLayers(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	writeFile(t, filepath.Join(repo, ".bam.yaml"), `
version: 1
servers:
  work:
    url: https://bamboo.example.com
default_server: work
projects: [PROJ, OPS]
targets:
  provision-lab:
    plan: PROJ-PROV
    defaults:
      compute_nodes: 2
      enabled: true
    options:
      compute_nodes: [2, 3]
    timeout: 45m
`)
	machine := filepath.Join(home, ".config", "bam", "config.yaml")
	writeFile(t, machine, "version: 1\ndefault_server: home\nservers:\n  home:\n    url: http://bamboo.lab.example:8085\n")

	cfg, err := Load(LoadOptions{WorkDir: repo, Home: home, MachineFile: machine, Getenv: envOf(map[string]string{"BAM_SERVER": "work"})})
	require.NoError(t, err)
	assert.Equal(t, repo, cfg.RepoRoot)
	assert.Equal(t, []string{"PROJ", "OPS"}, cfg.Project.Projects)
	tg := cfg.Project.Targets["provision-lab"]
	assert.Equal(t, "2", tg.Defaults["compute_nodes"])
	assert.Equal(t, "true", tg.Defaults["enabled"])
	assert.Equal(t, []string{"2", "3"}, tg.Options["compute_nodes"])
	assert.Equal(t, 45*time.Minute, time.Duration(tg.Timeout))
	assert.Equal(t, "home", cfg.Machine.DefaultServer)
	assert.Equal(t, "work", cfg.Env.Server)
}

func TestLoadMissingMachineFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: filepath.Join(dir, "none.yaml"), Getenv: noEnv})
	require.NoError(t, err)
	assert.Nil(t, cfg.Project)
	require.NotNil(t, cfg.Machine)
	assert.Equal(t, 1, cfg.Machine.Version)
}

func TestLoadRejectsUnknownVersion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".bam.yaml"), "version: 2\n")
	_, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: filepath.Join(dir, "m.yaml"), Getenv: noEnv})
	require.Error(t, err)
	assert.Equal(t, errs.KindConfig, errs.KindOf(err))
	assert.Contains(t, err.Error(), "version 2")
	assert.Contains(t, err.Error(), "understands version 1")
}

func TestLoadRejectsMissingVersion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".bam.yaml"), "projects: [PROJ]\n")
	_, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: filepath.Join(dir, "m.yaml"), Getenv: noEnv})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing version")
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".bam.yaml"), "version: 1\nservrs: {}\n")
	_, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: filepath.Join(dir, "m.yaml"), Getenv: noEnv})
	require.Error(t, err)
	assert.Equal(t, errs.KindConfig, errs.KindOf(err))
	assert.Contains(t, err.Error(), "servrs")
}

func TestLoadRejectsBadMachineColor(t *testing.T) {
	dir := t.TempDir()
	machine := filepath.Join(dir, "m.yaml")
	writeFile(t, machine, "version: 1\ncolor: bogus\n")
	_, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: machine, Getenv: noEnv})
	require.Error(t, err)
	assert.Equal(t, errs.KindConfig, errs.KindOf(err))
	assert.Contains(t, err.Error(), machine+`: color must be auto, always or never, not "bogus"`)
}

func TestLoadMachineEditor(t *testing.T) {
	dir := t.TempDir()
	machine := filepath.Join(dir, "m.yaml")
	writeFile(t, machine, "version: 1\npager: less -FRX\neditor: code --wait\n")
	cfg, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: machine, Getenv: noEnv})
	require.NoError(t, err)
	assert.Equal(t, "code --wait", cfg.Machine.Editor)
}

func TestLoadDefaultProjectInEveryLayer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".bam.yaml"), "version: 1\nprojects: [PROJ, OPS]\ndefault_project: OPS\n")
	machine := filepath.Join(dir, "m.yaml")
	writeFile(t, machine, `version: 1
servers:
  work:
    url: https://bamboo.example.com
    default_project: PROJ
repos:
  `+dir+`:
    default_project: LAB
`)
	cfg, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: machine, Getenv: noEnv})
	require.NoError(t, err)
	assert.Equal(t, "OPS", cfg.Project.DefaultProject)
	assert.Equal(t, "PROJ", cfg.Machine.Servers["work"].DefaultProject)
	assert.Equal(t, "LAB", cfg.Machine.Repos[dir].DefaultProject)
}

func TestLoadAcceptsMachineColorInAnyCase(t *testing.T) {
	dir := t.TempDir()
	machine := filepath.Join(dir, "m.yaml")
	writeFile(t, machine, "version: 1\ncolor: Always\n")
	_, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: machine, Getenv: noEnv})
	require.NoError(t, err)
}
