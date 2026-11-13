package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (pin by name): bam pin add takes several pins, a plan name among
// them, and writes all of them or none, keeping the settings file's comments.

func nameHarness(t *testing.T) *harness {
	t.Helper()
	h := topHarness(t)
	h.fake.Plans["PROJ"] = append(h.fake.Plans["PROJ"],
		provider.Plan{Key: "PROJ-INTEG", Name: "Integrate for next release", ProjectKey: "PROJ"},
		provider.Plan{Key: "PROJ-TESTS", Name: "Run feature tests", ProjectKey: "PROJ"},
		provider.Plan{Key: "PROJ-LOAD", Name: "Run load tests", ProjectKey: "PROJ"})
	require.NoError(t, os.MkdirAll(strings.TrimSuffix(h.env.Paths.MachineConfig, "/config.yaml"), 0o755))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig, []byte("# mine\nversion: 1\n"), 0o644))
	return h
}

func machineFile(t *testing.T, h *harness) string {
	t.Helper()
	data, err := os.ReadFile(h.env.Paths.MachineConfig)
	require.NoError(t, err)
	return string(data)
}

func TestPinAddByExactNameStoresTheKeyAndPrintsBoth(t *testing.T) {
	h := nameHarness(t)
	require.Equal(t, 0, h.run("pin", "add", "integration", "integrate FOR next release"), h.stderr.String())
	assert.Equal(t, "Pinned PROJ-INTEG (Integrate for next release) in integration\n", h.stdout.String())
	data := machineFile(t, h)
	assert.Contains(t, data, "PROJ-INTEG")
	assert.NotContains(t, data, "Integrate", "the plan key is stored, not the name")
	assert.True(t, strings.HasPrefix(data, "# mine"), "comments survive")
}

func TestPinAddByPartOfAName(t *testing.T) {
	h := nameHarness(t)
	require.Equal(t, 0, h.run("pin", "add", "integration", "feature"), h.stderr.String())
	assert.Equal(t, "Pinned PROJ-TESTS (Run feature tests) in integration\n", h.stdout.String())
}

func TestPinAddNameThatFitsSeveralPinsNothing(t *testing.T) {
	h := nameHarness(t)
	assert.Equal(t, 2, h.run("pin", "add", "integration", "run"))
	assert.Contains(t, h.stderr.String(), "PROJ-TESTS  Run feature tests")
	assert.Contains(t, h.stderr.String(), "PROJ-LOAD  Run load tests")
	assert.NotContains(t, machineFile(t, h), "PROJ-", "nothing is written")
}

func TestPinAddNameThatFitsNothingPointsToPlanList(t *testing.T) {
	h := nameHarness(t)
	assert.Equal(t, 2, h.run("pin", "add", "integration", "no such pipeline"))
	assert.Contains(t, h.stderr.String(), "bam plan list")
	assert.NotContains(t, machineFile(t, h), "integration")
}

func TestPinAddTakesSeveralPinsInOneCommand(t *testing.T) {
	h := nameHarness(t)
	require.Equal(t, 0, h.run("pin", "add", "integration", "Integrate for next release", "PROJ-BUILD", "provision-lab", "load"), h.stderr.String())
	assert.Equal(t, "Pinned PROJ-INTEG (Integrate for next release) in integration\n"+
		"Pinned PROJ-BUILD in integration\nPinned provision-lab in integration\nPinned PROJ-LOAD (Run load tests) in integration\n", h.stdout.String())
	require.Equal(t, 0, h.run("pin", "list", "--json"))
	assert.JSONEq(t, `[{"group":"integration","pins":["PROJ-INTEG","PROJ-BUILD","provision-lab","PROJ-LOAD"],
		"names":{"PROJ-INTEG":"Integrate for next release","PROJ-BUILD":"Build and test","PROJ-LOAD":"Run load tests"}}]`, h.stdout.String())
}

func TestPinAddOneBadPinAmongThreeWritesNone(t *testing.T) {
	h := nameHarness(t)
	assert.Equal(t, 2, h.run("pin", "add", "integration", "PROJ-BUILD", "no such pipeline", "load"))
	assert.Equal(t, "# mine\nversion: 1\n", machineFile(t, h))
}

func TestPinAddTheSamePlanTwiceInOneCommandPinsItOnce(t *testing.T) {
	h := nameHarness(t)
	require.Equal(t, 0, h.run("pin", "add", "integration", "PROJ-INTEG", "Integrate for next release"))
	assert.Equal(t, 1, strings.Count(machineFile(t, h), "PROJ-INTEG"))
}

func TestPinAddWithoutPlanSearchReadsTheProjectsPlans(t *testing.T) {
	h := nameHarness(t)
	h.fake.NoPlanSearch = true
	require.Equal(t, 0, h.run("pin", "add", "integration", "load"), h.stderr.String())
	assert.Equal(t, "Pinned PROJ-LOAD (Run load tests) in integration\n", h.stdout.String())
}

func TestPinAddKeyAndPresetNeedNoServer(t *testing.T) {
	h := nameHarness(t)
	h.fake.NoPlanSearch = true
	require.Equal(t, 0, h.run("pin", "add", "release", "PROJ-BUILD@develop", "provision-lab"))
	assert.Empty(t, h.fake.SearchCalls, "a key or a preset is not looked up")
}
