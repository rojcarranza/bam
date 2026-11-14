package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func (h *harness) tallyFile() string {
	b, err := os.ReadFile(h.env.Paths.Usage)
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(h.t, err)
	return string(b)
}

func TestUsageCountsCommandNamesOnly(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, 0, h.run("version"))
	require.Equal(t, 0, h.run("version"))
	require.Equal(t, 0, h.run("server", "list"))
	// A command with arguments, flags, a plan and a server name still counts as its name.
	require.Equal(t, 0, h.run("--server", "work", "build", "show", "PROJ-PROV12-8", "--json"))

	require.Equal(t, 0, h.run("usage", "--json"))
	var doc struct {
		Counting bool           `json:"counting"`
		Total    int            `json:"total"`
		Commands map[string]int `json:"commands"`
		Days     map[string]map[string]int
	}
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	require.True(t, doc.Counting)
	require.Equal(t, map[string]int{"bam version": 2, "bam server list": 1, "bam build show": 1}, doc.Commands)
	require.Equal(t, 4, doc.Total)
	require.Contains(t, doc.Days, "2026-09-12")

	raw := h.tallyFile()
	for _, secret := range []string{"PROJ", "work", "12-8", "--json", "bamboo.example.com"} {
		require.NotContains(t, raw, secret)
	}
}

func TestUsageSkipsItsOwnAndHousekeepingCommands(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, 0, h.run("usage"))
	require.Equal(t, 0, h.run("completion", "bash"))
	require.Equal(t, 0, h.run("__complete", "ver"))
	require.NotEqual(t, 0, h.run("nosuchcommand", "PROJ-BUILD"))
	require.Empty(t, h.tallyFile())
}

func TestUsageOffStopsCountingAndOnResumes(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, 0, h.run("version"))
	require.Equal(t, 0, h.run("usage", "off"))
	require.Contains(t, h.stdout.String(), "no longer counts")
	require.Equal(t, 0, h.run("version"))
	require.Equal(t, 0, h.run("usage"))
	require.Contains(t, h.stdout.String(), "counting is off")
	require.Contains(t, h.stdout.String(), "bam version  1")

	require.Equal(t, 0, h.run("usage", "on"))
	require.Equal(t, 0, h.run("version"))
	require.Equal(t, 0, h.run("usage"))
	require.Contains(t, h.stdout.String(), "bam version  2")
	require.Contains(t, h.stdout.String(), "counting is on")
}

func TestUsageEmptyAndBadArgument(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, 0, h.run("usage"))
	require.True(t, strings.Contains(h.stdout.String(), "No commands counted yet"))
	require.Equal(t, 2, h.run("usage", "maybe"))
}

func TestUsageUnwritableTallyNeverFailsACommand(t *testing.T) {
	h := newHarness(t)
	// A directory where the file should be makes every write fail.
	require.NoError(t, os.MkdirAll(h.env.Paths.Usage, 0o700))
	require.Equal(t, 0, h.run("version"))
	require.Contains(t, h.stdout.String(), "bam ")
}
