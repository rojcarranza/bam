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

func TestSetUnknownPresetWithNoPresets(t *testing.T) {
	cfg, err := loadYAML(t, "version: 1\nsets: {rel: {stages: [[deploy]]}}\n", "")
	if err == nil {
		_, err = cfg.Sets()
	}
	require.Error(t, err)
	require.Contains(t, errWhyTry(err), "bam preset add deploy --plan PROJ-PLAN creates it")
}

func TestSetPlanKeyWithNoPresets(t *testing.T) {
	cfg, err := loadYAML(t, "version: 1\nsets: {rel: {stages: [[PROJ-BUILD]]}}\n", "")
	if err == nil {
		_, err = cfg.Sets()
	}
	require.Error(t, err)
	require.Contains(t, errWhyTry(err), "bam preset add build --plan PROJ-BUILD creates one")
}

func TestSetConfigErrors(t *testing.T) {
	base := "version: 1\nservers: {work: {url: https://bamboo.example.com}, lab: {url: https://bamboo.lab.example}}\n" +
		"targets:\n  a: {plan: PROJ-A}\n  b: {plan: PROJ-B, server: work}\n  c: {plan: PROJ-C, server: lab}\n"
	for name, tc := range map[string]struct{ sets, want string }{
		"unknown preset":   {"sets: {rel: {stages: [[nope]]}}", `presets here: a, b, c`},
		"plan key":         {"sets: {rel: {stages: [[PROJ-BUILD]]}}", "a set lists presets, not plans; bam preset add build --plan PROJ-BUILD creates one"},
		"close preset":     {"sets: {rel: {stages: [[a, bb]]}}", "did you mean b?"},
		"no stages":        {"sets: {rel: {stages: []}}", "has no stages"},
		"empty stage":      {"sets: {rel: {stages: [[a], []]}}", "stage 2 has no runs"},
		"duplicate":        {"sets: {rel: {stages: [[a, a]]}}", `run "a" twice in stage 1`},
		"duplicate matrix": {"sets: {rel: {stages: [[{target: a, matrix: [{env: qa}, {env: qa}]}]]}}", `run "a[env=qa]" twice in stage 1`},
		"too many runs":    {"sets: {rel: {stages: [[{target: a, matrix: {x: [1,2,3,4,5,6,7,8], y: [1,2,3,4,5,6,7,8]}}]]}}", "stage 1 expands to 64 runs; the limit is 50"},
		"matrix secret":    {"sets: {rel: {stages: [[{target: a, matrix: [{db_password: hunter2}]}]]}}", "holds a literal secret"},

		"servers":          {"sets: {rel: {stages: [[b], [c]]}}", `servers "work" and "lab"`},
		"bad name":         {"sets: {Rel: {stages: [[a]]}}", `invalid set name "Rel"`},
		"bad timeout":      {"sets: {rel: {timeout: soon, stages: [[a]]}}", "not a duration"},
		"negative timeout": {"sets: {rel: {timeout: -5m, stages: [[a]]}}", `"-5m" is not a duration like 45m`},
		"unknown field":    {"sets: {rel: {stages: [[{target: a, env: x}]]}}", `unknown field "env"`},
		"literal secret":   {"sets: {rel: {stages: [[{target: a, vars: {db_password: hunter2}}]]}}", "holds a literal secret"},
		"scalar set":       {"sets: {rel: a}", "line 7: expected a set (stages: [[preset], ...]), found a single value"},
		"set field typo":   {"sets: {rel: {stage: [[a]]}}", `line 7: unknown field "stage" in a set`},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := loadYAML(t, base+tc.sets+"\n", "")
			if err == nil {
				_, err = cfg.Sets()
			}
			require.Error(t, err)
			require.Equal(t, errs.KindConfig, errs.KindOf(err))
			require.Contains(t, err.Error()+" "+errWhyTry(err), tc.want)
		})
	}
}

func TestSetAsABareListOfStages(t *testing.T) {
	cfg, err := loadYAML(t, "version: 1\nservers: {work: {url: https://bamboo.example.com}}\ntargets:\n  a: {plan: PROJ-A}\n  b: {plan: PROJ-B}\n"+
		"sets: {rel: [[a], [b, {target: a, vars: {env: qa}}]]}\n", "")
	require.NoError(t, err)
	s, ok, err := cfg.Set("rel")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []Stage{{{Target: "a"}}, {{Target: "b"}, {Target: "a", Vars: StringMap{"env": "qa"}}}}, s.Stages)
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

