package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChildrenMergeAcrossLayers(t *testing.T) {
	cfg, err := loadProject(t,
		"version: 1\nchildren:\n  PROJ-API: [OPS-DEPLOY, PROJ-DOCS]\n  OPS-DEPLOY: [OPS-SMOKE]\n",
		"version: 1\nchildren:\n  OPS-DEPLOY: [OPS-AUDIT]\n")
	require.NoError(t, err)
	got, err := cfg.Children()
	require.NoError(t, err)
	require.Equal(t, map[string][]string{
		"PROJ-API":   {"OPS-DEPLOY", "PROJ-DOCS"},
		"OPS-DEPLOY": {"OPS-AUDIT"},
	}, got, "the machine file replaces the project's list for the same plan")
}

func TestChildrenFromTheReposEntryWin(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	writeFile(t, filepath.Join(dir, ".bam.yaml"), "version: 1\nchildren:\n  PROJ-API: [OPS-DEPLOY]\n")
	mpath := filepath.Join(dir, "machine.yaml")
	writeFile(t, mpath, "version: 1\nrepos:\n  "+dir+":\n    children:\n      PROJ-API: [PROJ-DOCS]\n")
	cfg, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: mpath, Getenv: noEnv})
	require.NoError(t, err)
	got, err := cfg.Children()
	require.NoError(t, err)
	require.Equal(t, map[string][]string{"PROJ-API": {"PROJ-DOCS"}}, got)
}

func TestChildrenValidation(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{
		{"children:\n  proj-api: [OPS-DEPLOY]\n", `children: "proj-api" is not a plan key`},
		{"children:\n  PROJ-API: [OPS-DEPLOY-1]\n", `children.PROJ-API: "OPS-DEPLOY-1" is not a plan key`},
		{"children:\n  PROJ-API: [PROJ-API]\n", "children.PROJ-API lists itself"},
	} {
		cfg, err := loadProject(t, "version: 1\n"+tc.yaml, "")
		require.NoError(t, err, tc.yaml)
		_, err = cfg.Children()
		require.Error(t, err, tc.yaml)
		assert.Equal(t, errs.KindConfig, errs.KindOf(err), tc.yaml)
		assert.Contains(t, err.Error(), tc.want, tc.yaml)
		assert.Contains(t, err.Error(), ".bam.yaml", tc.yaml)
	}
}

func TestChildrenEmptyWithoutConfig(t *testing.T) {
	cfg, err := loadProject(t, "version: 1\n", "")
	require.NoError(t, err)
	got, err := cfg.Children()
	require.NoError(t, err)
	require.Empty(t, got)
}
