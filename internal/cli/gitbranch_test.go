package cli

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BAM-11 P5: "@" reads the branch checked out in the working directory.

func TestGitBranchReadsTheCheckedOutBranch(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "develop"}, {"checkout", "-q", "-b", "feature/x"}} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	b, err := gitBranch(dir)
	require.NoError(t, err)
	assert.Equal(t, "feature/x", b)

	_, err = gitBranch(t.TempDir())
	assert.Error(t, err, "not a git repository")
}

func TestAtBranchWithNoPlanBranchPrintsTheCreateLinkAndCreatesNothing(t *testing.T) {
	h := newHarness(t)
	h.env.GitBranch = func(string) (string, error) { return "devlop", nil }
	assert.Equal(t, 2, h.run("plan", "show", "PROJ-PROV", "--branch", "@"))
	assert.Contains(t, h.stderr.String(), `PROJ-PROV has no plan branch for "devlop"`)
	assert.Contains(t, h.stderr.String(), "the closest plan branch is develop")
	assert.Contains(t, h.stderr.String(), h.fake.CreateBranchURL("PROJ-PROV"))

	h.env.GitBranch = func(string) (string, error) { return "develop", nil }
	assert.Equal(t, 0, h.run("plan", "show", "PROJ-PROV", "--branch", "@"))
	assert.Regexp(t, `PROJ-PROV12-8\s+\S+ failed`, h.stdout.String())
}

// BAM-11 P17: "bam watch @" needs the checked-out commit and whether it is pushed.

func TestGitHeadSaysWhetherAPushReachedTheRemote(t *testing.T) {
	git := func(dir string, args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=jdoe", "-c", "user.email=jdoe@example.com"}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	remote, dir := t.TempDir(), t.TempDir()
	git(remote, "init", "-q", "--bare", "-b", "main")
	git(dir, "init", "-q", "-b", "main")
	git(dir, "remote", "add", "origin", remote)
	git(dir, "commit", "-q", "--allow-empty", "-m", "first")
	sha, pushed, err := gitHead(dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)
	assert.False(t, pushed, "nothing pushed yet")

	git(dir, "push", "-q", "-u", "origin", "main")
	_, pushed, err = gitHead(dir)
	require.NoError(t, err)
	assert.True(t, pushed)

	git(dir, "commit", "-q", "--allow-empty", "-m", "second")
	newSHA, pushed, err := gitHead(dir)
	require.NoError(t, err)
	assert.NotEqual(t, sha, newSHA)
	assert.False(t, pushed, "the new commit is local only")

	_, _, err = gitHead(t.TempDir())
	assert.Error(t, err, "not a git repository")
}
