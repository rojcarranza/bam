package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
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
	assert.Equal(t, 0, h.run("preset", "add", "prov2", "--plan", "PROJ-PROV", "--branch", "develop", "--shared"))
	assert.Contains(t, h.stdout.String(), "added preset prov2 to "+filepath.Join(h.root, ".bam.yaml"))
	assert.Contains(t, h.stderr.String(), "review before commit: values copied from Bamboo")
	data, err := os.ReadFile(filepath.Join(h.root, ".bam.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "prov2:")
	assert.Contains(t, string(data), `no plan default; last used "beta" in PROJ-PROV12-8`)

	assert.Equal(t, 0, h.run("preset", "show", "prov2"), "the written file loads")
	assert.Equal(t, 2, h.run("preset", "add", "build", "--plan", "PROJ-BUILD", "--shared"), "exists without --force")
}

func TestTargetAddPrintWritesNothing(t *testing.T) {
	h := newHarness(t)
	before, _ := os.ReadFile(filepath.Join(h.root, ".bam.yaml"))
	assert.Equal(t, 0, h.run("preset", "add", "x", "--plan", "PROJ-PROV", "--print"))
	assert.True(t, strings.HasPrefix(h.stdout.String(), "presets:\n  x:\n    plan: PROJ-PROV\n"), h.stdout.String())
	assert.Contains(t, h.stdout.String(), "    variables:\n")
	assert.Equal(t, 0, h.run("preset", "add", "x", "--plan", "PROJ-PROV", "--print", "--shared"))
	assert.True(t, strings.HasPrefix(h.stdout.String(), "targets:\n  x:\n    plan: PROJ-PROV\n"), h.stdout.String())
	after, _ := os.ReadFile(filepath.Join(h.root, ".bam.yaml"))
	assert.Equal(t, before, after)
	assert.NoFileExists(t, filepath.Join(h.home, "bam", "presets.yaml"))
}

func TestPresetAddWritesTheProjectFileInThePresetsDirectory(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(h.home, "bam", "presets", "proj.yaml")
	assert.Equal(t, 0, h.run("preset", "add", "mine", "--plan", "PROJ-PROV"))
	assert.Contains(t, h.stdout.String(), "added preset mine to "+path)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(data), "presets:\n  mine:\n    plan: PROJ-PROV\n"), string(data))
	assert.Contains(t, string(data), "    variables:\n")
	project, _ := os.ReadFile(filepath.Join(h.root, ".bam.yaml"))
	assert.Equal(t, projectYAML, string(project), ".bam.yaml is untouched")

	// The preset works from a directory outside the repository.
	h.env.WorkDir = t.TempDir()
	h.vars["BAM_URL"] = workOrigin
	h.vars["BAM_TOKEN"] = "tok"
	assert.Equal(t, 0, h.run("preset", "show", "mine"))
	assert.Contains(t, h.stdout.String(), path)
	assert.Equal(t, 0, h.run("run", "mine", "--var", "cluster_name=x", "--dry-run"), h.stderr.String())
	assert.NoFileExists(t, filepath.Join(h.home, "bam", "presets.yaml"))
}

func TestPresetAddFileFlag(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("preset", "add", "deploy-package", "--plan", "PROJ-PROV", "--file", "custom-presets"))
	path := filepath.Join(h.home, "bam", "presets", "custom-presets.yaml")
	assert.Contains(t, h.stdout.String(), "added preset deploy-package to "+path)
	assert.FileExists(t, path)
	assert.NoFileExists(t, filepath.Join(h.home, "bam", "presets", "proj.yaml"))

	for _, bad := range []string{"../ops", "a/b", "..", ".hidden"} {
		assert.Equal(t, 2, h.run("preset", "add", "x", "--plan", "PROJ-PROV", "--file", bad), bad)
		assert.Contains(t, h.stderr.String(), "invalid presets file name", bad)
	}
	assert.Empty(t, h.connectOpts[1:], "a bad --file is refused before Bamboo is asked")
	assert.Equal(t, 2, h.run("preset", "add", "x", "--plan", "PROJ-PROV", "--file", "ops", "--shared"))
}

