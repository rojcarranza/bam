package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectList(t *testing.T) {
	h := newHarness(t)
	h.fake.Projects = append(h.fake.Projects, provider.Project{Key: "OPS", Name: "Operations"})
	assert.Equal(t, 0, h.run("project", "list"))
	assert.Contains(t, h.stdout.String(), "PROJ")
	assert.NotContains(t, h.stdout.String(), "OPS", "only configured projects by default")
	assert.Equal(t, 0, h.run("project", "list", "--all"))
	assert.Contains(t, h.stdout.String(), "OPS")
}

func TestProjectListFiltersByText(t *testing.T) {
	h := newHarness(t)
	h.fake.Projects = append(h.fake.Projects, provider.Project{Key: "OPS", Name: "Operations"})
	assert.Equal(t, 0, h.run("project", "list", "--all", "operat"))
	assert.Contains(t, h.stdout.String(), "OPS")
	assert.NotContains(t, h.stdout.String(), "Example Project")
	assert.Equal(t, 0, h.run("project", "list", "--all", "proj", "--json"))
	assert.Contains(t, h.stdout.String(), `"key": "PROJ"`)
	assert.NotContains(t, h.stdout.String(), "OPS")
	assert.Equal(t, 0, h.run("project", "list", "nope"))
	assert.Contains(t, h.stderr.String(), `no projects on work match "nope"`)
}

func TestPlanListGroupsConfiguredProjects(t *testing.T) {
	h := newHarness(t)
	h.tty = true            // relative "ago" times only render on a TTY
	h.vars["TERM"] = "dumb" // ...but without hyperlink escapes muddying the assertions below
	assert.Equal(t, 0, h.run("plan", "list", "--color=never"))
	assert.Contains(t, h.stdout.String(), "PROJ  Example Project")
	assert.Regexp(t, `PROJ-BUILD\s+Build and test\s+#482\s+✓ success\s+12m ago`, h.stdout.String())
	assert.Regexp(t, `PROJ-PROV\s+Provision lab\s+–\s+– never`, h.stdout.String())

	assert.Equal(t, 0, h.run("plan", "list", "--json"))
	var docs []map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &docs))
	assert.Len(t, docs, 2)
	assert.Equal(t, "PROJ-BUILD", docs[0]["key"])
}

func TestPlanListFiltersByMatch(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("plan", "list", "--match", "PROVISION"))
	assert.Contains(t, h.stdout.String(), "PROJ-PROV")
	assert.NotContains(t, h.stdout.String(), "PROJ-BUILD")
	assert.Equal(t, 0, h.run("plan", "list", "--match", "build", "--json"))
	assert.Contains(t, h.stdout.String(), `"key": "PROJ-BUILD"`)
	assert.NotContains(t, h.stdout.String(), "PROJ-PROV")
	assert.Equal(t, 0, h.run("plan", "list", "--match", "nope"))
	assert.Empty(t, h.stdout.String())
	assert.Contains(t, h.stderr.String(), `no plans match "nope"`)
	assert.Equal(t, 0, h.run("plan", "list", "--match", "nope", "--json"))
	assert.Equal(t, "[]\n", h.stdout.String())
}

func TestPlanListWithoutProjectsIsUsageError(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte("version: 1\nservers:\n  work:\n    url: https://bamboo.example.com\n"), 0o644))
	h.fake.Projects = append([]provider.Project{{Key: "OPS", Name: "Operations"}}, h.fake.Projects...)
	assert.Equal(t, 2, h.run("plan", "list"))
	assert.Contains(t, h.stderr.String(), "no projects configured")
	assert.Contains(t, h.stderr.String(), "projects on work: OPS, PROJ", "names the server's projects")
	assert.Contains(t, h.stderr.String(), "try: bam plan list OPS, or add projects: [OPS] to .bam.yaml")

	for _, k := range []string{"LAB", "QA", "WEB", "API", "DOC"} {
		h.fake.Projects = append(h.fake.Projects, provider.Project{Key: k})
	}
	assert.Equal(t, 2, h.run("plan", "list"))
	assert.Contains(t, h.stderr.String(), "projects on work: OPS, PROJ, LAB, QA, WEB and 2 more (bam project list)")
}

