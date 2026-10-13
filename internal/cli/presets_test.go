package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTargetCommandIsGone(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("target", "list"), "bam target was renamed to bam preset")
}

func TestTargetListAndShow(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("preset", "list"))
	assert.Regexp(t, `build\s+PROJ-BUILD`, h.stdout.String())
	assert.Regexp(t, `provision-lab\s+PROJ-PROV\s+develop`, h.stdout.String())

	assert.Equal(t, 0, h.run("preset", "list", "--json"))
	var docs []map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &docs))
	assert.Len(t, docs, 2)

	assert.Equal(t, 0, h.run("preset", "show", "provision-lab"))
	assert.Regexp(t, `Required\s+cluster_name`, h.stdout.String())
	assert.Regexp(t, `cluster_type\s+k8s, dcos`, h.stdout.String())
	assert.Equal(t, 2, h.run("preset", "show", "nope"))
}

func TestTargetAddWritesProjectFile(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("preset", "add", "prov2", "--plan", "PROJ-PROV", "--branch", "develop"))
	assert.Contains(t, h.stdout.String(), "added preset prov2 to "+filepath.Join(h.root, ".bam.yaml"))
	assert.Contains(t, h.stderr.String(), "review before commit: values copied from Bamboo")
	data, err := os.ReadFile(filepath.Join(h.root, ".bam.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "prov2:")
	assert.Contains(t, string(data), `no plan default; last used "beta" in PROJ-PROV12-8`)

	assert.Equal(t, 0, h.run("preset", "show", "prov2"), "the written file loads")
	assert.Equal(t, 2, h.run("preset", "add", "build", "--plan", "PROJ-BUILD"), "exists without --force")
}

func TestTargetAddPrintWritesNothing(t *testing.T) {
	h := newHarness(t)
	before, _ := os.ReadFile(filepath.Join(h.root, ".bam.yaml"))
	assert.Equal(t, 0, h.run("preset", "add", "x", "--plan", "PROJ-PROV", "--print"))
	assert.True(t, strings.HasPrefix(h.stdout.String(), "targets:\n  x:\n    plan: PROJ-PROV\n"), h.stdout.String())
	after, _ := os.ReadFile(filepath.Join(h.root, ".bam.yaml"))
	assert.Equal(t, before, after)
}

func TestTargetAddMachine(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("preset", "add", "mine", "--plan", "PROJ-BUILD", "--machine"))
	data, err := os.ReadFile(h.env.Paths.MachineConfig)
	require.NoError(t, err)
	assert.Contains(t, string(data), h.root+":")
	assert.Contains(t, string(data), "mine:")
	assert.Equal(t, 0, h.run("preset", "show", "mine"))
}

func TestTargetAddNeedsPlan(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("preset", "add", "x"))
}

func TestInit(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("init", "--server", "work", "--project", "PROJ"), ".bam.yaml exists")
	assert.Equal(t, 2, h.run("init", "--server", "work", "--force"), "--project is required")

	require.NoError(t, os.Remove(filepath.Join(h.root, ".bam.yaml")))
	assert.Equal(t, 0, h.run("init", "--server", "work", "--url", "https://bamboo.example.com", "--project", "PROJ", "--project", "OPS"))
	assert.Empty(t, h.connectOpts, "init without --plan makes no network call")
	data, _ := os.ReadFile(filepath.Join(h.root, ".bam.yaml"))
	assert.Contains(t, string(data), "projects: [PROJ, OPS]")

	assert.Equal(t, 0, h.run("init", "--server", "work", "--url", "https://bamboo.example.com", "--project", "PROJ",
		"--plan", "PROJ-PROV=lab", "--plan", "PROJ-BUILD", "--force"))
	data, _ = os.ReadFile(filepath.Join(h.root, ".bam.yaml"))
	assert.Contains(t, string(data), "  lab:\n    plan: PROJ-PROV\n")
	assert.Contains(t, string(data), "  build:\n    plan: PROJ-BUILD\n")
	assert.NotEmpty(t, h.connectOpts)
}

func TestInitWithDifferentURLDropsMachineAuthEnv(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(h.env.Paths.MachineConfig), 0o755))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig,
		[]byte("version: 1\nservers:\n  work:\n    url: https://bamboo.example.com\n    auth_env: BAM_WORK_TOKEN\n"), 0o644))
	h.vars["BAM_WORK_TOKEN"] = "work-secret"
	h.kr.m["bam|https://other.example.com"] = "other-token"

	assert.Equal(t, 0, h.run("init", "--server", "work", "--url", "https://other.example.com",
		"--project", "PROJ", "--plan", "PROJ-BUILD", "--force"), h.stderr.String())
	require.NotEmpty(t, h.connectOpts)
	assert.Equal(t, "other-token", h.connectOpts[len(h.connectOpts)-1].Token,
		"must not use BAM_WORK_TOKEN, which was learned for a different origin")
}