func TestPresetAddRefusesANameInAnotherPresetsFile(t *testing.T) {
	h := newHarness(t)
	old := filepath.Join(h.home, "bam", "presets.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(old), 0o700))
	require.NoError(t, os.WriteFile(old, []byte("presets:\n  mine:\n    plan: PROJ-BUILD\n"), 0o600))

	assert.Equal(t, 2, h.run("preset", "add", "mine", "--plan", "PROJ-PROV", "--force"))
	assert.Contains(t, h.stderr.String(), old)
	assert.NoFileExists(t, filepath.Join(h.home, "bam", "presets", "proj.yaml"))

	// The same file is fine with --force.
	assert.Equal(t, 0, h.run("preset", "add", "other", "--plan", "PROJ-PROV"))
	assert.Equal(t, 2, h.run("preset", "add", "other", "--plan", "PROJ-PROV"))
	assert.Equal(t, 0, h.run("preset", "add", "other", "--plan", "PROJ-PROV", "--force"))

	// The presets file still loads beside the directory.
	assert.Equal(t, 0, h.run("preset", "list"))
	assert.Regexp(t, `mine\s+PROJ-BUILD`, h.stdout.String())
	assert.Regexp(t, `other\s+PROJ-PROV`, h.stdout.String())
}

func TestPresetAddMachineFlagIsGone(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("preset", "add", "mine", "--plan", "PROJ-BUILD", "--machine"))
}

func TestPresetAddHonoursBAMPresets(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "sync", "presets.yaml")
	h.vars["BAM_PRESETS"] = path
	assert.Equal(t, 0, h.run("preset", "add", "mine", "--plan", "PROJ-BUILD"))
	assert.FileExists(t, filepath.Join(filepath.Dir(path), "presets", "proj.yaml"))
	assert.NoDirExists(t, filepath.Join(h.home, "bam"))
}

func TestPresetAddInWSLCreatesTheFileInTheWindowsHome(t *testing.T) {
	h := newHarness(t)
	win := filepath.Join(t.TempDir(), "Users", "jdoe")
	h.vars["WSL_DISTRO_NAME"] = "Ubuntu"
	h.env.WindowsHome = func() (string, error) { return win, nil }
	assert.Equal(t, 0, h.run("preset", "add", "mine", "--plan", "PROJ-BUILD"))
	assert.FileExists(t, filepath.Join(win, "bam", "presets", "proj.yaml"))

	// Next time the glob finds it, and list says where it is.
	h.env.WindowsHome = func() (string, error) { return "", errors.New("must not be asked again") }
	h.env.Glob = func(string) ([]string, error) { return []string{filepath.Join(win, "bam", "presets.yaml")}, nil }
	assert.Equal(t, 0, h.run("preset", "list"))
	assert.Regexp(t, `mine\s+PROJ-BUILD`, h.stdout.String())
	assert.Contains(t, h.stderr.String(), "presets file: "+filepath.Join(win, "bam", "presets.yaml"))
}

func TestPresetListExplainsAmbiguousWSLFiles(t *testing.T) {
	h := newHarness(t)
	h.vars["WSL_DISTRO_NAME"] = "Ubuntu"
	h.env.Glob = func(string) ([]string, error) {
		return []string{"/mnt/c/Users/jdoe/bam/presets.yaml", "/mnt/c/Users/ops/bam/presets.yaml"}, nil
	}
	assert.Equal(t, 0, h.run("preset", "list"))
	assert.Contains(t, h.stderr.String(), "BAM_PRESETS")
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

func TestPresetMigrate(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(h.env.Paths.MachineConfig), 0o755))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig, []byte(`version: 1
targets:
  pkg-a:
    plan: PROJ-BUILD
    defaults:
      env: qa # mine
repos:
  /somewhere/else:
    targets:
      prov-mine:
        plan: PROJ-PROV
`), 0o644))
	presets := filepath.Join(h.home, "bam", "presets.yaml")

	assert.Equal(t, 0, h.run("preset", "migrate", "--dry-run"))
	assert.Contains(t, h.stdout.String(), "would move  pkg-a")
	assert.Contains(t, h.stdout.String(), "would move  prov-mine")
	assert.Contains(t, h.stdout.String(), "dry run: nothing written")
	assert.NoFileExists(t, presets)

	assert.Equal(t, 0, h.run("preset", "migrate"))
	assert.Contains(t, h.stdout.String(), "moved  pkg-a      from targets")
	assert.Contains(t, h.stdout.String(), "moved 2 presets to "+presets)
	data, err := os.ReadFile(presets)
	require.NoError(t, err)
	assert.Contains(t, string(data), "    variables:\n      env: qa # mine\n")

	// Both now run from a directory outside any repository.
	h.env.WorkDir = t.TempDir()
	h.vars["BAM_URL"] = workOrigin
	h.vars["BAM_TOKEN"] = "tok"
	assert.Equal(t, 0, h.run("preset", "show", "prov-mine"))
	assert.Contains(t, h.stdout.String(), presets)

	assert.Equal(t, 0, h.run("preset", "migrate"))
	assert.Contains(t, h.stdout.String(), "no presets to migrate in "+h.env.Paths.MachineConfig)
}