func TestSetMatrixAllowsAPresetTwiceInAStage(t *testing.T) {
	cfg, err := loadYAML(t, "version: 1\nservers: {work: {url: https://bamboo.example.com}}\ntargets:\n  a: {plan: PROJ-A}\n"+
		"sets: {rel: {stages: [[{target: a, matrix: [{env: qa}]}, {target: a, matrix: [{env: prod}]}]]}}\n", "")
	require.NoError(t, err)
	_, err = cfg.Sets()
	require.NoError(t, err)
}

// A product of 10^10 runs must be refused from its size, not by building it:
// building it would hang every command that loads the configuration.
func TestSetHugeProductIsRefusedWithoutExpanding(t *testing.T) {
	ten := "[0,1,2,3,4,5,6,7,8,9]"
	keys := ""
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		keys += k + ": " + ten + ", "
	}
	src := "version: 1\nservers: {work: {url: https://bamboo.example.com}}\ntargets:\n  a: {plan: PROJ-A}\n" +
		"sets: {rel: {stages: [[{target: a, matrix: {" + keys + "}}]]}}\n"
	cfg, err := loadYAML(t, src, "")
	if err == nil {
		_, err = cfg.Sets()
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "stage 1 expands to 10000000000 runs; the limit is 50")
}

// With several literal secrets, the error always names the first variable in
// sorted order, so the message is the same on every run.
func TestSetLiteralSecretErrorIsDeterministic(t *testing.T) {
	src := "version: 1\nservers: {work: {url: https://bamboo.example.com}}\ntargets:\n  a: {plan: PROJ-A}\n" +
		"sets: {rel: {stages: [[{target: a, vars: {z_password: x, m_secret: y}, matrix: [{b_passphrase: p, a_password: q}]}]]}}\n"
	for i := 0; i < 30; i++ {
		_, err := loadYAML(t, src, "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "vars.a_password holds a literal secret")
	}
}

func TestWrongShapeNamesTheExpectedShape(t *testing.T) {
	for yml, want := range map[string]string{
		"version: 1\ntargets: {d: PROJ-D}\n":      "line 2: expected a preset (plan: KEY, ...), found a single value `PROJ-D`",
		"version: 1\nprojects: PROJ\n":            "line 2: expected a list, found a single value `PROJ`",
		"version: 1\nservers: {work: [x]}\n":      "line 2: expected a server (url: URL, ...), found a list",
		"version: 1\ntargets: {d: {plan: [x]}}\n": "line 2: expected a single value, found a list",
		"version: 1\ntargets: {d: {plann: X}}\n":  `line 2: unknown field "plann" in a preset`,
		"version: 1\nservrs: {}\n":                `line 2: unknown field "servrs" in the project config`,
	} {
		_, err := loadYAML(t, yml, "")
		require.Error(t, err)
		require.Contains(t, errWhyTry(err), want, yml)
		require.NotContains(t, errWhyTry(err), "config.", yml)
	}
}

func TestUnknownFieldSuggestsTheClosest(t *testing.T) {
	for yml, want := range map[string]string{
		"version: 1\ntargets: {d: {plan: X, defauts: {a: b}}}\n": "did you mean defaults?",
		"version: 1\nservrs: {}\n":                               "did you mean servers?",
		"version: 1\nservers: {w: {ur: http://x}}\n":             "did you mean url?",
		"version: 1\nsets: {rel: {stage: [[a]]}}\n":              "did you mean stages?",
		"version: 1\nsets: {rel: [[{targt: a}]]}\n":              "did you mean target?",
	} {
		_, err := loadYAML(t, yml, "")
		require.Error(t, err)
		require.Contains(t, errWhyTry(err), want, yml)
	}
	_, err := loadYAML(t, "version: 1\nzzzzzz: 1\n", "")
	require.Error(t, err)
	require.NotContains(t, errWhyTry(err), "did you mean", "no hint when nothing is close")
}

func TestVariableWithSeveralValuesPointsAtOptions(t *testing.T) {
	for yml, want := range map[string]string{
		"version: 1\ntargets:\n  d:\n    plan: PROJ-D\n    variables:\n      env:\n        required: true\n": `line 7: variable "env" takes a single value, like env: VALUE; found a mapping`,
		"version: 1\ntargets: {d: {plan: PROJ-D, defaults: {env: [dev, prod]}}}\n":                           `line 2: variable "env" takes a single value, like env: VALUE; found a list`,
	} {
		_, err := loadYAML(t, yml, "")
		require.Error(t, err)
		require.Contains(t, errWhyTry(err), want, yml)
		require.Contains(t, errWhyTry(err), "options: {env: [a, b]} and must-set names under required: [env]", yml)
	}
}