func TestDefaultTargetName(t *testing.T) {
	assert.Equal(t, "prov", defaultTargetName("PROJ-PROV"))
	assert.Equal(t, "build2", defaultTargetName("PROJ-BUILD2"))
}

func TestTargetSyncAddsThenUpToDate(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(h.root, ".bam.yaml")
	assert.Equal(t, 0, h.run("preset", "sync", "provision-lab"))
	assert.Contains(t, h.stdout.String(), "provision-lab  PROJ-PROV  .bam.yaml\n")
	assert.Regexp(t, `\+\s+cluster_name\s+""\s+no plan default; last used "beta" in PROJ-PROV12-8`, h.stdout.String())
	assert.Contains(t, h.stderr.String(), "review before commit: values copied from Bamboo")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "      cluster_type: k8s\n      cluster_name: \"\" # no plan default; last used \"beta\" in PROJ-PROV12-8\n")
	assert.Contains(t, string(data), "    required: [cluster_name]\n", "required is untouched")

	assert.Equal(t, 0, h.run("preset", "sync", "provision-lab"))
	assert.Contains(t, h.stdout.String(), "provision-lab  PROJ-PROV  .bam.yaml  up to date\n")
	assert.NotContains(t, h.stderr.String(), "review before commit")
	again, _ := os.ReadFile(path)
	assert.Equal(t, data, again)
}

func TestTargetSyncMarksAndUnmarksStale(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(h.root, ".bam.yaml")
	h.fake.Variables["PROJ-PROV"] = []provider.Variable{{Name: "cluster_name", Value: ""}}
	assert.Equal(t, 0, h.run("preset", "sync", "provision-lab"))
	assert.Regexp(t, `!\s+cluster_type\s+not declared on PROJ-PROV \(kept\)`, h.stdout.String())
	data, _ := os.ReadFile(path)
	assert.Contains(t, string(data), "cluster_type: k8s # not declared on PROJ-PROV\n")

	h.fake.Variables["PROJ-PROV"] = []provider.Variable{{Name: "cluster_name", Value: ""}, {Name: "cluster_type", Value: "k8s"}}
	assert.Equal(t, 0, h.run("preset", "sync", "provision-lab"))
	assert.Regexp(t, `~\s+cluster_type\s+declared again on PROJ-PROV`, h.stdout.String())
	data, _ = os.ReadFile(path)
	assert.Contains(t, string(data), "cluster_type: k8s\n")
}

func TestTargetSyncDryRunWritesNothing(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(h.root, ".bam.yaml")
	before, _ := os.ReadFile(path)
	assert.Equal(t, 0, h.run("preset", "sync", "--all", "--dry-run"))
	assert.Contains(t, h.stdout.String(), "would sync: provision-lab  PROJ-PROV  .bam.yaml\n")
	assert.Contains(t, h.stdout.String(), "build  PROJ-BUILD  .bam.yaml  up to date\n")
	assert.Contains(t, h.stdout.String(), "dry run: no files written\n")
	after, _ := os.ReadFile(path)
	assert.Equal(t, before, after)
}

func TestTargetSyncAllContinuesPastFailure(t *testing.T) {
	h := newHarness(t)
	h.fake.Variables["PROJ-BUILD"] = nil
	h.fake.Plans["PROJ"] = h.fake.Plans["PROJ"][1:] // PROJ-BUILD no longer exists
	assert.Equal(t, 5, h.run("preset", "sync", "--all", "--json"))
	var docs []map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &docs))
	require.Len(t, docs, 2)
	assert.Equal(t, "build", docs[0]["name"])
	assert.Equal(t, "error", docs[0]["status"])
	assert.NotEmpty(t, docs[0]["error"])
	assert.Equal(t, "provision-lab", docs[1]["name"])
	assert.Equal(t, "synced", docs[1]["status"])
	assert.Equal(t, filepath.Join(h.root, ".bam.yaml"), docs[1]["file"])
	assert.Equal(t, []any{}, docs[1]["stale"])
}

func TestTargetSyncUnsupportedPlanVariables(t *testing.T) {
	h := newHarness(t)
	h.fake.VariablesErr = errs.Bamboof("no").Wrap(errs.ErrUnsupported)
	assert.Equal(t, 5, h.run("preset", "sync", "provision-lab"))
	assert.Contains(t, h.stdout.String(), "provision-lab  PROJ-PROV  error: cannot read plan variables of PROJ-PROV on this server")
}

func TestTargetSyncArgs(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("preset", "sync"))
	assert.Equal(t, 2, h.run("preset", "sync", "build", "--all"))
	assert.Equal(t, 2, h.run("preset", "sync", "nope"))
}
