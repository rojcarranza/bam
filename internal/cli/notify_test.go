package cli

import (
	"testing"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #150: --notify rings the bell always and raises a desktop
// notification where the system has one.

func TestNotifyRingsBellAndRaisesDesktopNotificationOnTreeFinish(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	var titles, bodies []string
	h.env.Notify = func(title, body string) error {
		titles, bodies = append(titles, title), append(bodies, body)
		return nil
	}
	assert.Equal(t, 1, h.run("watch", "PROJ-BUILD-482", "--notify"))
	assert.Contains(t, h.stderr.String(), "\a")
	require.Len(t, titles, 1)
	assert.Equal(t, "bam: PROJ-BUILD-482", titles[0])
	assert.Contains(t, bodies[0], "failed")
}

func TestNotifyStillRingsTheBellWithoutADesktopNotifier(t *testing.T) {
	h := treeHarness(t, provider.StateSuccess)
	h.env.Notify = nil
	assert.Equal(t, 0, h.run("watch", "PROJ-BUILD-482", "--notify"))
	assert.Contains(t, h.stderr.String(), "\a")
}

func TestNotifyKeepsJSONStdoutClean(t *testing.T) {
	h := newHarness(t)
	h.fake.Sequences = map[string][]provider.Build{"PROJ-BUILD-483": {
		{Key: "PROJ-BUILD-483", State: provider.StateSuccess},
	}}
	called := 0
	h.env.Notify = func(string, string) error { called++; return nil }
	assert.Equal(t, 0, h.run("watch", "PROJ-BUILD-483", "--json", "--notify"))
	assert.NotContains(t, h.stdout.String(), "\a")
	assert.Equal(t, 1, called)
}

func TestWithoutNotifyNothingRings(t *testing.T) {
	h := treeHarness(t, provider.StateSuccess)
	h.env.Notify = func(string, string) error { t.Fatal("notified without --notify"); return nil }
	assert.Equal(t, 0, h.run("watch", "PROJ-BUILD-482"))
	assert.NotContains(t, h.stderr.String(), "\a")
}
