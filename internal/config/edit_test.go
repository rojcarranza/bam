package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteTargetKeepsCommentsAndLoads(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	path := filepath.Join(dir, ".bam.yaml")
	writeFile(t, path, "# team presets\nversion: 1\nprojects: [PROJ]\ntargets:\n  build:\n    plan: PROJ-BUILD\n")

	require.NoError(t, WriteTarget(path, []string{"targets"}, sampleDraft(), false))

	data, _ := os.ReadFile(path)
	text := string(data)
	assert.Contains(t, text, "# team presets")
	assert.Contains(t, text, "projects: [PROJ]")
	assert.Contains(t, text, "# watch: true")

	cfg, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: filepath.Join(dir, "m.yaml"), Getenv: noEnv})
	require.NoError(t, err)
	assert.Equal(t, "PROJ-BUILD", cfg.Project.Targets["build"].Plan)
	assert.Equal(t, "k8s", cfg.Project.Targets["provision-lab"].Defaults["cluster_type"])
}

func TestWriteTargetRefusesExistingWithoutForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".bam.yaml")
	writeFile(t, path, "version: 1\ntargets:\n  provision-lab:\n    plan: OLD-PLAN\n")

	err := WriteTarget(path, []string{"targets"}, sampleDraft(), false)
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))

	require.NoError(t, WriteTarget(path, []string{"targets"}, sampleDraft(), true))
	data, _ := os.ReadFile(path)
	assert.NotContains(t, string(data), "OLD-PLAN")
}

func TestWriteTargetCreatesMachineRepoPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bam", "config.yaml")
	require.NoError(t, WriteTarget(path, []string{"repos", "/home/jdoe/src/repo", "targets"}, TargetDraft{Name: "mine", Plan: "PROJ-MINE"}, false))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "version: 1")
	assert.Contains(t, string(data), "/home/jdoe/src/repo:")
	assert.Contains(t, string(data), "plan: PROJ-MINE")
}

func TestSetAndRemoveMachineServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bam", "config.yaml")
	writeFile(t, path, "# my machine\nversion: 1\n")

	require.NoError(t, SetMachineServer(path, "home", Server{URL: "http://bamboo.lab.example:8085", Projects: []string{"LAB"}}))
	data, _ := os.ReadFile(path)
	assert.Contains(t, string(data), "# my machine")
	assert.Contains(t, string(data), "url: http://bamboo.lab.example:8085")

	require.NoError(t, RemoveMachineServer(path, "home"))
	data, _ = os.ReadFile(path)
	assert.NotContains(t, string(data), "bamboo.lab.example")

	err := RemoveMachineServer(path, "home")
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
}

func TestRemoveMachineServerDropsReferencesToIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, `version: 1
default_server: work
servers:
  work:
    url: https://bamboo.example.com
  home:
    url: https://bamboo.lab.example
repos:
  /src/a:
    server: work
    projects: [PROJ]
  /src/b:
    server: home
`)
	require.NoError(t, RemoveMachineServer(path, "work"))
	data, _ := os.ReadFile(path)
	assert.Equal(t, `version: 1
servers:
  home:
    url: https://bamboo.lab.example
repos:
  /src/a:
    projects: [PROJ]
  /src/b:
    server: home
`, string(data))
}

func TestWriteInitFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".bam.yaml")
	f := InitFile{ServerAlias: "work", ServerURL: "https://bamboo.example.com", Projects: []string{"PROJ", "OPS"}}
	require.NoError(t, WriteInitFile(path, f, []TargetDraft{{Name: "prov", Plan: "PROJ-PROV", Full: true}}, false))

	data, _ := os.ReadFile(path)
	assert.Equal(t, `version: 1
servers:
  work:
    url: "https://bamboo.example.com"
default_server: work
projects: [PROJ, OPS]
targets:
  prov:
    plan: PROJ-PROV
    # branch: develop
    # watch: true
`, string(data))

	err := WriteInitFile(path, f, nil, false)
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	require.NoError(t, WriteInitFile(path, f, nil, true))
}
