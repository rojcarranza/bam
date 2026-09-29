package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// setService is newService with a two-stage set "release":
// [publish-specs] then [provision-lab with cluster_name and compute_nodes].
func setService(t *testing.T, p *fake.Provider) *Service {
	t.Helper()
	svc := newService(t, p)
	svc.Cfg.Project.Targets["publish-specs"] = config.Target{Plan: "PROJ-BUILD"}
	svc.Cfg.Project.Sets = map[string]config.Set{"release": {Stages: []config.Stage{
		{{Target: "publish-specs"}},
		{{Target: "provision-lab", Vars: config.StringMap{"cluster_name": "alpha", "compute_nodes": "4"}}},
	}}}
	return svc
}

func TestPlanSetResolvesEveryRunWithSetVarsOverDefaults(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(k string) string { return map[string]string{"LAB_DB_PASSWORD": "s3cret"}[k] }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{Vars: []string{"compute_nodes=5"}})
	require.NoError(t, err)
	require.Equal(t, "release", p.Name)
	require.Equal(t, "https://bamboo.example.com", p.Origin)
	require.Len(t, p.Stages, 2)
	require.Equal(t, "PROJ-BUILD", p.Stages[0][0].Ref.PlanKey)
	prov := p.Stages[1][0]
	require.Equal(t, "provision-lab", prov.Target)
	require.Equal(t, "PROJ-PROV12", prov.Ref.PlanKey, "the preset's branch applies")
	name, _ := prov.Vars.Get("cluster_name")
	require.Equal(t, "alpha", name.Value, "set vars beat preset defaults")
	nodes, _ := prov.Vars.Get("compute_nodes")
	require.Equal(t, "5", nodes.Value, "--var beats set vars")
	typ, _ := prov.Vars.Get("cluster_type")
	require.Equal(t, "k8s", typ.Value, "preset defaults still apply")
	require.Equal(t, []string{"compute_nodes"}, p.VarNames)
	require.Equal(t, "k8s", svc.Cfg.Project.Targets["provision-lab"].Defaults["cluster_type"])
	require.NotContains(t, svc.Cfg.Project.Targets["provision-lab"].Defaults, "cluster_name", "the preset itself is untouched")
}

func TestPlanSetCollectsEveryError(t *testing.T) {
	svc := setService(t, fakeBamboo())
	prov := svc.Cfg.Project.Targets["provision-lab"]
	prov.Branch = "no-such-branch"
	svc.Cfg.Project.Targets["provision-lab"] = prov
	svc.Cfg.Project.Targets["build"] = config.Target{Plan: "PROJ-BUILD", Required: []string{"release_tag"}}
	svc.Cfg.Project.Sets["release"] = config.Set{Stages: []config.Stage{
		{{Target: "provision-lab"}},
		{{Target: "build"}},
		{{Target: "publish-specs"}},
	}}
	_, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.Error(t, err)
	require.Equal(t, errs.KindUsage, errs.KindOf(err))
	text := err.Error() + " " + errTry(err)
	require.Contains(t, text, "provision-lab")
	require.Contains(t, text, "no-such-branch")
	require.Contains(t, text, "stage 2 build")
	require.Contains(t, text, "release_tag")
	require.Empty(t, svc.P.(*fake.Provider).Triggered)
}

func TestPlanSetWarnsAboutAnUndeclaredVar(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{Vars: []string{"global_flag=1"}})
	require.NoError(t, err)
	joined := strings.Join(p.Warnings, "\n")
	require.Contains(t, joined, "global_flag")
	require.Contains(t, joined, "publish-specs: global_flag is not a declared variable of PROJ-BUILD")
	require.Contains(t, joined, "provision-lab: global_flag is not a declared variable of PROJ-PROV")
	v, ok := p.Stages[1][0].Vars.Get("global_flag")
	require.True(t, ok, "an undeclared --var is still sent")
	require.Equal(t, "1", v.Value)
}

func TestPlanSetRejectsAnotherServer(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Cfg.Project.Servers["lab"] = config.Server{URL: "https://bamboo.lab.example"}
	tg := svc.Cfg.Project.Targets["publish-specs"]
	tg.Server = "lab"
	svc.Cfg.Project.Targets["publish-specs"] = tg
	_, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.Equal(t, errs.KindConfig, errs.KindOf(err))
	require.Contains(t, err.Error(), "bamboo.lab.example")
}

func TestPlanSetUnknownSet(t *testing.T) {
	_, err := setService(t, fakeBamboo()).PlanSet(context.Background(), "nope", SetOptions{})
	require.Equal(t, errs.KindUsage, errs.KindOf(err))
}

func errTry(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Why + " " + e.Try
	}
	return ""
}
