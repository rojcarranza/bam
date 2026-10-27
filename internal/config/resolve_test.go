package config

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func baseConfig() *Config {
	return &Config{
		RepoRoot:    "/home/jdoe/src/repo",
		Home:        "/home/jdoe",
		ProjectPath: "/home/jdoe/src/repo/.bam.yaml",
		MachinePath: "/home/jdoe/.config/bam/config.yaml",
		Project: &ProjectFile{Version: 1,
			Servers:       map[string]Server{"work": {URL: "https://bamboo.example.com"}},
			DefaultServer: "work",
			Projects:      []string{"PROJ", "OPS"},
		},
		Machine: &MachineFile{Version: 1,
			DefaultServer: "home",
			Servers: map[string]Server{
				"home": {URL: "http://bamboo.lab.example:8085", Projects: []string{"LAB"}},
				"work": {URL: "https://bamboo-eu.example.com", AuthEnv: "BAM_WORK_TOKEN"},
			},
		},
	}
}

func alias(t *testing.T, c *Config, flag, target string) string {
	t.Helper()
	s, err := c.SelectServer(flag, target)
	require.NoError(t, err)
	return s.Alias
}

func TestServerSelectionOrder(t *testing.T) {
	c := baseConfig()
	assert.Equal(t, "work", alias(t, c, "", ""), "project default beats machine global default")
	assert.Equal(t, "home", alias(t, c, "home", ""), "flag wins")
	assert.Equal(t, "home", alias(t, c, "", "home"), "target server beats project default")

	c.Env.Server = "home"
	assert.Equal(t, "home", alias(t, c, "", "work"), "BAM_SERVER beats target server")
	assert.Equal(t, "work", alias(t, c, "work", ""), "flag beats BAM_SERVER")
	c.Env.Server = ""

	c.Machine.Repos = map[string]Repo{"/home/jdoe/src/repo": {Server: "home"}}
	assert.Equal(t, "home", alias(t, c, "", ""), "machine repos entry beats project default")

	c.Machine.Repos = nil
	c.Project = nil
	assert.Equal(t, "home", alias(t, c, "", ""), "machine default when no project file")
}

func TestServerSelectionEnvURL(t *testing.T) {
	c := baseConfig()
	c.Env = Env{URL: "https://ci.bamboo.example.com/", Token: "tok"}
	s, err := c.SelectServer("", "")
	require.NoError(t, err)
	assert.Equal(t, EnvServerAlias, s.Alias)
	assert.Equal(t, "https://ci.bamboo.example.com/", s.URL)
	assert.Equal(t, "tok", s.Token)

	s, err = c.SelectServer("work", "")
	require.NoError(t, err)
	assert.Equal(t, "", s.Token, "BAM_TOKEN never attaches to a configured alias")
}

func TestServerSelectionSingleAndNone(t *testing.T) {
	c := &Config{Machine: &MachineFile{Version: 1, Servers: map[string]Server{"only": {URL: "https://bamboo.example.com"}}}}
	assert.Equal(t, "only", alias(t, c, "", ""))

	c.Machine.Servers["second"] = Server{URL: "https://b.example.com"}
	_, err := c.SelectServer("", "")
	require.Error(t, err)
	assert.Equal(t, errs.KindConfig, errs.KindOf(err))
	assert.Contains(t, err.Error(), "only, second")

	_, err = c.SelectServer("missing", "")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Contains(t, e.Try, "bam server add missing --url")
	assert.Equal(t, "servers: only, second", e.Why, "a mistyped alias shows the real ones")

	_, err = (&Config{Machine: &MachineFile{Version: 1}}).SelectServer("missing", "")
	require.ErrorAs(t, err, &e)
	assert.Empty(t, e.Why, "no list when nothing is configured")
}

func TestServerFieldsMergeWithMachineWinning(t *testing.T) {
	c := baseConfig()
	c.Project.Servers["work"] = Server{URL: "https://bamboo.example.com", Projects: []string{"PROJ"}}
	s := c.Servers()["work"]
	assert.Equal(t, "https://bamboo-eu.example.com", s.URL)
	assert.Equal(t, "BAM_WORK_TOKEN", s.AuthEnv)
	assert.Equal(t, []string{"PROJ"}, s.Projects, "project-only field survives the override")
	assert.Equal(t, []string{"project", "machine"}, s.DefinedIn)
}

func TestMachineAuthEnvWithoutURLIsNotAppliedToProjectURL(t *testing.T) {
	c := baseConfig()
	// Machine sets auth_env for "work" but not url; the project sets url.
	c.Machine.Servers["work"] = Server{AuthEnv: "BAM_WORK_TOKEN"}
	s := c.Servers()["work"]
	assert.Equal(t, "https://bamboo.example.com", s.URL, "project url survives")
	assert.Equal(t, "", s.AuthEnv, "machine auth_env without its own url is never applied")
}

func TestMachineAuthEnvWithURLIsKept(t *testing.T) {
	c := baseConfig()
	// baseConfig's machine "work" already sets both url and auth_env.
	s := c.Servers()["work"]
	assert.Equal(t, "BAM_WORK_TOKEN", s.AuthEnv)
	assert.Equal(t, "https://bamboo-eu.example.com", s.URL)
}

func TestProjectLayerAuthEnvIsNeverApplied(t *testing.T) {
	c := baseConfig()
	// Constructed directly (bypassing Load/validation) to prove Servers()
	// itself never lets a project-layer auth_env reach ResolvedServer, even
	// if some future code path skips validation.
	c.Project.Servers["evil"] = Server{URL: "https://bamboo.example.com", AuthEnv: "SOME_OTHER_SECRET"}
	s := c.Servers()["evil"]
	assert.Equal(t, "https://bamboo.example.com", s.URL)
	assert.Equal(t, "", s.AuthEnv, "a project-layer auth_env must never reach ResolvedServer")
}

