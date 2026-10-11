package bamboo

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// knownCaps installs fresh capabilities so no version check happens, and
// records every save.
func knownCaps(c *Client, caps Capabilities) *[]Capabilities {
	var saved []Capabilities
	if caps.Version == "" {
		caps.Version = "9.6.2"
	}
	caps.CheckedAt = c.now()
	c.caps = caps
	c.versionChecked = true
	c.saveCaps = func(cp Capabilities) { saved = append(saved, cp) }
	return &saved
}

func TestCapabilitiesFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bam", "capabilities.json")
	a := Capabilities{Version: "9.6.2", PlanVars: "variables", Log: "entries", CheckedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	b := Capabilities{Version: "10.2.0", PlanVars: "none"}
	require.NoError(t, SaveCapabilities(path, "https://bamboo.example.com", a))
	require.NoError(t, SaveCapabilities(path, "http://bamboo.lab.example:8085", b))

	got, err := LoadCapabilities(path, "https://bamboo.example.com")
	require.NoError(t, err)
	assert.Equal(t, a, got)
	got, _ = LoadCapabilities(path, "http://bamboo.lab.example:8085")
	assert.Equal(t, "none", got.PlanVars)
	missing, err := LoadCapabilities(filepath.Join(t.TempDir(), "none.json"), "x")
	require.NoError(t, err)
	assert.Equal(t, Capabilities{}, missing)
}

func TestVersionChangeDiscardsCapabilities(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/info":                      {fixture: "info.json"}, // 9.6.2
		"GET /rest/api/latest/plan/PROJ-BUILD/variables": {fixture: "plan_variables.json"},
	})
	c.caps = Capabilities{Version: "9.5.0", CheckedAt: c.now(), PlanVars: "none"}
	vars, err := c.ListVariables(ctx, "PROJ-BUILD")
	require.NoError(t, err, "stale 'none' must be forgotten after an upgrade")
	assert.Len(t, vars, 3)
	assert.Equal(t, "9.6.2", c.Capabilities().Version)
	assert.Equal(t, "variables", c.Capabilities().PlanVars)
}

func TestOldCapabilitiesDiscarded(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/info":                      {fixture: "info.json"},
		"GET /rest/api/latest/plan/PROJ-BUILD/variables": {fixture: "plan_variables.json"},
	})
	c.caps = Capabilities{Version: "9.6.2", CheckedAt: c.now().Add(-8 * 24 * time.Hour), PlanVars: "none"}
	_, err := c.ListVariables(ctx, "PROJ-BUILD")
	require.NoError(t, err)
	assert.NotEmpty(t, rec.all())
}

func TestAnOldCachedFailedTestsNoIsForgotten(t *testing.T) {
	// failed_tests: no was learned from build results, which carry no tests
	// on Bamboo 12; it must not keep failed tests off for a week.
	path := filepath.Join(t.TempDir(), "capabilities.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"https://bamboo.example.com":{"version":"12.1.8","failed_tests":"no","log":"entries"}}`), 0o600))
	caps, err := LoadCapabilities(path, "https://bamboo.example.com")
	require.NoError(t, err)
	assert.Equal(t, "", caps.FailedTestsFrom)
	assert.Equal(t, "entries", caps.Log, "the rest of the cache is kept")
}