func TestPlanListSuggestsCloseProject(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 5, h.run("plan", "list", "PRJ"))
	assert.Contains(t, h.stderr.String(), "project PRJ not found")
	assert.Contains(t, h.stderr.String(), "try: did you mean bam plan list PROJ?")

	assert.Equal(t, 5, h.run("plan", "list", "ZZZZZZ"))
	assert.NotContains(t, h.stderr.String(), "did you mean")
}

func TestPlanListCompletesProjectKeys(t *testing.T) {
	h := newHarness(t)
	h.fake.Projects = append(h.fake.Projects, provider.Project{Key: "OPS"})
	h.run("__complete", "plan", "list", "")
	assert.Contains(t, h.stdout.String(), "PROJ\nOPS\n")
	h.run("__complete", "plan", "list", "PROJ", "")
	assert.NotContains(t, h.stdout.String(), "PROJ\n", "keys already given are not offered again")
	assert.Contains(t, h.stdout.String(), "OPS\n")
}

func TestPlanShowForTarget(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("plan", "show", "provision-lab"))
	out := h.stdout.String()
	assert.Contains(t, out, "PROJ-PROV  Provision lab  https://bamboo.example.com/browse/PROJ-PROV")
	assert.Regexp(t, `Branches +1: develop\n`, out)
	assert.Contains(t, out, "cluster_type=k8s")
	assert.Contains(t, out, "PROJ-PROV12-8")
}

func TestPlanShowJSONReportsVariablesError(t *testing.T) {
	h := newHarness(t)
	h.fake.VariablesErr = errs.Bamboof("boom")
	h.fake.BuildVarsErr = errs.Bamboof("boom")
	assert.Equal(t, 0, h.run("plan", "show", "PROJ-BUILD", "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Contains(t, doc["variables_error"], "boom")
	assert.Equal(t, []any{}, doc["variables"])

	h.fake.VariablesErr = nil
	h.fake.BuildVarsErr = nil
	assert.Equal(t, 0, h.run("plan", "show", "provision-lab", "--json"))
	var okDoc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &okDoc))
	_, hasErr := okDoc["variables_error"]
	assert.False(t, hasErr, "no variables_error key on success")
}

func TestPlanVarsShowsLastUsed(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("plan", "vars", "PROJ-PROV", "--branch", "develop"))
	assert.Contains(t, h.stdout.String(), "LAST USED (#8)")
	assert.Regexp(t, `cluster_name\s+–\s+beta`, h.stdout.String())
}

func TestPlanVarsFiltersByName(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("plan", "vars", "PROJ-PROV", "NAME"))
	assert.Contains(t, h.stdout.String(), "cluster_name")
	assert.NotContains(t, h.stdout.String(), "cluster_type")
	assert.Equal(t, 0, h.run("plan", "vars", "PROJ-PROV", "type", "--json"))
	assert.Contains(t, h.stdout.String(), `"cluster_type"`)
	assert.NotContains(t, h.stdout.String(), `"cluster_name"`)
	assert.Equal(t, 0, h.run("plan", "vars", "PROJ-PROV", "nope"))
	assert.Contains(t, h.stderr.String(), `no variables on PROJ-PROV match "nope"`)
}

func TestPlanBranches(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("plan", "branches", "provision-lab"))
	assert.Regexp(t, `develop\s+PROJ-PROV12`, h.stdout.String())
	assert.Equal(t, 0, h.run("plan", "branches", "PROJ-BUILD"))
	assert.Contains(t, h.stderr.String(), "no branches on PROJ-BUILD")
}

func TestPlanBranchesFiltersByText(t *testing.T) {
	h := newHarness(t)
	h.fake.Branches["PROJ-PROV"] = append(h.fake.Branches["PROJ-PROV"],
		provider.Branch{Key: "PROJ-PROV7", Name: "Provision lab - feat/login", ShortName: "feat/login", PlanKey: "PROJ-PROV"})
	assert.Equal(t, 0, h.run("plan", "branches", "PROJ-PROV", "FEAT"))
	assert.Regexp(t, `feat/login\s+PROJ-PROV7`, h.stdout.String())
	assert.NotContains(t, h.stdout.String(), "develop")
	assert.Equal(t, 0, h.run("plan", "branches", "PROJ-PROV", "prov12", "--json"))
	assert.Contains(t, h.stdout.String(), `"short_name": "develop"`)
	assert.NotContains(t, h.stdout.String(), "feat/login")
	assert.Equal(t, 0, h.run("plan", "branches", "PROJ-PROV", "nope"))
	assert.Contains(t, h.stderr.String(), `no branches on PROJ-PROV match "nope"`)
}

