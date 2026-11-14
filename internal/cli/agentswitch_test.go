package cli

import (
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func switchHarness(t *testing.T) *harness {
	h := topHarness(t)
	h.fake.AgentList = []provider.Agent{
		{ID: 1, Name: "linux-1", Type: "REMOTE", Online: true, Enabled: true, Busy: true},
		{ID: 2, Name: "linux-2", Type: "REMOTE", Online: true, Enabled: false},
	}
	return h
}

// Intent (issue 217, BAM-34 P6): bam agent disable and enable ask first on a
// terminal and need --yes without one.
func TestAgentDisableAsksOnATerminal(t *testing.T) {
	h := switchHarness(t)
	h.tty, h.stdin = true, "y\n"
	require.Equal(t, 0, h.run("agent", "disable", "linux-1"))
	assert.Contains(t, h.stderr.String(), "Disable linux-1? It is building now and finishes that build first. [y/N]")
	assert.Contains(t, h.stdout.String(), "linux-1 is now disabled")
	assert.Equal(t, []string{"disable linux-1"}, h.fake.AgentSwitched)
}

func TestAgentDisableAnsweredNoChangesNothing(t *testing.T) {
	for _, in := range []string{"n\n", ""} {
		h := switchHarness(t)
		h.tty, h.stdin = true, in
		require.Equal(t, 0, h.run("agent", "disable", "linux-1"))
		assert.Contains(t, h.stderr.String(), "nothing changed")
		assert.Empty(t, h.fake.AgentSwitched)
	}
}

func TestAgentSwitchNeedsYesWithoutATerminal(t *testing.T) {
	h := switchHarness(t)
	assert.Equal(t, 2, h.run("agent", "enable", "linux-2"))
	assert.Contains(t, h.stderr.String(), "needs --yes")
	h.tty = true
	assert.Equal(t, 2, h.run("agent", "enable", "linux-2", "--json"))
	assert.Empty(t, h.fake.AgentSwitched)
}

func TestAgentEnableWithYesJSON(t *testing.T) {
	h := switchHarness(t)
	require.Equal(t, 0, h.run("agent", "enable", "linux-2", "--yes", "--json"))
	assert.JSONEq(t, `{"id":2,"name":"linux-2","enabled":true,"changed":true}`, h.stdout.String())
	assert.Equal(t, []string{"enable linux-2"}, h.fake.AgentSwitched)
}

func TestAgentSwitchAlreadyInStateAsksNothing(t *testing.T) {
	h := switchHarness(t)
	require.Equal(t, 0, h.run("agent", "enable", "linux-1"))
	assert.Contains(t, h.stdout.String(), "linux-1 is already enabled")
	assert.Empty(t, h.fake.AgentSwitched)
}

func TestAgentSwitchUnknownNameAndRefusal(t *testing.T) {
	h := switchHarness(t)
	assert.Equal(t, 2, h.run("agent", "disable", "linux", "--yes"))
	assert.Contains(t, h.stderr.String(), "no agent is named")
	h.fake.SetAgentErr = errs.Authf("403 from bamboo.example.com: only an administrator can disable an agent")
	assert.Equal(t, 4, h.run("agent", "disable", "linux-1", "--yes"))
	assert.Contains(t, h.stderr.String(), "administrator")
}
