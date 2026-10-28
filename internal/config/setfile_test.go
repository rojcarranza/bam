package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/require"
)

const setFileProject = "version: 1\nservers: {work: {url: https://bamboo.example.com}}\n" +
	"targets:\n  a: {plan: PROJ-A}\n  b: {plan: PROJ-B}\n"

// trainFile writes body as dir/name and returns the path.
func trainFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

func TestLoadSetFile(t *testing.T) {
	cfg, err := loadYAML(t, setFileProject, "")
	require.NoError(t, err)
	p := trainFile(t, "hotfix-2041.yaml", "timeout: 1h\nstages:\n  - [a]\n  - [{target: b, matrix: [{env: qa}, {env: prod}]}]\n")
	s, err := cfg.LoadSetFile(p)
	require.NoError(t, err)
	require.Equal(t, "hotfix-2041", s.Name, "named after the file")
	require.Equal(t, []string{p}, s.DefinedIn)
	require.Len(t, s.Stages, 2)
	require.Len(t, s.Stages[1][0].Expand(), 2)
}

func TestLoadSetFileTakesItsName(t *testing.T) {
	cfg, err := loadYAML(t, setFileProject, "")
	require.NoError(t, err)
	s, err := cfg.LoadSetFile(trainFile(t, "Train File.yml", "name: release-2\nstages: [[a]]\n"))
	require.NoError(t, err)
	require.Equal(t, "release-2", s.Name)
}

func TestLoadSetFileAsABareListOfStages(t *testing.T) {
	cfg, err := loadYAML(t, setFileProject, "")
	require.NoError(t, err)
	s, err := cfg.LoadSetFile(trainFile(t, "hotfix.yaml", "- [a]\n- [{target: b, matrix: [{env: qa}, {env: prod}]}]\n"))
	require.NoError(t, err)
	require.Equal(t, "hotfix", s.Name)
	require.Len(t, s.Stages, 2)
	require.Len(t, s.Stages[1][0].Expand(), 2)
}

func TestLoadSetFileErrors(t *testing.T) {
	cfg, err := loadYAML(t, setFileProject, "")
	require.NoError(t, err)
	for name, tc := range map[string]struct{ file, body, want string }{
		"bad file name":  {"Train File.yaml", "stages: [[a]]\n", "add name:"},
		"bad name":       {"t.yaml", "name: Bad\nstages: [[a]]\n", `invalid set name "Bad"`},
		"unknown key":    {"t.yaml", "stagez: [[a]]\n", `line 1: unknown field "stagez" in a train file`},
		"single value":   {"t.yaml", "a\n", "expected a train file (stages: [[preset], ...]), found a single value"},
		"unknown preset": {"t.yaml", "stages: [[nope]]\n", "presets here: a, b"},
		"duplicate run":  {"t.yaml", "stages: [[a, a]]\n", `run "a" twice in stage 1`},
		"no stages":      {"t.yaml", "stages: []\n", "has no stages"},
		"empty file":     {"t.yaml", "", "is empty"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := cfg.LoadSetFile(trainFile(t, tc.file, tc.body))
			require.Error(t, err)
			require.Equal(t, errs.KindConfig, errs.KindOf(err))
			require.Contains(t, err.Error()+" "+errWhyTry(err), tc.want)
		})
	}
	_, err = cfg.LoadSetFile(filepath.Join(t.TempDir(), "missing.yaml"))
	require.Equal(t, errs.KindConfig, errs.KindOf(err))
}

func TestLoadSetFileRefusesALiteralSecret(t *testing.T) {
	cfg, err := loadYAML(t, setFileProject, "")
	require.NoError(t, err)
	_, err = cfg.LoadSetFile(trainFile(t, "t.yaml", "stages: [[{target: a, vars: {db_password: hunter2}}]]\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "holds a literal secret")
	_, err = cfg.LoadSetFile(trainFile(t, "t.yaml", "stages: [[{target: a, vars: {db_password: \"${PW}\"}}]]\n"))
	require.NoError(t, err)
}

func errWhyTry(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Why + " " + e.Try
	}
	return ""
}