func TestPlanBranchesMarksDisabled(t *testing.T) {
	h := newHarness(t)
	h.fake.Branches["PROJ-PROV"] = append(h.fake.Branches["PROJ-PROV"],
		provider.Branch{Key: "PROJ-PROV7", Name: "Provision lab - feat/old", ShortName: "feat/old", PlanKey: "PROJ-PROV", Disabled: true})
	assert.Equal(t, 0, h.run("plan", "branches", "PROJ-PROV"))
	assert.Regexp(t, `develop\s+PROJ-PROV12`, h.stdout.String())
	assert.Regexp(t, `feat/old \(disabled\)\s+PROJ-PROV7`, h.stdout.String())
	assert.Equal(t, 0, h.run("plan", "branches", "PROJ-PROV", "--json"))
	assert.Regexp(t, `"short_name": "develop",\s+"plan_key": "PROJ-PROV",\s+"enabled": true`, h.stdout.String())
	assert.Regexp(t, `"short_name": "feat/old",\s+"plan_key": "PROJ-PROV",\s+"enabled": false`, h.stdout.String())
	assert.Equal(t, 0, h.run("plan", "show", "PROJ-PROV"))
	assert.Contains(t, h.stdout.String(), "Branches    2: develop, feat/old (disabled)\n")
}

func TestPlanListAndShowMarkDisabledPlans(t *testing.T) {
	h := newHarness(t)
	ps := h.fake.Plans["PROJ"]
	for i := range ps {
		ps[i].Disabled = ps[i].Key == "PROJ-PROV"
	}
	assert.Equal(t, 0, h.run("plan", "list", "PROJ"))
	assert.Regexp(t, `PROJ-PROV\s+Provision lab \(disabled\)`, h.stdout.String())
	assert.Regexp(t, `PROJ-BUILD\s+Build and test\s`, h.stdout.String())
	assert.Equal(t, 0, h.run("plan", "list", "PROJ", "--json"))
	assert.Regexp(t, `"key": "PROJ-PROV",[^}]*"enabled": false`, h.stdout.String())
	assert.Equal(t, 0, h.run("plan", "show", "PROJ-PROV"))
	assert.Contains(t, h.stdout.String(), "Provision lab (disabled)")
	assert.Equal(t, 0, h.run("plan", "show", "PROJ-PROV", "--json"))
	assert.Regexp(t, `"enabled": false`, h.stdout.String())
}

func TestBuildList(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("build", "list", "PROJ-BUILD"))
	assert.Contains(t, h.stdout.String(), "PROJ-BUILD-482")
	assert.Equal(t, 2, h.run("build", "list", "PROJ-BUILD", "--state", "green"))
	assert.NotContains(t, h.stderr.String(), "did you mean")
	assert.Equal(t, 2, h.run("build", "list", "PROJ-BUILD", "--state", "faild"))
	assert.Contains(t, h.stderr.String(), "did you mean --state failed?")
	for _, s := range []string{"not-built", "NotBuilt", "not built"} {
		assert.Equal(t, 0, h.run("build", "list", "PROJ-BUILD", "--state", s), s)
	}
	for _, s := range []string{"Successful", "In progress", "failure", "cancelled"} {
		assert.Equal(t, 0, h.run("build", "list", "PROJ-BUILD", "--state", s), s)
	}
	assert.Equal(t, 0, h.run("build", "list", "PROJ-BUILD", "--state", "FAILED"))
	assert.Equal(t, "no failed builds for PROJ-BUILD\n  try: bam build list PROJ-BUILD\n", h.stderr.String())
	assert.Equal(t, 0, h.run("build", "list", "provision-lab", "--state", "success"))
	assert.Empty(t, h.stdout.String())
	assert.Contains(t, h.stderr.String(), "no success builds for PROJ-PROV12 on branch develop")
	assert.Contains(t, h.stderr.String(), "try: bam build list provision-lab\n")
	assert.Equal(t, 0, h.run("build", "list", "PROJ-PROV", "--branch", "develop", "--state", "success"))
	assert.Contains(t, h.stderr.String(), "try: bam build list PROJ-PROV --branch develop\n")

	// A result list may not say which branch a build ran on, so the branch
	// comes from the one asked for.
	assert.Equal(t, 0, h.run("build", "list", "PROJ-PROV", "--branch", "develop"))
	assert.Regexp(t, `PROJ-PROV12-8\s+\S+ failed\s+develop`, h.stdout.String())
	assert.Equal(t, 0, h.run("--json", "build", "list", "PROJ-PROV", "--branch", "develop"))
	assert.Contains(t, h.stdout.String(), `"branch": "develop"`)
	assert.Equal(t, 0, h.run("plan", "show", "PROJ-PROV", "--branch", "develop"))
	assert.Regexp(t, `PROJ-PROV12-8\s+\S+ failed`, h.stdout.String())

	assert.Equal(t, 2, h.run("build", "list", "PROJ-BUILD", "--limit", "0"))
	assert.Equal(t, 2, h.run("build", "list", "PROJ-BUILD", "--limit", "-1"))
	assert.Contains(t, h.stderr.String(), "--limit takes 1 or more, not -1")
}