func TestProjectKeysOrder(t *testing.T) {
	c := baseConfig()
	work := c.Servers()["work"]
	assert.Equal(t, []string{"PROJ", "OPS"}, c.ProjectKeys(work))

	c.Machine.Repos = map[string]Repo{"~/src/repo": {Projects: []string{"X"}}}
	assert.Equal(t, []string{"X"}, c.ProjectKeys(work), "repos keys may start with ~/")

	c.Machine.Repos = nil
	c.Project = nil
	assert.Equal(t, []string{"LAB"}, c.ProjectKeys(c.Servers()["home"]))
}

func TestTargetsMergeAcrossThreeSources(t *testing.T) {
	yes := true
	c := baseConfig()
	c.Project.Targets = map[string]Target{"lab": {
		Plan: "PROJ-LAB", Defaults: StringMap{"a": "1", "b": "2"},
		Options: StringListMap{"a": {"1", "9"}}, Required: []string{"x", "y"},
	}}
	c.Machine.Targets = map[string]Target{"lab": {Defaults: StringMap{"b": "3"}, Watch: &yes}}
	c.Machine.Repos = map[string]Repo{"/home/jdoe/src/repo": {Targets: map[string]Target{
		"lab":  {Required: []string{"z"}, Timeout: Duration(5 * time.Minute)},
		"mine": {Plan: "PROJ-MINE"},
	}}}

	all, err := c.Targets()
	require.NoError(t, err)
	lab := all["lab"]
	assert.Equal(t, "PROJ-LAB", lab.Plan)
	assert.Equal(t, StringMap{"a": "1", "b": "3"}, lab.Defaults)
	assert.Equal(t, []string{"z"}, lab.Required, "required is replaced as a whole")
	assert.True(t, *lab.Watch)
	assert.Equal(t, Duration(5*time.Minute), lab.Timeout)
	assert.Len(t, lab.DefinedIn, 3)
	assert.Equal(t, "PROJ-MINE", all["mine"].Plan)

	got, ok, err := c.Target("mine")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "mine", got.Name)
}

func TestTargetsRecordEachLayer(t *testing.T) {
	c := baseConfig()
	c.Home = "/home/jdoe"
	c.Project.Targets = map[string]Target{"lab": {Plan: "PROJ-LAB", Defaults: StringMap{"a": "1"}}}
	c.Machine.Targets = map[string]Target{"lab": {Defaults: StringMap{"b": "3"}}}
	c.Machine.Repos = map[string]Repo{"~/src/repo": {Targets: map[string]Target{"lab": {Plan: "PROJ-LAB2"}}}}

	lab, ok, err := c.Target("lab")
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, lab.Layers, 3)
	assert.Equal(t, TargetLayer{Path: c.ProjectPath, KeyPath: []string{"targets"}, Target: c.Project.Targets["lab"]}, lab.Layers[0])
	assert.Equal(t, TargetLayer{Path: c.MachinePath, KeyPath: []string{"targets"}, Target: c.Machine.Targets["lab"]}, lab.Layers[1])
	assert.Equal(t, []string{"repos", "~/src/repo", "targets"}, lab.Layers[2].KeyPath, "the raw repos key, as written")
	assert.Equal(t, c.MachinePath, lab.Layers[2].Path)
	assert.Equal(t, c.MachinePath+" (repos)", lab.DefinedIn[2], "defined_in is unchanged")
}

func TestMergedTargetValidation(t *testing.T) {
	cases := map[string]struct {
		target Target
		want   string
	}{
		"no plan":                   {Target{Branch: "develop"}, "has no plan"},
		"default not option":        {Target{Plan: "PROJ-A", Defaults: StringMap{"a": "5"}, Options: StringListMap{"a": {"1", "2"}}}, `"5" is not one of 1, 2`},
		"masked default not option": {Target{Plan: "PROJ-A", Defaults: StringMap{"db_password": "hunter2"}, Options: StringListMap{"db_password": {"a", "b"}}}, `db_password="********" is not one of a, b`},
		"unknown server":            {Target{Plan: "PROJ-A", Server: "nope"}, `unknown server "nope"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := baseConfig()
			c.Project.Targets = map[string]Target{"t": tc.target}
			_, err := c.Targets()
			require.Error(t, err)
			assert.Equal(t, errs.KindConfig, errs.KindOf(err))
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestEnvRefDefaultSkipsOptionsCheck(t *testing.T) {
	c := baseConfig()
	c.Project.Targets = map[string]Target{"t": {Plan: "PROJ-A", Defaults: StringMap{"a": "${A}"}, Options: StringListMap{"a": {"1"}}}}
	_, err := c.Targets()
	assert.NoError(t, err)
}

func TestDefaultProjectOrder(t *testing.T) {
	c := baseConfig()
	c.Machine.Servers["work"] = Server{URL: "https://bamboo.example.com", DefaultProject: "lab"}
	work := c.Servers()["work"]
	assert.Equal(t, "LAB", c.DefaultProject(work), "server entry, uppercased")

	c.Project.DefaultProject = "ops"
	assert.Equal(t, "OPS", c.DefaultProject(work), "project file beats the server entry")

	c.Machine.Repos = map[string]Repo{"~/src/repo": {DefaultProject: "proj"}}
	assert.Equal(t, "PROJ", c.DefaultProject(work), "repos entry beats the project file")

	c.Machine.Repos = nil
	c.Project = nil
	assert.Empty(t, c.DefaultProject(c.Servers()["home"]), "nothing set means all projects")
}
