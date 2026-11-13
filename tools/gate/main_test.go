package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// standIn does to Git what no-mistakes 1.99 does for the two commands make
// gate runs (internal/gate/gate.go and internal/cli/status.go): a worktree whose
// common git folder is a main checkout's .git shares that checkout's gate;
// any other worktree, such as one of a bare clone, gets a gate of its own. Init
// writes the "no-mistakes" remote with git remote add or set-url, which lands
// in the config every worktree shares. Status prints the gate line as
// no-mistakes does.
const standIn = `#!/bin/sh
set -eu
common=$(git rev-parse --path-format=absolute --git-common-dir)
case "$common" in
*/.git) top=${common%/.git} ;;
*) top=$(git rev-parse --show-toplevel) ;;
esac
gate="$GATES/$(basename "$top").git"
case "$1" in
init)
	[ -d "$gate" ] || git init -q --bare "$gate"
	if git remote get-url no-mistakes >/dev/null 2>&1; then
		git remote set-url no-mistakes "$gate"
	else
		git remote add no-mistakes "$gate"
	fi
	;;
status)
	[ -d "$gate" ] || { echo "repo not initialized (run 'no-mistakes init' first)"; exit 0; }
	printf '    repo:  %s\n  remote:  %s\n    gate:  %s\n' "$top" "$(git remote get-url origin)" "$gate"
	;;
esac
`

type lab struct {
	root   string
	common string // the bare clone's shared git folder
	tool   string // the no-mistakes stand-in
}

// newLab makes the layout the bug needs: a bare clone with linked worktrees,
// as a tool that keeps one worktree per task lays it out.
func newLab(t *testing.T, worktrees ...string) lab {
	t.Helper()
	requireGit(t)
	root := t.TempDir()
	l := lab{root: root, common: filepath.Join(root, "common"), tool: filepath.Join(root, "bin", "no-mistakes")}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))
	script := strings.Replace(standIn, "set -eu\n", "set -eu\nGATES='"+filepath.Join(root, "gates")+"'\n", 1)
	require.NoError(t, os.WriteFile(l.tool, []byte(script), 0o755))

	src := filepath.Join(root, "src")
	gitIn(t, root, "init", "-q", "-b", "main", src)
	gitIn(t, src, "commit", "-q", "--allow-empty", "-m", "init")
	gitIn(t, root, "clone", "-q", "--bare", src, l.common)
	for _, w := range worktrees {
		gitIn(t, l.common, "worktree", "add", "-q", l.dir(w), "-b", w, "main")
	}
	return l
}

func (l lab) dir(worktree string) string { return filepath.Join(l.root, worktree) }

func (l lab) gate(worktree string) string { return filepath.Join(l.root, "gates", worktree+".git") }

// makeGate runs what make gate runs, in one worktree.
func (l lab) makeGate(t *testing.T, worktree string) string {
	t.Helper()
	var out bytes.Buffer
	s := setup{dir: l.dir(worktree), noMistakes: l.tool, out: &out, errOut: &out}
	require.NoError(t, s.run(), out.String())
	return out.String()
}