func TestPresetMigrateJSONAndSkips(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(h.env.Paths.MachineConfig), 0o755))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig, []byte("version: 1\ntargets:\n  pkg-a:\n    plan: PROJ-BUILD\n"), 0o644))
	presets := filepath.Join(h.home, "bam", "presets.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(presets), 0o755))
	require.NoError(t, os.WriteFile(presets, []byte("presets:\n  pkg-a:\n    plan: PROJ-PROV\n"), 0o600))

	assert.Equal(t, 0, h.run("preset", "migrate", "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, presets, doc["presets_file"])
	assert.Equal(t, false, doc["dry_run"])
	assert.Empty(t, doc["moved"])
	skipped := doc["skipped"].([]any)
	require.Len(t, skipped, 1)
	assert.Equal(t, "pkg-a", skipped[0].(map[string]any)["name"])
	assert.Contains(t, skipped[0].(map[string]any)["reason"], "already in")
}

func TestPresetListAndSyncUseThePresetsDirectory(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(h.home, "bam", "presets")
	path := filepath.Join(dir, "proj.yaml")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`# PROJ presets
presets:
  lab-dir:
    plan: PROJ-PROV # provisioning
    variables:
      cluster_type: dcos # the old one
`), 0o600))

	assert.Equal(t, 0, h.run("preset", "list"))
	assert.Regexp(t, `lab-dir\s+PROJ-PROV\s+\S+\s+\(default\)\s+no\s+`+regexp.QuoteMeta(path), h.stdout.String())
	assert.Regexp(t, `build\s+PROJ-BUILD.*`+regexp.QuoteMeta(filepath.Join(h.root, ".bam.yaml")), h.stdout.String())
	assert.Contains(t, h.stderr.String(), "presets dir:  "+dir)

	assert.Equal(t, 0, h.run("preset", "sync", "lab-dir"))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(data)
	assert.True(t, strings.HasPrefix(s, "# PROJ presets\npresets:\n  lab-dir:\n    plan: PROJ-PROV # provisioning\n"), s)
	assert.Contains(t, s, "      cluster_type: dcos # the old one\n      cluster_name: \"\"")
}

func TestRunPresetScopedToAnotherRepo(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(h.env.Paths.MachineConfig), 0o755))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig, []byte(`version: 1
repos:
  /somewhere/else:
    targets:
      prov-mine:
        plan: PROJ-PROV
`), 0o644))
	h.env.WorkDir = t.TempDir()
	h.vars["BAM_URL"] = workOrigin
	h.vars["BAM_TOKEN"] = "tok"

	assert.Equal(t, 2, h.run("run", "prov-mine"))
	assert.Contains(t, h.stderr.String(), "defined only for /somewhere/else (repos entry in "+h.env.Paths.MachineConfig+")")
	assert.Contains(t, h.stderr.String(), "bam preset migrate")

	h.stderr.Reset()
	assert.Equal(t, 2, h.run("run", "nope"))
	assert.Contains(t, h.stderr.String(), "no presets apply here; other repositories define some")
	assert.NotContains(t, h.stderr.String(), "no presets are configured")
}

func TestPresetAddFromBuildKeyNeedsNoPlan(t *testing.T) {
	h := newHarness(t)
	b := &h.fake.History["PROJ-PROV12"][0]
	b.Branch, b.MasterKey = "develop", "PROJ-PROV"
	assert.Equal(t, 0, h.run("preset", "add", "x", "--from", "PROJ-PROV12-8", "--print"), h.stderr.String())
	out := h.stdout.String()
	assert.Contains(t, out, "    plan: PROJ-PROV\n")
	assert.Contains(t, out, `    branch: "develop"`)
	assert.Contains(t, out, `cluster_type: "dcos"  # from PROJ-PROV12-8`)
	assert.Equal(t, 2, h.run("preset", "add", "x", "--from", "last"), "--from last still needs --plan")
}
