package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const syncYAML = `# team presets
version: 1
targets:
  lab:
    plan: PROJ-LAB
    defaults:
      keep: "1" # plan default
      old: x # set by hand
      gone: "2"
      back: y # set by hand; not declared on PROJ-LAB
    required: [keep]
`

func TestSyncTargetEditsInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".bam.yaml")
	writeFile(t, path, syncYAML)
	require.NoError(t, os.Chmod(path, 0o600))

	ch, err := SyncTarget(path, []string{"targets"}, "lab", TargetEdit{
		Add: []DraftVar{
			{Name: "region", Value: "eu-west-1", Comment: "plan default"},
			{Name: "api_token", Value: "${API_TOKEN}", Comment: "masked by Bamboo; set env var API_TOKEN"},
		},
		Mark:   []string{"old", "gone"},
		Unmark: []string{"back", "keep"},
		Plan:   "PROJ-LAB",
	}, true)
	require.NoError(t, err)
	assert.Equal(t, TargetChange{Added: []string{"region", "api_token"}, Marked: []string{"old", "gone"}, Unmarked: []string{"back"}}, ch)

	data, _ := os.ReadFile(path)
	assert.Equal(t, `# team presets
version: 1
targets:
  lab:
    plan: PROJ-LAB
    defaults:
      keep: "1" # plan default
      old: x # set by hand; not declared on PROJ-LAB
      gone: "2" # not declared on PROJ-LAB
      back: y # set by hand
      region: "eu-west-1" # plan default
      api_token: "${API_TOKEN}" # masked by Bamboo; set env var API_TOKEN
    required: [keep]
`, string(data))
	st, _ := os.Stat(path)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())

	again, err := SyncTarget(path, []string{"targets"}, "lab", TargetEdit{Mark: []string{"old", "gone"}, Unmark: []string{"back"}, Plan: "PROJ-LAB"}, true)
	require.NoError(t, err)
	assert.True(t, again.Empty(), "marking twice changes nothing")
}

func TestSyncTargetDryRunAndNoOpWriteNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".bam.yaml")
	writeFile(t, path, syncYAML)
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))

	ch, err := SyncTarget(path, []string{"targets"}, "lab", TargetEdit{Add: []DraftVar{{Name: "region", Value: "a"}}, Plan: "PROJ-LAB"}, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"region"}, ch.Added)

	ch, err = SyncTarget(path, []string{"targets"}, "lab", TargetEdit{Unmark: []string{"keep"}, Plan: "PROJ-LAB"}, true)
	require.NoError(t, err)
	assert.True(t, ch.Empty())

	data, _ := os.ReadFile(path)
	assert.Equal(t, syncYAML, string(data))
	st, _ := os.Stat(path)
	assert.True(t, st.ModTime().Equal(old), "no-op sync does not rewrite the file")
}

func TestSyncTargetCreatesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "version: 1\nrepos:\n  ~/src/repo:\n    targets:\n      mine:\n        plan: PROJ-MINE\n")
	_, err := SyncTarget(path, []string{"repos", "~/src/repo", "targets"}, "mine",
		TargetEdit{Add: []DraftVar{{Name: "a", Value: "", Comment: "no plan default"}}, Plan: "PROJ-MINE"}, true)
	require.NoError(t, err)
	data, _ := os.ReadFile(path)
	assert.Contains(t, string(data), "        defaults:\n          a: \"\" # no plan default\n")
}

func TestSyncTargetFillsEmptyDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".bam.yaml")
	writeFile(t, path, "version: 1\ntargets:\n  lab:\n    plan: PROJ-LAB\n    defaults:\n")
	_, err := SyncTarget(path, []string{"targets"}, "lab", TargetEdit{Add: []DraftVar{{Name: "a", Value: "1"}}, Plan: "PROJ-LAB"}, true)
	require.NoError(t, err)
	data, _ := os.ReadFile(path)
	assert.Contains(t, string(data), "    defaults:\n      a: \"1\"\n")
}

func TestSyncTargetAddsToEmptyFlowMapping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.yaml")
	writeFile(t, path, "version: 1\npresets:\n  lab:\n    plan: PROJ-LAB\n    variables: {}\n")
	_, err := SyncTarget(path, []string{"presets"}, "lab",
		TargetEdit{Add: []DraftVar{{Name: "a", Value: "", Comment: "no plan default"}}, Plan: "PROJ-LAB"}, true)
	require.NoError(t, err)
	data, _ := os.ReadFile(path)
	assert.Contains(t, string(data), "    variables:\n      a: \"\" # no plan default\n")
}

func TestSyncTargetMissingEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".bam.yaml")
	writeFile(t, path, syncYAML)
	_, err := SyncTarget(path, []string{"targets"}, "nope", TargetEdit{Mark: []string{"a"}, Plan: "PROJ-LAB"}, true)
	require.Error(t, err)
}
