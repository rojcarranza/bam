package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadProject(t *testing.T, project, machine string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	if project != "" {
		writeFile(t, filepath.Join(dir, ".bam.yaml"), project)
	}
	mpath := filepath.Join(dir, "machine.yaml")
	if machine != "" {
		writeFile(t, mpath, machine)
	}
	return Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: mpath, Getenv: noEnv})
}

func TestTokenInProjectFileIsHardError(t *testing.T) {
	_, err := loadProject(t, "version: 1\nservers:\n  work:\n    url: https://bamboo.example.com\n    token: abc\n", "")
	require.Error(t, err)
	assert.Equal(t, errs.KindConfig, errs.KindOf(err))
	assert.Contains(t, err.Error(), "servers.work.token")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Contains(t, e.Try, "bam login work")
}

func TestAuthEnvInProjectFileIsHardError(t *testing.T) {
	_, err := loadProject(t, "version: 1\nservers:\n  evil:\n    url: https://bamboo.example.com\n    auth_env: SOME_OTHER_SECRET\n", "")
	require.Error(t, err)
	assert.Equal(t, errs.KindConfig, errs.KindOf(err))
	assert.Contains(t, err.Error(), "servers.evil.auth_env")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Contains(t, e.Why, "auth_env")
	assert.Contains(t, e.Try, "~/.config/bam/config.yaml")
}

func TestAuthEnvViaMergeKeyInProjectFileIsHardError(t *testing.T) {
	_, err := loadProject(t, "version: 1\nservers:\n  evil:\n    url: https://bamboo.example.com\n    <<: {auth_env: SOME_OTHER_SECRET}\ndefault_server: evil\n", "")
	require.Error(t, err)
	assert.Equal(t, errs.KindConfig, errs.KindOf(err))
	assert.Contains(t, err.Error(), "servers.evil.auth_env")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Contains(t, e.Why, "auth_env")
	assert.Contains(t, e.Try, "~/.config/bam/config.yaml")
}

func TestAuthEnvViaAnchorAliasInProjectFileIsHardError(t *testing.T) {
	_, err := loadProject(t, "version: 1\nservers:\n  base: &b\n    auth_env: SOME_OTHER_SECRET\n  evil:\n    url: https://bamboo.example.com\n    <<: *b\ndefault_server: evil\n", "")
	require.Error(t, err)
	assert.Equal(t, errs.KindConfig, errs.KindOf(err))
	assert.Contains(t, err.Error(), "auth_env")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Contains(t, e.Why, "auth_env")
	assert.Contains(t, e.Try, "~/.config/bam/config.yaml")
}

func TestLiteralSecretDefaultInProjectFileIsHardError(t *testing.T) {
	_, err := loadProject(t, "version: 1\ntargets:\n  lab:\n    plan: PROJ-LAB\n    defaults:\n      db_password: hunter2\n", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "targets.lab.defaults.db_password")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Contains(t, e.Try, `"${DB_PASSWORD}"`)
}

func TestEnvRefSecretDefaultIsAllowed(t *testing.T) {
	_, err := loadProject(t, "version: 1\ntargets:\n  lab:\n    plan: PROJ-LAB\n    defaults:\n      db_password: \"${LAB_DB_PASSWORD}\"\n", "")
	assert.NoError(t, err)
}

func TestLiteralSecretInMachineFileIsAllowed(t *testing.T) {
	_, err := loadProject(t, "", "version: 1\ntargets:\n  lab:\n    plan: PROJ-LAB\n    defaults:\n      db_password: hunter2\n")
	assert.NoError(t, err)
}

func TestInvalidTargetNamesRejectedInEveryLayer(t *testing.T) {
	_, err := loadProject(t, "version: 1\ntargets:\n  Provision:\n    plan: PROJ-PROV\n", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"Provision"`)

	_, err = loadProject(t, "", "version: 1\nrepos:\n  /x:\n    targets:\n      BAD:\n        plan: PROJ-PROV\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"BAD"`)
}

func TestEnvRef(t *testing.T) {
	name, ok := EnvRef("${LAB_DB_PASSWORD}")
	assert.True(t, ok)
	assert.Equal(t, "LAB_DB_PASSWORD", name)
	for _, v := range []string{"$LAB", "pre${LAB}", "${LAB}post", "${1BAD}", ""} {
		_, ok := EnvRef(v)
		assert.False(t, ok, v)
	}
}

func TestIsMaskedName(t *testing.T) {
	for _, n := range []string{"db_password", "API_SECRET", "sshKey", "gpg.passphrase", "PASSWORD"} {
		assert.True(t, IsMaskedName(n), n)
	}
	for _, n := range []string{"cluster_type", "pass", "token"} {
		assert.False(t, IsMaskedName(n), n)
	}
}

func TestEnvNameFor(t *testing.T) {
	assert.Equal(t, "DB_PASSWORD", EnvNameFor("db-password"))
	assert.Equal(t, "GPG_PASSPHRASE", EnvNameFor("gpg.passphrase"))
}

func TestValidTargetName(t *testing.T) {
	assert.True(t, ValidTargetName("provision-lab"))
	assert.True(t, ValidTargetName("lab_2"))
	assert.False(t, ValidTargetName("PROJ-PLAN"))
	assert.False(t, ValidTargetName("2lab"))
}

func TestTargetNameFor(t *testing.T) {
	assert.Equal(t, "web-deploy", TargetNameFor("Web Deploy"))
	assert.Equal(t, "lab", TargetNameFor(" Lab!"))
	assert.Empty(t, TargetNameFor("2lab"), "no suggestion that is itself invalid")
	assert.Empty(t, TargetNameFor("--"))
}

func TestEnvServerAliasIsReserved(t *testing.T) {
	_, err := loadProject(t, "version: 1\nservers:\n  env:\n    url: https://bamboo.example.com\n", "")
	require.Error(t, err)
	assert.Equal(t, errs.KindConfig, errs.KindOf(err))
	assert.Contains(t, err.Error(), `"env" is reserved`)

	_, err = loadProject(t, "", "version: 1\nservers:\n  env:\n    url: https://bamboo.example.com\n")
	require.Error(t, err)
	assert.Equal(t, errs.KindConfig, errs.KindOf(err))
	assert.Contains(t, err.Error(), `"env" is reserved`)
}
