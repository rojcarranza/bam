package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-34 P1): monitor: holds named groups of pins, in the machine
// file or .bam.yaml; a bad group, a bad pin, an unknown key or a duplicate
// pin is a configuration error.
func TestMonitorMergesProjectAndMachineGroups(t *testing.T) {
	cfg, err := loadProject(t,
		"version: 1\nmonitor:\n  integration: [PROJ-BUILD, PROJ-PROV@release/1.2]\n",
		"version: 1\nmonitor:\n  integration: [PROJ-BUILD, OPS-DEPLOY]\n  release: [PROJ-REL]\n")
	require.NoError(t, err)
	got, err := cfg.Monitor()
	require.NoError(t, err)
	assert.Equal(t, []PinGroup{
		{Name: "integration", Pins: []string{"PROJ-BUILD", "PROJ-PROV@release/1.2", "OPS-DEPLOY"}},
		{Name: "release", Pins: []string{"PROJ-REL"}},
	}, got)
}

func TestMonitorAcceptsPresetPins(t *testing.T) {
	cfg, err := loadProject(t, "version: 1\n", "version: 1\ntargets:\n  provision:\n    plan: PROJ-PROV\nmonitor:\n  integration: [provision]\n")
	require.NoError(t, err)
	got, err := cfg.Monitor()
	require.NoError(t, err)
	assert.Equal(t, []PinGroup{{Name: "integration", Pins: []string{"provision"}}}, got)
}

func TestMonitorLeavesAnUnknownPresetToTheBoard(t *testing.T) {
	cfg, err := loadProject(t, "version: 1\n", "version: 1\nmonitor:\n  release: [deploy-qa]\n")
	require.NoError(t, err)
	got, err := cfg.Monitor()
	require.NoError(t, err, "a preset defined in another repository must not break the board here")
	assert.Equal(t, []PinGroup{{Name: "release", Pins: []string{"deploy-qa"}}}, got)

	err = cfg.CheckPin("deploy-qa")
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), "deploy-qa is not a preset")
}

func TestMonitorWithoutPinsIsEmpty(t *testing.T) {
	cfg, err := loadProject(t, "version: 1\n", "version: 1\n")
	require.NoError(t, err)
	got, err := cfg.Monitor()
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestMonitorValidation(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{
		{"monitor:\n  Integration: [PROJ-BUILD]\n", `group "Integration" is not a valid name`},
		{"monitor:\n  integration: []\n", `group "integration" has no pins`},
		{"monitor:\n  integration: [PROJ-BUILD, PROJ-BUILD]\n", "pins PROJ-BUILD twice"},
		{"monitor:\n  a: [PROJ-BUILD]\n  b: [PROJ-BUILD]\n", `PROJ-BUILD is pinned in "a" and in "b"`},
		{"monitor:\n  a: [PROJ-BUILD@]\n", "names no branch"},
		{"monitor:\n  a: [PROJ-BUILD-45]\n", "is not a plan key"},
	} {
		cfg, err := loadProject(t, "version: 1\n"+tc.yaml, "")
		require.NoError(t, err, tc.yaml)
		_, err = cfg.Monitor()
		require.Error(t, err, tc.yaml)
		assert.Equal(t, errs.KindConfig, errs.KindOf(err), tc.yaml)
		assert.Contains(t, err.Error(), tc.want, tc.yaml)
	}
}

func TestMonitorRefusesWrongShapeAndUnknownKey(t *testing.T) {
	_, err := loadProject(t, "version: 1\nmonitor:\n  a: PROJ-BUILD\n", "")
	require.Error(t, err, "a group must be a list")
	_, err = loadProject(t, "version: 1\nmonitr:\n  a: [PROJ-BUILD]\n", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "monitr")
}

func TestAddAndRemovePinKeepComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "# my machine\nversion: 1\nmonitor:\n  # the nightly ones\n  integration:\n    - PROJ-BUILD # main build\n")

	require.NoError(t, AddPin(path, "integration", "PROJ-PROV"))
	require.NoError(t, AddPin(path, "integration", "PROJ-PROV"), "adding twice changes nothing")
	require.NoError(t, AddPin(path, "release", "OPS-DEPLOY@main"))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(data)
	for _, keep := range []string{"# my machine", "# the nightly ones", "# main build"} {
		assert.Contains(t, text, keep)
	}
	assert.Equal(t, 1, strings.Count(text, "PROJ-PROV"))
	assert.Contains(t, text, "OPS-DEPLOY@main")

	ok, err := RemovePin(path, "PROJ-PROV")
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = RemovePin(path, "OPS-DEPLOY@main")
	require.NoError(t, err)
	assert.True(t, ok)
	data, _ = os.ReadFile(path)
	assert.NotContains(t, string(data), "release", "an emptied group goes")
	assert.Contains(t, string(data), "# my machine")
	assert.Contains(t, string(data), "PROJ-BUILD")

	ok, err = RemovePin(path, "NOPE-PLAN")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestAddPinCreatesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	require.NoError(t, AddPin(path, "integration", "PROJ-BUILD"))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "version: 1")
	assert.Contains(t, string(data), "PROJ-BUILD")
}

func TestAgentStartsEditKeepsTheFilesComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\n# my servers\nservers:\n  work:\n    url: https://bamboo.example.com\n"), 0o600))

	added, err := AddAgentStart(path, "team-a-agent")
	require.NoError(t, err)
	assert.True(t, added)
	added, err = AddAgentStart(path, "team-a-agent")
	require.NoError(t, err)
	assert.False(t, added, "adding twice changes nothing")
	_, err = AddAgentStart(path, "team-b-agent")
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "# my servers")
	assert.Contains(t, string(data), "- team-a-agent")
	assert.Contains(t, string(data), "- team-b-agent")

	removed, err := RemoveAgentStart(path, "nope")
	require.NoError(t, err)
	assert.False(t, removed)
	for _, s := range []string{"team-a-agent", "team-b-agent"} {
		removed, err = RemoveAgentStart(path, s)
		require.NoError(t, err)
		assert.True(t, removed)
	}
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "agents")
	assert.Contains(t, string(data), "# my servers")
}
