package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

const labOrigin = "https://bamboo.lab.example"

// stateDir keeps the tests' last-build records out of the source tree: a
// StateStore with an empty Path writes its temporary file into the package
// directory.
var stateDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bam-tui-state")
	if err != nil {
		panic(err)
	}
	stateDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

var errBoom = errors.New("bamboo returned 500")

func testService() *app.Service {
	f := &fake.Provider{
		BaseURL:  labOrigin,
		User:     provider.User{Name: "jdoe", FullName: "J Doe"},
		Info:     provider.ServerInfo{Version: "9.6.4"},
		Projects: []provider.Project{{Key: "PROJ", Name: "Example Project", URL: labOrigin + "/browse/PROJ"}},
		Plans: map[string][]provider.Plan{"PROJ": {
			{Key: "PROJ-BUILD", Name: "Build and test", ProjectKey: "PROJ", URL: labOrigin + "/browse/PROJ-BUILD"},
			{Key: "PROJ-PROV", Name: "Provision lab", ProjectKey: "PROJ", URL: labOrigin + "/browse/PROJ-PROV"},
		}},
		Branches: map[string][]provider.Branch{"PROJ-PROV": {
			{Key: "PROJ-PROV12", Name: "Provision lab - develop", ShortName: "develop", PlanKey: "PROJ-PROV"},
		}},
		TriggerResult: provider.Build{Key: "PROJ-PROV12-9", PlanKey: "PROJ-PROV12", Number: 9,
			State: provider.StateQueued, URL: labOrigin + "/browse/PROJ-PROV12-9"},
		Logs: map[string][]string{
			"PROJ-BUILD-INT-44": {
				"12:04:31  [INFO] Running integration suite",
				"12:06:58  [ERROR] ConnectionRefused: bamboo.lab.example:5432",
				"12:06:58  [ERROR] 3 tests failed",
				"12:07:01  Finished with exit code 1",
			},
			"PROJ-BUILD-COMP-44": {"12:03:02  compiled"},
			"PROJ-BUILD-UNIT-44": {"12:03:44  all tests passed"},
		},
		History: map[string][]provider.Build{
			"PROJ-BUILD": {
				{Key: "PROJ-BUILD-44", PlanKey: "PROJ-BUILD", Number: 44, State: provider.StateFailed,
					URL: labOrigin + "/browse/PROJ-BUILD-44", Duration: 192 * time.Second},
			},
			"PROJ-PROV12": {
				{Key: "PROJ-PROV12-8", PlanKey: "PROJ-PROV12", Number: 8, State: provider.StateSuccess,
					URL: labOrigin + "/browse/PROJ-PROV12-8"},
			},
		},
	}
	cfg := &config.Config{
		Machine: &config.MachineFile{
			Version: 1,
			Servers: map[string]config.Server{"lab": {URL: labOrigin}},
			// smoke names a branch, so resolving it must reach the branch
			// plan rather than the master.
			Targets: map[string]config.Target{
				"smoke": {Plan: "PROJ-PROV", Branch: "develop"},
				"build": {Plan: "PROJ-BUILD"},
			},
		},
		MachinePath: "config.yaml",
		RepoRoot:    "/repo",
	}
	return &app.Service{P: f, Cfg: cfg, Clock: app.SystemClock{},
		State:  &app.StateStore{Path: filepath.Join(stateDir, "state.json")},
		Getenv: func(string) string { return "" }}
}

func TestConnectCmdReportsServerAndUser(t *testing.T) {
	svc := testService()
	d := Deps{Connect: func(context.Context, string) (*app.Service, error) { return svc, nil }}
	msg := connectCmd(t.Context(), d, "lab", 0)()
	got, ok := msg.(connectedMsg)
	require.True(t, ok, "got %T", msg)
	require.Equal(t, "lab", got.Alias)
	require.Equal(t, "9.6.4", got.Info.Version)
	require.Equal(t, "jdoe", got.User.Name)
	require.Same(t, svc, got.Svc)
}

