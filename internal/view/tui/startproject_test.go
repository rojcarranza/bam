package tui

import (
	"context"
	"testing"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// serviceWithDefault is testService with default_project set in the project
// file, or none when key is "".
func serviceWithDefault(key string) *app.Service {
	svc := testService()
	if key != "" {
		svc.Cfg.Project = &config.ProjectFile{Version: 1, DefaultProject: key}
	}
	return svc
}

func TestDepsProjectOpensOnThatProject(t *testing.T) {
	m := New(Deps{Initial: "lab", Project: "proj"})
	m, _ = send(m, connectedMsg{Alias: "lab", Svc: serviceWithDefault("OPS")})
	require.Equal(t, "PROJ", m.project, "the flag beats the saved default, uppercased")
}

func TestDepsProjectAllIgnoresTheSavedDefault(t *testing.T) {
	m := New(Deps{Initial: "lab", Project: "all"})
	m, _ = send(m, connectedMsg{Alias: "lab", Svc: serviceWithDefault("PROJ")})
	require.Empty(t, m.project)
}

func TestSavedDefaultProjectAppliesOnConnect(t *testing.T) {
	m := New(Deps{Initial: "lab"})
	m = m.connected(connectedMsg{Alias: "lab", Svc: serviceWithDefault("PROJ")})
	require.Equal(t, "PROJ", m.project, "Run's first connect goes through connected")
}

// TestServerSwitchAppliesTheNewServersDefault: the flag is for the server bam
// opens on; another server opens on its own default_project.
func TestServerSwitchAppliesTheNewServersDefault(t *testing.T) {
	d := Deps{
		Servers: []Server{{Alias: "lab"}, {Alias: "work"}}, Initial: "lab", Project: "PROJ",
		Connect: func(context.Context, string) (*app.Service, error) { return testService(), nil },
	}
	m := New(d).connected(connectedMsg{Alias: "lab", Svc: testService()})
	require.Equal(t, "PROJ", m.project)

	next, _ := m.switchServer("work")
	m = next.(Model)
	m, _ = send(m, connectedMsg{Gen: m.connGen, Alias: "work", Svc: serviceWithDefault("OPS")})
	require.Equal(t, "OPS", m.project)

	next, _ = m.switchServer("lab")
	m = next.(Model)
	m, _ = send(m, connectedMsg{Gen: m.connGen, Alias: "lab", Svc: testService()})
	require.Empty(t, m.project, "the flag is not applied again")
}

func TestLoadPlansCmdFallsBackToEveryProjectWhenTheFilterIsNotOnTheServer(t *testing.T) {
	got := loadPlansCmd(context.Background(), testService(), "OPS", 0)().(plansLoadedMsg)
	require.Len(t, got.Plans, 2, "PROJ's plans")
	require.Equal(t, "OPS", got.ProjectMissing)
}

func TestLoadPlansCmdLoadsAProjectOutsideTheConfiguredList(t *testing.T) {
	svc := testService()
	f := svc.P.(*fake.Provider)
	f.Projects = append(f.Projects, provider.Project{Key: "LAB", Name: "Lab"})
	f.Plans["LAB"] = []provider.Plan{{Key: "LAB-SMOKE", Name: "Smoke", ProjectKey: "LAB"}}
	svc.Cfg.Project = &config.ProjectFile{Version: 1, Projects: []string{"PROJ"}}

	got := loadPlansCmd(context.Background(), svc, "LAB", 0)().(plansLoadedMsg)
	require.Len(t, got.Plans, 1)
	require.Equal(t, "LAB-SMOKE", got.Plans[0].Key)
	require.Empty(t, got.ProjectMissing)
}

func TestAMissingProjectFilterWidensToEveryProject(t *testing.T) {
	m := New(Deps{Initial: "lab", Project: "OPS"})
	m, _ = send(m, connectedMsg{Alias: "lab", Svc: testService()})
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, ProjectMissing: "OPS",
		Plans: []provider.Plan{{Key: "PROJ-BUILD", ProjectKey: "PROJ"}}})
	require.Empty(t, m.project)
	require.Equal(t, "project OPS not found on lab", m.status)
	require.Equal(t, 1, m.plans.len())
}
