package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-11 P16): bam status lists what you started across servers with
// live state and link, the checked-out branch's build first; --short is one
// line for a prompt.
func statusHarness(t *testing.T) *harness {
	h := newHarness(t)
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte(insertServer(projectYAML)), 0o644))
	h.vars["BAM_TOKEN"] = ""
	h.kr.m["bam|https://bamboo.lab.example"] = "tok2"
	running := provider.Build{Key: "PROJ-BUILD-483", URL: workOrigin + "/browse/PROJ-BUILD-483", PlanKey: "PROJ-BUILD", State: provider.StateRunning}
	failed := provider.Build{Key: "LAB-DEP-7", URL: workOrigin + "/browse/LAB-DEP-7", PlanKey: "LAB-DEP", State: provider.StateFailed}
	done := provider.Build{Key: "PROJ-BUILD-482", URL: workOrigin + "/browse/PROJ-BUILD-482", PlanKey: "PROJ-BUILD", State: provider.StateSuccess}
	h.fake.Sequences = map[string][]provider.Build{"PROJ-BUILD-483": {running}, "LAB-DEP-7": {failed}, "PROJ-BUILD-482": {done}}
	h.fake.Progressions = map[string][]provider.Progress{"PROJ-BUILD-483": {{Valid: true, Percent: 0.55, Average: time.Minute}}}
	store := &app.StateStore{Path: h.env.Paths.State}
	now := fixedNow
	for _, r := range []app.JournalRun{
		{BuildKey: "PROJ-BUILD-482", Origin: workOrigin, Server: "work", PlanKey: "PROJ-BUILD", Target: "build", RepoRoot: "/elsewhere", StartedAt: now.Add(-2 * time.Hour)},
		{BuildKey: "LAB-DEP-7", Origin: "https://bamboo.lab.example", Server: "lab", PlanKey: "LAB-DEP", RepoRoot: "/elsewhere", StartedAt: now.Add(-time.Hour)},
		{BuildKey: "PROJ-BUILD-483", Origin: workOrigin, Server: "work", PlanKey: "PROJ-BUILD", Target: "build", RepoRoot: h.root, StartedAt: now.Add(-3 * time.Hour)},
		{BuildKey: "PROJ-OLD-1", Origin: workOrigin, Server: "work", PlanKey: "PROJ-OLD", StartedAt: now.Add(-48 * time.Hour)},
	} {
		require.NoError(t, store.Record(r.RepoRoot, app.LastRecord{BuildKey: r.BuildKey}, r))
	}
	h.env.GitBranch = func(string) (string, error) { return "main", nil }
	return h
}

func insertServer(y string) string {
	return replaceOnce(y, "default_server: work\n", "  lab:\n    url: https://bamboo.lab.example\ndefault_server: work\n")
}

func replaceOnce(s, old, new string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}

func TestStatusListsRunsAcrossServersCheckedOutFirst(t *testing.T) {
	h := statusHarness(t)
	require.Equal(t, 0, h.run("status"), h.stderr.String())
	out := h.stdout.String()
	// The checkout's build comes first although it is the oldest; the day-old
	// run is left out; both servers are asked.
	assert.Regexp(t, `(?s)→\s+PROJ-BUILD-483.*LAB-DEP-7.*PROJ-BUILD-482`, out)
	assert.NotContains(t, out, "PROJ-OLD-1")
	assert.Contains(t, out, "55%")
	assert.Len(t, h.connectOpts, 2, "one connection for each server")
	assert.Contains(t, out, workOrigin+"/browse/PROJ-BUILD-483")
}

func TestStatusShortIsOneLineWithoutColour(t *testing.T) {
	h := statusHarness(t)
	h.tty = true
	require.Equal(t, 0, h.run("status", "--short"), h.stderr.String())
	assert.Equal(t, "PROJ-BUILD-483 running 55%\n", h.stdout.String())
	h.fake.Sequences["PROJ-BUILD-483"] = []provider.Build{{Key: "PROJ-BUILD-483", State: provider.StateFailed}}
	require.Equal(t, 0, h.run("status", "--short"))
	assert.Equal(t, "PROJ-BUILD-483 failed\n", h.stdout.String())
}

func TestStatusShortIsEmptyWhenNothingWasStarted(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, 0, h.run("status", "--short"))
	assert.Empty(t, h.stdout.String())
	require.Equal(t, 0, h.run("status"))
	assert.Contains(t, h.stdout.String(), "No builds started with bam in the last day")
	require.Equal(t, 0, h.run("status", "--json"))
	assert.JSONEq(t, `{"builds":[]}`, h.stdout.String())
}

func TestStatusJSON(t *testing.T) {
	h := statusHarness(t)
	require.Equal(t, 0, h.run("status", "--json"), h.stderr.String())
	var doc struct {
		Builds []map[string]any `json:"builds"`
	}
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	require.Len(t, doc.Builds, 3)
	assert.Equal(t, "PROJ-BUILD-483", doc.Builds[0]["key"])
	assert.Equal(t, true, doc.Builds[0]["here"])
	assert.Equal(t, "running", doc.Builds[0]["state"])
	assert.Equal(t, "build", doc.Builds[0]["preset"])
	assert.Equal(t, "lab", doc.Builds[1]["server"])
}

func TestStatusKeepsGoingWhenOneBuildIsGone(t *testing.T) {
	h := statusHarness(t)
	delete(h.fake.Sequences, "LAB-DEP-7")
	require.Equal(t, 0, h.run("status"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "LAB-DEP-7")
	assert.Contains(t, h.stdout.String(), "? unknown")
	assert.Contains(t, h.stdout.String(), "PROJ-BUILD-482")
}

func TestRunAddsToTheJournal(t *testing.T) {
	h := newHarness(t)
	h.fake.TriggerResult = queuedBuild("PROJ-BUILD-483")
	require.Equal(t, 0, h.run("run", "build"), h.stderr.String())
	runs, err := (&app.StateStore{Path: h.env.Paths.State}).Runs()
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "PROJ-BUILD-483", runs[0].BuildKey)
	assert.Equal(t, "build", runs[0].Target)
	assert.Equal(t, "work", runs[0].Server)
}
