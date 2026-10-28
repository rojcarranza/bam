package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRuntime(h *harness) *runtime {
	env := h.env
	env.Stdin = nil
	return &runtime{env: env}
}

func TestConnectUsesTokenForSelectedOrigin(t *testing.T) {
	h := newHarness(t)
	r := newRuntime(h)
	svc, _, err := r.connect(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, "work", svc.Server.Alias)
	assert.Equal(t, workOrigin, svc.Origin)
	require.Len(t, h.connectOpts, 1)
	assert.Equal(t, "tok", h.connectOpts[0].Token)
	assert.Equal(t, "https://bamboo.example.com", h.connectOpts[0].BaseURL)
}

func TestConnectWithoutTokenIsAuthError(t *testing.T) {
	h := newHarness(t)
	h.kr.m = map[string]string{}
	_, _, err := newRuntime(h).connect(context.Background(), "")
	require.Error(t, err)
	assert.Equal(t, errs.KindAuth, errs.KindOf(err))
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "bam login work", e.Try)
	assert.Empty(t, e.Why)
}

func TestBAMTokenOnlyForBAMURL(t *testing.T) {
	h := newHarness(t)
	h.kr.m = map[string]string{}
	h.vars["BAM_TOKEN"] = "ci-token"
	_, _, err := newRuntime(h).connect(context.Background(), "")
	assert.Equal(t, errs.KindAuth, errs.KindOf(err), "BAM_TOKEN alone never reaches the work alias")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "BAM_TOKEN is set, but it applies only to the server named by BAM_URL", e.Why)

	h.vars["BAM_URL"] = "https://ci.bamboo.example.com"
	svc, _, err := newRuntime(h).connect(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, "env", svc.Server.Alias)
	assert.Equal(t, "ci-token", h.connectOpts[len(h.connectOpts)-1].Token)
}

func TestConnectForTargetUsesTargetServer(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(h.env.Paths.MachineConfig), 0o700))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig, []byte(
		"version: 1\nservers:\n  home:\n    url: http://bamboo.lab.example:8085\ntargets:\n  lab-smoke:\n    server: home\n    plan: LAB-SMOKE\n"), 0o644))
	h.kr.m["bam|http://bamboo.lab.example:8085"] = "home-tok"
	svc, _, err := newRuntime(h).connectFor(context.Background(), "lab-smoke")
	require.NoError(t, err)
	assert.Equal(t, "home", svc.Server.Alias)
	assert.Equal(t, "home-tok", h.connectOpts[0].Token)
}

func TestConnectLastSelectsTheOriginsServer(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(h.env.Paths.MachineConfig), 0o700))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig, []byte(
		"version: 1\nservers:\n  home:\n    url: http://bamboo.lab.example:8085\n"), 0o644))
	h.kr.m["bam|http://bamboo.lab.example:8085"] = "home-tok"
	require.NoError(t, (&app.StateStore{Path: h.env.Paths.State}).SetLast(h.root, app.LastRecord{
		BuildKey: "PROJ-PROV12-8", Origin: "http://bamboo.lab.example:8085", PlanKey: "PROJ-PROV12",
	}))
	svc, _, err := newRuntime(h).connectLast(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "home", svc.Server.Alias)
	assert.Equal(t, "home-tok", h.connectOpts[0].Token)
}

func TestConnectLastWithNoMatchingServerIsConfigError(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, (&app.StateStore{Path: h.env.Paths.State}).SetLast(h.root, app.LastRecord{
		BuildKey: "LAB-SMOKE-3", Origin: "http://bamboo.lab.example:8085", PlanKey: "LAB-SMOKE",
	}))
	_, _, err := newRuntime(h).connectLast(context.Background())
	require.Error(t, err)
	assert.Equal(t, errs.KindConfig, errs.KindOf(err))
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Contains(t, e.Try, "bam server add")
}

func TestConnectLastWithoutRecordFallsBackToDefaultServer(t *testing.T) {
	h := newHarness(t)
	svc, _, err := newRuntime(h).connectLast(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "work", svc.Server.Alias)
}

func TestColorFlagValidated(t *testing.T) {
	h := newHarness(t)
	r := newRuntime(h)
	r.flags.color = "sometimes"
	_, err := r.out()
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
}

func TestBuildArgAcceptsAPageLinkOnItsServer(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(h.env.Paths.MachineConfig), 0o700))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig, []byte(
		"version: 1\nservers:\n  home:\n    url: http://bamboo.lab.example:8085\n"), 0o644))
	h.kr.m["bam|http://bamboo.lab.example:8085"] = "home-tok"
	svc, key, err := newRuntime(h).resolveBuildArg(context.Background(),
		[]string{"http://bamboo.lab.example:8085/browse/PROJ-PROV12-8/log"}, false, "")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PROV12-8", key)
	assert.Equal(t, "home", svc.Server.Alias, "the link's server, not the default one")
	assert.Equal(t, "home-tok", h.connectOpts[0].Token)

	_, _, err = newRuntime(h).resolveBuildArg(context.Background(),
		[]string{"https://bamboo.other.example/browse/PROJ-PROV12-8"}, false, "")
	assert.Equal(t, errs.KindConfig, errs.KindOf(err), "a link to an unconfigured server")
}