func TestBuildShow(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("build", "show", "PROJ-PROV12-8"))
	assert.Contains(t, h.stdout.String(), "Failure")
	assert.Contains(t, h.stdout.String(), "bam logs PROJ-PROV12-8 --job TF")

	assert.Equal(t, 0, h.run("build", "show", "provision-lab", "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "PROJ-PROV12-8", doc["key"])

	assert.Equal(t, 2, h.run("build", "show", "--last"))
	assert.Contains(t, h.stderr.String(), "no build triggered from this repository")
	assert.Equal(t, 2, h.run("build", "show"))
}

func TestPlanShowListsARunningBuild(t *testing.T) {
	// Bamboo leaves a running build out of a result list unless asked.
	h := newHarness(t)
	runningLab(h)
	assert.Equal(t, 0, h.run("plan", "show", "provision-lab"))
	assert.Contains(t, h.stdout.String(), "PROJ-PROV12-8")
}

func TestProjectFlagCompletesProjectKeys(t *testing.T) {
	h := newHarness(t)
	h.fake.Projects = append(h.fake.Projects, provider.Project{Key: "OPS"})
	h.run("__complete", "ui", "--project", "")
	assert.Contains(t, h.stdout.String(), "PROJ\nOPS\nall\n")
	h.run("__complete", "init", "--server", "work", "--project", "PROJ", "--project", "")
	assert.NotContains(t, h.stdout.String(), "PROJ\n", "keys already given are not offered again")
	assert.Contains(t, h.stdout.String(), "OPS\n")
}

func TestBuildListCompletesStates(t *testing.T) {
	h := newHarness(t)
	h.run("__complete", "build", "list", "PROJ-API", "--state", "")
	assert.Contains(t, h.stdout.String(), "failed\n")
	assert.Contains(t, h.stdout.String(), "success\n")
	assert.Contains(t, h.stdout.String(), ":4\n", "no file completion")
}

func TestColorFlagCompletesModes(t *testing.T) {
	h := newHarness(t)
	h.run("__complete", "plan", "list", "--color", "")
	assert.Contains(t, h.stdout.String(), "auto\nalways\nnever\n:4\n")
}

func TestArgumentsNeverCompleteFileNames(t *testing.T) {
	h := newHarness(t)
	for _, line := range [][]string{{"preset", "list", ""}, {"set", "list", ""}, {"project", "list", ""}, {"doctor", ""}, {""}} {
		h.run(append([]string{"__complete"}, line...)...)
		assert.Contains(t, h.stdout.String(), ":4\n", line)
	}
}

func TestBranchFlagCompletesPlanBranches(t *testing.T) {
	h := newHarness(t)
	for _, line := range [][]string{
		{"run", "provision-lab", "--branch", ""},
		{"plan", "show", "PROJ-PROV", "--branch", ""},
		{"preset", "add", "new-one", "--plan", "PROJ-PROV", "--branch", ""},
	} {
		h.run(append([]string{"__complete"}, line...)...)
		assert.Contains(t, h.stdout.String(), "develop\n", line)
		assert.Contains(t, h.stdout.String(), ":4\n", "no file names")
	}
	h.run("__complete", "run", "PROJ-BUILD-12", "--branch", "")
	assert.NotContains(t, h.stdout.String(), "develop", "a build key names no plan")
}

func TestPlanFlagCompletesConfiguredPlans(t *testing.T) {
	h := newHarness(t)
	for _, line := range [][]string{{"doctor", "--plan", ""}, {"preset", "add", "x", "--plan", ""}} {
		h.run(append([]string{"__complete"}, line...)...)
		assert.Equal(t, "PROJ-BUILD\tBuild and test\nPROJ-PROV\tProvision lab\n:4\n", h.stdout.String(), line)
	}
	// init lists its own --project values and skips plans already given.
	h.run("__complete", "init", "--server", "work", "--project", "PROJ", "--plan", "PROJ-BUILD=build", "--plan", "")
	assert.Equal(t, "PROJ-PROV\tProvision lab\n:4\n", h.stdout.String())
}

func TestFromFlagCompletesRecentBuilds(t *testing.T) {
	h := newHarness(t)
	h.run("__complete", "run", "build", "--from", "")
	assert.Contains(t, h.stdout.String(), "last\tmost recent manual build\n")
	assert.Contains(t, h.stdout.String(), "482\tsuccess\n")
	// The preset's develop branch numbers its own builds.
	h.run("__complete", "run", "provision-lab", "--from", "")
	assert.Contains(t, h.stdout.String(), "8\tfailed\n")
	h.run("__complete", "preset", "add", "x", "--from", "")
	assert.Equal(t, "last\tmost recent manual build\n:36\n", h.stdout.String(), "no plan, so only last")
}

func TestRevisionFlagCompletesRecentCommits(t *testing.T) {
	h := newHarness(t)
	b := h.fake.History["PROJ-BUILD"][0]
	rev := func(n int, state provider.State, commit string) provider.Build {
		c := b
		c.Number, c.State, c.Revision = n, state, commit
		return c
	}
	h.fake.History["PROJ-BUILD"] = []provider.Build{
		rev(484, provider.StateFailed, "9fceb02d0ae598e95dc970b74767f19372d61af8"),
		rev(483, provider.StateSuccess, "9fceb02d0ae598e95dc970b74767f19372d61af8"),
		rev(482, provider.StateSuccess, "e83c5163316f89bfbde7d9ab23ca2e25604af290"),
		rev(481, provider.StateSuccess, ""),
	}
	h.run("__complete", "run", "build", "--revision", "")
	assert.Equal(t, "from\tthe commit of the --from build\n"+
		"9fceb02d0ae598e95dc970b74767f19372d61af8\t#484 failed\n"+
		"e83c5163316f89bfbde7d9ab23ca2e25604af290\t#482 success\n:36\n", h.stdout.String(), "newest first, each commit once")
	h.run("__complete", "run", "--revision", "")
	assert.Equal(t, "from\tthe commit of the --from build\n:36\n", h.stdout.String(), "no plan, so only from")
}

func TestJobFlagCompletesBuildJobs(t *testing.T) {
	h := newHarness(t)
	for _, line := range [][]string{
		{"logs", "PROJ-PROV12-8", "--job", ""},
		{"url", "PROJ-PROV12-8", "--job", ""},
		{"open", "PROJ-PROV12-8", "--job", ""},
	} {
		h.run(append([]string{"__complete"}, line...)...)
		assert.Equal(t, "TF\tTerraform\n:36\n", h.stdout.String(), line)
	}
	h.run("__complete", "logs", "--job", "")
	assert.Equal(t, ":4\n", h.stdout.String(), "no build, no jobs")
}

// -n is the count shorthand head, tail and git log users reach for first.
func TestCountFlagsTakeDashN(t *testing.T) {
	h := newHarness(t)
	for _, c := range [][2][]string{
		{{"build", "list", "PROJ-BUILD", "--limit", "1"}, {"build", "list", "PROJ-BUILD", "-n", "1"}},
		{{"logs", "PROJ-PROV12-8", "--tail", "1"}, {"logs", "PROJ-PROV12-8", "-n", "1"}},
		{{"plan", "stats", "PROJ-BUILD", "--last", "2"}, {"plan", "stats", "PROJ-BUILD", "-n", "2"}},
	} {
		assert.Equal(t, 0, h.run(c[0]...), c[0])
		long := h.stdout.String()
		assert.Equal(t, 0, h.run(c[1]...), h.stderr.String())
		assert.Equal(t, long, h.stdout.String(), c[1])
	}
}