// plainInit runs no-mistakes init alone, as a task that skipped make gate does.
func (l lab) plainInit(t *testing.T, worktree string) {
	t.Helper()
	cmd := exec.Command(l.tool, "init")
	cmd.Dir = l.dir(worktree)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

// remoteOf is what Git resolves the no-mistakes remote to in a worktree:
// the URL fetches use, and every URL a push goes to.
func remoteOf(t *testing.T, dir string) (fetch, push string) {
	t.Helper()
	return gitIn(t, dir, "remote", "get-url", "no-mistakes"),
		gitIn(t, dir, "remote", "get-url", "--push", "--all", "no-mistakes")
}

func TestMakeGateKeepsEachWorktreeOnItsOwnGate(t *testing.T) {
	l := newLab(t, "wa", "wb")
	l.makeGate(t, "wa")
	l.makeGate(t, "wb")
	// A task that later runs plain no-mistakes init in wb re-points the
	// shared remote; wa must not follow it.
	l.plainInit(t, "wb")

	for _, w := range []string{"wa", "wb"} {
		fetch, push := remoteOf(t, l.dir(w))
		assert.Equal(t, l.gate(w), fetch, "%s fetches from", w)
		assert.Equal(t, l.gate(w), push, "%s pushes to", w)
	}

	gitIn(t, l.dir("wa"), "push", "-q", "no-mistakes", "HEAD:refs/heads/wa-branch")
	gitIn(t, l.dir("wb"), "push", "-q", "no-mistakes", "HEAD:refs/heads/wb-branch")
	assert.Equal(t, "wa-branch", branches(t, l.gate("wa")))
	assert.Equal(t, "wb-branch", branches(t, l.gate("wb")))
}

func TestMakeGateLeavesTheSharedRemoteAsItWas(t *testing.T) {
	l := newLab(t, "wa", "wc")
	l.plainInit(t, "wc") // a task that never ran make gate

	out := l.makeGate(t, "wa")

	fetch, push := remoteOf(t, l.dir("wc"))
	assert.Equal(t, l.gate("wc"), fetch, "wc fetches from")
	assert.Equal(t, l.gate("wc"), push, "wc pushes to")
	assert.Contains(t, out, "gate: this worktree pushes to "+l.gate("wa"))
}

func TestMakeGateInANormalCloneOnlyRunsInit(t *testing.T) {
	l := newLab(t)
	src := l.dir("src")
	gitIn(t, src, "worktree", "add", "-q", l.dir("src-wt"), "-b", "wt", "main")

	for _, w := range []string{"src", "src-wt"} {
		out := l.makeGate(t, w)
		assert.NotContains(t, out, "gate: this worktree pushes to", w)
		fetch, push := remoteOf(t, l.dir(w))
		assert.Equal(t, l.gate("src"), fetch, "%s fetches from", w)
		assert.Equal(t, l.gate("src"), push, "%s pushes to", w)
	}
	assert.Empty(t, includes(t, filepath.Join(src, ".git", "config")))
}

func TestRemovedWorktreeLeavesNoPinBehind(t *testing.T) {
	l := newLab(t, "wa", "wb")
	l.makeGate(t, "wa")
	l.makeGate(t, "wb")
	shared := filepath.Join(l.common, "config")
	require.Len(t, includes(t, shared), 2)

	gitIn(t, l.common, "worktree", "remove", "--force", l.dir("wb"))
	l.makeGate(t, "wa")

	assert.Equal(t, []string{filepath.Join(gitIn(t, l.dir("wa"), "rev-parse", "--absolute-git-dir"), pinFile)}, includes(t, shared))
}

// includes lists the files a config file includes conditionally.
func includes(t *testing.T, config string) []string {
	t.Helper()
	cmd := exec.Command("git", "config", "--file", config, "--get-regexp", `^includeif\.`)
	out, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return nil
	}
	require.NoError(t, err)
	var files []string
	for line := range strings.Lines(strings.TrimSpace(string(out))) {
		_, file, _ := strings.Cut(strings.TrimSpace(line), " ")
		files = append(files, file)
	}
	return files
}

func branches(t *testing.T, gate string) string {
	t.Helper()
	return gitIn(t, gate, "for-each-ref", "--format=%(refname:short)", "refs/heads")
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// requireGit skips unless git can clear a remote's URL list, which it does
// from 2.46, and keeps the user's own git config out of the test.
func requireGit(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the no-mistakes stand-in is a shell script")
	}
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		t.Skip("git is not installed")
	}
	m := regexp.MustCompile(`(\d+)\.(\d+)`).FindStringSubmatch(string(out))
	require.NotNil(t, m, "git --version: %s", out)
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major < 2 || major == 2 && minor < 46 {
		t.Skipf("%s cannot clear a remote's URL list; make gate needs 2.46 or later", strings.TrimSpace(string(out)))
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "jdoe")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "jdoe@example.com")
	}
}