func TestConnectCmdSurfacesFailureAsErrMsg(t *testing.T) {
	boom := errors.New("no token for https://bamboo.lab.example")
	d := Deps{Connect: func(context.Context, string) (*app.Service, error) { return nil, boom }}
	msg := connectCmd(t.Context(), d, "lab", 0)()
	got, ok := msg.(errMsg)
	require.True(t, ok, "got %T", msg)
	require.ErrorIs(t, got.Err, boom)
	require.Equal(t, "connect", got.Where)
}

func TestLoadPlansCmdFlattensEveryProject(t *testing.T) {
	msg := loadPlansCmd(context.Background(), testService(), "", 0)()
	got, ok := msg.(plansLoadedMsg)
	require.True(t, ok, "got %T", msg)
	require.Len(t, got.Plans, 2)
	require.Equal(t, "PROJ-BUILD", got.Plans[0].Key)
	require.Equal(t, "PROJ-PROV", got.Plans[1].Key)
}

func TestLoadPlansCmdHonoursTheProjectFilter(t *testing.T) {
	msg := loadPlansCmd(context.Background(), testService(), "OPS", 0)()
	got := msg.(plansLoadedMsg)
	require.Empty(t, got.Plans)
}

func TestLoadPlansCmdUsesTheConfiguredProjects(t *testing.T) {
	svc := testService()
	f := svc.P.(*fake.Provider)
	f.Projects = append(f.Projects,
		provider.Project{Key: "OPS", Name: "Operations"},
		provider.Project{Key: "LAB", Name: "Lab"})
	f.Plans["OPS"] = []provider.Plan{{Key: "OPS-NIGHTLY", Name: "Nightly", ProjectKey: "OPS"}}
	f.Plans["LAB"] = []provider.Plan{{Key: "LAB-SMOKE", Name: "Smoke", ProjectKey: "LAB"}}
	svc.Cfg.Project = &config.ProjectFile{Version: 1, Projects: []string{"PROJ", "OPS", "GONE"}}

	got := loadPlansCmd(context.Background(), svc, "", 3)().(plansLoadedMsg)
	require.Equal(t, 3, got.Gen)
	keys := []string{}
	for _, p := range got.Plans {
		keys = append(keys, p.Key)
	}
	require.Equal(t, []string{"OPS-NIGHTLY", "PROJ-BUILD", "PROJ-PROV"}, keys, "LAB is not configured")
	require.Equal(t, []string{"GONE"}, got.Missing)
	require.Empty(t, got.Failed)
}

func TestLoadPlansCmdReportsAFailedProjectWithoutFailingTheOthers(t *testing.T) {
	svc := testService()
	f := svc.P.(*fake.Provider)
	// On the server, but the fake has no plans for it: ListPlans fails.
	f.Projects = append(f.Projects, provider.Project{Key: "OPS", Name: "Operations"})

	got := loadPlansCmd(context.Background(), svc, "", 0)().(plansLoadedMsg)
	require.Len(t, got.Plans, 2, "PROJ still loads")
	require.Equal(t, []string{"OPS"}, got.Failed)
	require.Error(t, got.Err)
}

func TestLoadProjectsCmdListsOnlyTheConfiguredProjects(t *testing.T) {
	svc := testService()
	f := svc.P.(*fake.Provider)
	f.Projects = append(f.Projects,
		provider.Project{Key: "OPS", Name: "Operations"},
		provider.Project{Key: "LAB", Name: "Lab"})
	svc.Cfg.Project = &config.ProjectFile{Version: 1, Projects: []string{"OPS", "PROJ"}}

	got := loadProjectsCmd(context.Background(), svc, streamPicker, 0)().(projectsLoadedMsg)
	keys := []string{}
	for _, p := range got.Projects {
		keys = append(keys, p.Key)
	}
	require.Equal(t, []string{"OPS", "PROJ"}, keys)
}

func TestLoadProjectsCmdListsEveryProjectWhenNoneConfigured(t *testing.T) {
	svc := testService()
	f := svc.P.(*fake.Provider)
	f.Projects = append(f.Projects, provider.Project{Key: "OPS", Name: "Operations"})

	got := loadProjectsCmd(context.Background(), svc, streamPicker, 0)().(projectsLoadedMsg)
	keys := []string{}
	for _, p := range got.Projects {
		keys = append(keys, p.Key)
	}
	require.Equal(t, []string{"PROJ", "OPS"}, keys)
}

