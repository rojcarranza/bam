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

const machineWithTargets = `version: 1
servers:
  work:
    url: https://bamboo.example.com # keep me
targets:
  # the package I build most
  pkg-a:
    plan: PROJ-PKG
    branch: stable
    defaults:
      PACKAGE_NAME: custom-package-a # no plan default
    required: [PACKAGE_NAME]
repos:
  /home/jdoe/src/repo:
    server: work
    targets:
      prov:
        plan: PROJ-PROV
        defaults: {cluster_name: jdoe-lab}
  /home/jdoe/src/other:
    targets:
      only-here:
        plan: PROJ-OPS
color: auto
`

func TestMigrateTargetsMovesEveryMachineTarget(t *testing.T) {
	dir := t.TempDir()
	machine := filepath.Join(dir, "config.yaml")
	presets := filepath.Join(dir, "bam", "presets.yaml")
	writeFile(t, machine, machineWithTargets)

	res, err := MigrateTargets(machine, presets, true)
	require.NoError(t, err)
	assert.Equal(t, []MigrateMove{
		{Name: "pkg-a", From: "targets"},
		{Name: "prov", From: "repos./home/jdoe/src/repo.targets"},
		{Name: "only-here", From: "repos./home/jdoe/src/other.targets"},
	}, res.Moved)
	assert.Empty(t, res.Skipped)

	p, err := os.ReadFile(presets)
	require.NoError(t, err)
	ps := string(p)
	assert.Contains(t, ps, "presets:\n  # the package I build most\n  pkg-a:\n")
	assert.Contains(t, ps, "    variables:\n      PACKAGE_NAME: custom-package-a # no plan default\n")
	assert.Contains(t, ps, "  prov:\n    plan: PROJ-PROV\n    variables: {cluster_name: jdoe-lab}\n")
	assert.NotContains(t, ps, "defaults")
	assert.NotContains(t, ps, "version:")
	if runtime.GOOS != "windows" {
		st, err := os.Stat(presets)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	}

	m, err := os.ReadFile(machine)
	require.NoError(t, err)
	ms := string(m)
	assert.NotContains(t, ms, "targets")
	assert.NotContains(t, ms, "/home/jdoe/src/other", "a repos entry left empty is removed")
	assert.Contains(t, ms, "  /home/jdoe/src/repo:\n    server: work\n")
	assert.Contains(t, ms, "url: https://bamboo.example.com # keep me")
	assert.Contains(t, ms, "color: auto")

	cfg, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: machine, PresetsFile: presets, Getenv: noEnv})
	require.NoError(t, err)
	all, err := cfg.Targets()
	require.NoError(t, err)
	assert.Equal(t, "custom-package-a", all["pkg-a"].Defaults["PACKAGE_NAME"])
	assert.Equal(t, []string{"PACKAGE_NAME"}, all["pkg-a"].Required)
	assert.Equal(t, "jdoe-lab", all["prov"].Defaults["cluster_name"])
	assert.Equal(t, []string{presets}, all["prov"].DefinedIn)
}

func TestMigrateTargetsSkipsClashes(t *testing.T) {
	dir := t.TempDir()
	machine := filepath.Join(dir, "config.yaml")
	presets := filepath.Join(dir, "presets.yaml")
	writeFile(t, machine, `version: 1
targets:
  pkg-a:
    plan: PROJ-PKG
  dup:
    plan: PROJ-ONE
repos:
  /home/jdoe/src/repo:
    targets:
      dup:
        plan: PROJ-TWO
      fresh:
        plan: PROJ-NEW
`)
	writeFile(t, presets, "presets:\n  pkg-a:\n    plan: PROJ-MINE\n")

	res, err := MigrateTargets(machine, presets, true)
	require.NoError(t, err)
	assert.Equal(t, []MigrateMove{{Name: "fresh", From: "repos./home/jdoe/src/repo.targets"}}, res.Moved)
	require.Len(t, res.Skipped, 3)
	assert.Equal(t, "pkg-a", res.Skipped[0].Name)
	assert.Contains(t, res.Skipped[0].Reason, "already in "+presets)
	assert.Equal(t, "dup", res.Skipped[1].Name)
	assert.Contains(t, res.Skipped[1].Reason, "defined in 2 places")
	assert.Equal(t, "dup", res.Skipped[2].Name)

	p, _ := os.ReadFile(presets)
	assert.Contains(t, string(p), "plan: PROJ-MINE", "the existing preset is untouched")
	assert.Contains(t, string(p), "fresh:")
	m, _ := os.ReadFile(machine)
	assert.Contains(t, string(m), "pkg-a:", "a skipped target stays where it was")
	assert.Contains(t, string(m), "PROJ-ONE")
	assert.Contains(t, string(m), "PROJ-TWO")
	assert.NotContains(t, string(m), "fresh")
}

func TestMigrateTargetsDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	machine := filepath.Join(dir, "config.yaml")
	presets := filepath.Join(dir, "presets.yaml")
	writeFile(t, machine, machineWithTargets)

	res, err := MigrateTargets(machine, presets, false)
	require.NoError(t, err)
	assert.Len(t, res.Moved, 3)
	assert.NoFileExists(t, presets)
	m, _ := os.ReadFile(machine)
	assert.Equal(t, machineWithTargets, string(m))
}

func TestMigrateTargetsNothingToMove(t *testing.T) {
	dir := t.TempDir()
	presets := filepath.Join(dir, "presets.yaml")

	res, err := MigrateTargets(filepath.Join(dir, "missing.yaml"), presets, true)
	require.NoError(t, err)
	assert.Empty(t, res.Moved)
	assert.NoFileExists(t, presets)

	machine := filepath.Join(dir, "config.yaml")
	writeFile(t, machine, "version: 1\ncolor: auto\n")
	res, err = MigrateTargets(machine, presets, true)
	require.NoError(t, err)
	assert.Empty(t, res.Moved)
	assert.NoFileExists(t, presets)
	m, _ := os.ReadFile(machine)
	assert.Equal(t, "version: 1\ncolor: auto\n", string(m))
}

func TestMigrateTargetsRefusesAnInvalidPresetsFile(t *testing.T) {
	for name, body := range map[string]string{
		"list":   "presets: []\n",
		"scalar": "presets: old-value\n",
		"key":    "targets: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			machine := filepath.Join(dir, "config.yaml")
			presets := filepath.Join(dir, "presets.yaml")
			writeFile(t, machine, machineWithTargets)
			writeFile(t, presets, body)

			_, err := MigrateTargets(machine, presets, true)
			require.Error(t, err)
			assert.Contains(t, err.Error(), presets)
			p, _ := os.ReadFile(presets)
			assert.Equal(t, body, string(p), "the presets file is untouched")
			m, _ := os.ReadFile(machine)
			assert.Equal(t, machineWithTargets, string(m), "the machine file is untouched")
		})
	}
}

func TestMigrateTargetsSkipsYAMLAnchors(t *testing.T) {
	dir := t.TempDir()
	machine := filepath.Join(dir, "config.yaml")
	presets := filepath.Join(dir, "presets.yaml")
	body := `version: 1
targets:
  base: &base
    plan: PROJ-BASE
    defaults: {env: qa}
  derived:
    <<: *base
    branch: develop
  plain:
    plan: PROJ-PLAIN
`
	writeFile(t, machine, body)

	res, err := MigrateTargets(machine, presets, true)
	require.NoError(t, err)
	assert.Equal(t, []MigrateMove{{Name: "plain", From: "targets"}}, res.Moved)
	require.Len(t, res.Skipped, 2)
	assert.Contains(t, res.Skipped[0].Reason, "YAML anchors")

	cfg, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: machine, PresetsFile: presets, Getenv: noEnv})
	require.NoError(t, err, "both files still load")
	all, err := cfg.Targets()
	require.NoError(t, err)
	assert.Equal(t, "PROJ-BASE", all["derived"].Plan)
	assert.Equal(t, "PROJ-PLAIN", all["plain"].Plan)
}

func TestMigrateTargetsSkipsNamesInThePresetsDirectory(t *testing.T) {
	dir := t.TempDir()
	machine := filepath.Join(dir, "config.yaml")
	presets := filepath.Join(dir, "bam", "presets.yaml")
	proj := filepath.Join(dir, "bam", "presets", "proj.yaml")
	writeFile(t, machine, machineWithTargets)
	writeFile(t, proj, "presets:\n  prov:\n    plan: PROJ-PROV\n")

	res, err := MigrateTargets(machine, presets, true)
	require.NoError(t, err)
	assert.Equal(t, []MigrateSkip{{Name: "prov", From: "repos./home/jdoe/src/repo.targets", Reason: "already in " + proj}}, res.Skipped)
	assert.Len(t, res.Moved, 2)

	_, err = Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: machine, PresetsFile: presets, Getenv: noEnv})
	require.NoError(t, err, "no preset ends up in two presets files")
}

func TestMigrateTargetsRefusesDuplicatesInThePresetsLayer(t *testing.T) {
	dir := t.TempDir()
	machine := filepath.Join(dir, "config.yaml")
	presets := filepath.Join(dir, "bam", "presets.yaml")
	writeFile(t, machine, machineWithTargets)
	a := filepath.Join(dir, "bam", "presets", "a.yaml")
	b := filepath.Join(dir, "bam", "presets", "b.yaml")
	writeFile(t, a, "presets:\n  twice:\n    plan: PROJ-X\n")
	writeFile(t, b, "presets:\n  twice:\n    plan: OPS-X\n")
	before, _ := os.ReadFile(machine)

	_, err := MigrateTargets(machine, presets, true)
	require.Error(t, err)
	assert.Equal(t, errs.KindConfig, errs.KindOf(err))
	assert.Contains(t, err.Error(), a)
	assert.Contains(t, err.Error(), b)
	after, _ := os.ReadFile(machine)
	assert.Equal(t, before, after, "nothing moves")
	assert.NoFileExists(t, presets)
}
