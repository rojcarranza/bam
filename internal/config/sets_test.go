package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/require"
)

// loadYAML loads a repository holding project as .bam.yaml and a machine
// file holding machine; an empty string leaves that file out.
func loadYAML(t *testing.T, project, machine string) (*Config, error) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	if project != "" {
		writeFile(t, filepath.Join(root, ProjectFileName), project)
	}
	mpath := filepath.Join(t.TempDir(), "config.yaml")
	if machine != "" {
		writeFile(t, mpath, machine)
	}
	return Load(LoadOptions{WorkDir: root, Home: t.TempDir(), MachineFile: mpath, Getenv: noEnv})
}

const setsProject = `version: 1
servers: {work: {url: https://bamboo.example.com}}
targets:
  publish-specs: {plan: PROJ-SPEC}
  provision: {plan: OPS-PROV}
  deploy: {plan: OPS-DEP}
sets:
  release:
    timeout: 2h
    stages:
      - [publish-specs]
      - - provision
        - target: deploy
          vars: {env: qa, deploy_token: "${DEPLOY_TOKEN}"}
`

func TestSetsParseShorthandAndLongForm(t *testing.T) {
	cfg, err := loadYAML(t, setsProject, "")
	require.NoError(t, err)
	s, ok, err := cfg.Set("release")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 2*time.Hour, time.Duration(s.Timeout))
	require.Equal(t, Stage{{Target: "publish-specs"}}, s.Stages[0])
	require.Equal(t, Stage{{Target: "provision"},
		{Target: "deploy", Vars: StringMap{"env": "qa", "deploy_token": "${DEPLOY_TOKEN}"}}}, s.Stages[1])
	require.Len(t, s.DefinedIn, 1)
}

func TestAHigherLayerReplacesASetWhole(t *testing.T) {
	machine := `version: 1
sets:
  release:
    stages: [[deploy]]
`
	cfg, err := loadYAML(t, setsProject, machine)
	require.NoError(t, err)
	s, _, err := cfg.Set("release")
	require.NoError(t, err)
	require.Equal(t, []Stage{{{Target: "deploy"}}}, s.Stages)
	require.Zero(t, s.Timeout, "nothing of the lower layer survives")
	require.Len(t, s.DefinedIn, 2)
}

func TestSetConfigErrors(t *testing.T) {
	base := "version: 1\nservers: {work: {url: https://bamboo.example.com}, lab: {url: https://bamboo.lab.example}}\n" +
		"targets:\n  a: {plan: PROJ-A}\n  b: {plan: PROJ-B, server: work}\n  c: {plan: PROJ-C, server: lab}\n"
	for name, tc := range map[string]struct{ sets, want string }{
		"unknown preset": {"sets: {rel: {stages: [[nope]]}}", `unknown preset "nope"`},
		"no stages":      {"sets: {rel: {stages: []}}", "has no stages"},
		"empty stage":    {"sets: {rel: {stages: [[a], []]}}", "stage 2 has no runs"},
		"duplicate":      {"sets: {rel: {stages: [[a, a]]}}", `preset "a" twice in stage 1`},
		"servers":        {"sets: {rel: {stages: [[b], [c]]}}", `servers "work" and "lab"`},
		"bad name":       {"sets: {Rel: {stages: [[a]]}}", `invalid set name "Rel"`},
		"bad timeout":    {"sets: {rel: {timeout: soon, stages: [[a]]}}", "not a duration"},
		"unknown field":  {"sets: {rel: {stages: [[{target: a, env: x}]]}}", `unknown field "env"`},
		"literal secret": {"sets: {rel: {stages: [[{target: a, vars: {db_password: hunter2}}]]}}", "holds a literal secret"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := loadYAML(t, base+tc.sets+"\n", "")
			if err == nil {
				_, err = cfg.Sets()
			}
			require.Error(t, err)
			require.Equal(t, errs.KindConfig, errs.KindOf(err))
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestSetVarsAcceptEnvReferences(t *testing.T) {
	cfg, err := loadYAML(t, setsProject, "")
	require.NoError(t, err)
	_, err = cfg.Sets()
	require.NoError(t, err)
}

func TestSetsInTheReposEntry(t *testing.T) {
	cfg, err := loadYAML(t, setsProject, "")
	require.NoError(t, err)
	cfg.Machine.Repos = map[string]Repo{cfg.RepoRoot: {Sets: map[string]Set{"hotfix": {Stages: []Stage{{{Target: "deploy"}}}}}}}
	s, ok, err := cfg.Set("hotfix")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{cfg.MachinePath + " (repos)"}, s.DefinedIn)
}