func TestLoadBuildsCmdCarriesThePlanKey(t *testing.T) {
	msg := loadBuildsCmd(context.Background(), testService(), "PROJ-BUILD", 25, 0)()
	got, ok := msg.(buildsLoadedMsg)
	require.True(t, ok, "got %T", msg)
	require.Equal(t, "PROJ-BUILD", got.PlanKey)
	require.Len(t, got.Builds, 1)
}

// TestModelStoresWhatItLoads: the messages must land in the panels.
func TestModelStoresWhatItLoads(t *testing.T) {
	m := testModel()
	m, _ = send(m, connectedMsg{Alias: "lab", Svc: testService(),
		Info: provider.ServerInfo{Version: "9.6.4"}, User: provider.User{Name: "jdoe"}})
	require.Equal(t, "9.6.4", m.info.Version)

	// connectedMsg issued a plans load, so the reply must carry that
	// generation to be accepted.
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{{Key: "PROJ-BUILD"}, {Key: "OPS-NIGHTLY"}}})
	require.Equal(t, 2, m.plans.len())

	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: []provider.Build{{Key: "PROJ-BUILD-44"}}})
	require.Equal(t, 1, m.builds.len())
}

// TestErrMsgNeverQuits: a Bamboo error belongs in the status bar, not in an
// exit. Spec §6.
func TestErrMsgNeverQuits(t *testing.T) {
	m, cmd := send(testModel(), errMsg{Err: errBoom, Where: "plans"})
	require.Nil(t, cmd)
	require.Error(t, m.err)
}

// TestRunReturnsTheFirstConnectFailure is spec §6: a failure to start maps to
// an exit code, and only later failures stay in the status bar. Run must
// therefore not enter the program loop when the first connect fails.
func TestRunReturnsTheFirstConnectFailure(t *testing.T) {
	boom := errs.Authf("no token for %s", labOrigin)
	err := Run(t.Context(), Deps{
		Initial: "lab",
		Connect: func(context.Context, string) (*app.Service, error) { return nil, boom },
	})
	require.ErrorIs(t, err, boom)
}

// TestRunWithoutAConnectIsAConfigError: no configured server means nothing to
// open, and that is a start-up failure too.
func TestRunWithoutAConnectIsAConfigError(t *testing.T) {
	err := Run(t.Context(), Deps{})
	require.Error(t, err)
	require.Equal(t, errs.KindConfig, errs.KindOf(err))
}

// TestInitDoesNotReconnectWhenRunAlreadyDid: the handshake connected, so Init
// loads the panels instead of dialling again.
func TestInitConnectsOnlyWhenNotAlreadyConnected(t *testing.T) {
	m := testModel()
	m.svc = testService()
	require.NotNil(t, m.Init())

	calls := 0
	m2 := New(Deps{Initial: "lab", Connect: func(context.Context, string) (*app.Service, error) {
		calls++
		return testService(), nil
	}})
	require.NotNil(t, m2.Init())
	require.Equal(t, 0, calls, "Init returns the command; it does not run it")
}

func TestLoadProjectsCmdReportsConfiguredProjectsMissingOnTheServer(t *testing.T) {
	svc := testService()
	f := svc.P.(*fake.Provider)
	f.Projects = append(f.Projects, provider.Project{Key: "OPS", Name: "Operations"})
	svc.Cfg.Project = &config.ProjectFile{Version: 1, Projects: []string{"OPS", "GONE", "PROJ"}}

	got := loadProjectsCmd(context.Background(), svc, streamProjects, 7)().(projectsLoadedMsg)
	require.Equal(t, streamProjects, got.Stream)
	require.Equal(t, 7, got.Gen)
	require.Equal(t, []string{"GONE"}, got.Missing)
	require.Len(t, got.Projects, 2)
}

func TestLoadProjectsCmdMissesNothingWhenNoneConfigured(t *testing.T) {
	got := loadProjectsCmd(context.Background(), testService(), streamPicker, 0)().(projectsLoadedMsg)
	require.Empty(t, got.Missing)
	require.Equal(t, streamPicker, got.Stream)
}
