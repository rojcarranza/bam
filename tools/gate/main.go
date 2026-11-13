// Command gate sets up the no-mistakes push gate for this working copy.
//
//	make gate
//
// It runs `no-mistakes init`. In a normal clone that is all it does.
//
// In a linked worktree of a bare clone (git clone --bare, then git worktree
// add), no-mistakes registers a separate gate for each worktree but writes the
// "no-mistakes" remote into the config every worktree shares, so `no-mistakes
// init` in one worktree re-points the remote the others push to. So after
// init, gate pins the remote to this worktree's own gate, in a file inside the
// worktree's own git folder that the shared config includes only for this
// worktree, and puts the shared value back to what it was (removing it again
// if there was none). The pin goes away with the worktree.
//
// Git's per-worktree config (extensions.worktreeConfig) is not used: turning it
// on in a bare clone breaks every linked worktree until core.bare is moved.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const (
	remote  = "no-mistakes"
	urlKey  = "remote." + remote + ".url"
	pushKey = "remote." + remote + ".pushurl"
	// pinFile is the pin's name inside a worktree's own git folder.
	pinFile = "no-mistakes-remote.config"
)

func main() {
	s := setup{dir: ".", noMistakes: "no-mistakes", out: os.Stdout, errOut: os.Stderr}
	if err := s.run(); err != nil {
		fmt.Fprintln(os.Stderr, "gate:", err)
		os.Exit(1)
	}
}

type setup struct {
	dir        string // the working copy
	noMistakes string // the no-mistakes command
	out        io.Writer
	errOut     io.Writer
}

func (s setup) run() error {
	gitDir, err := s.git("rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	commonDir, err := s.git("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	shared := filepath.Join(commonDir, "config")
	bare, err := s.configValues("config", "--file", shared, "--type=bool", "--null", "--get-all", "core.bare")
	if err != nil {
		return err
	}
	if samePath(gitDir, commonDir) || len(bare) != 1 || bare[0] != "true" {
		// A normal clone: no-mistakes gives all its worktrees the gate of
		// the main working tree, so the shared remote is right for all.
		return s.init()
	}

	if err := s.canResetURLs(); err != nil {
		return err
	}
	before, err := s.configAll(shared, urlKey)
	if err != nil {
		return err
	}
	if err := s.init(); err != nil {
		return err
	}
	gate, err := s.pinOwnGate(gitDir, shared)
	if rerr := s.restoreShared(shared, before, gate); rerr != nil {
		return errors.Join(err, fmt.Errorf("putting back the shared no-mistakes remote: %w", rerr))
	}
	if err == nil {
		err = s.checkPin(gate)
		if err != nil {
			s.unpin(gitDir, shared)
		}
	}
	if err != nil {
		return fmt.Errorf("%w; the no-mistakes remote was put back as it was, run make gate again before pushing", err)
	}
	if err := s.pruneStale(shared); err != nil {
		return err
	}
	fmt.Fprintf(s.out, "gate: this worktree pushes to %s\n", gate)
	fmt.Fprintln(s.out, "gate: other worktrees of this clone keep their own no-mistakes remote")
	return nil
}

// pinOwnGate reads this worktree's gate and pins the no-mistakes remote to it.
// It returns the gate whenever no-mistakes named one, even if pinning failed.
func (s setup) pinOwnGate(gitDir, shared string) (string, error) {
	gate, err := s.gatePath()
	if err != nil {
		return "", err
	}
	pin := filepath.Join(gitDir, pinFile)
	if err := s.writePin(pin, gate); err != nil {
		return gate, err
	}
	if _, err := s.git("config", "--file", shared, includeKey(gitDir), pin); err != nil {
		_ = os.Remove(pin)
		return gate, err
	}
	return gate, nil
}

// unpin removes this worktree's include and pin.
func (s setup) unpin(gitDir, shared string) {
	_, _ = s.git("config", "--file", shared, "--unset", includeKey(gitDir))
	_ = os.Remove(filepath.Join(gitDir, pinFile))
}

func includeKey(gitDir string) string {
	return "includeIf.gitdir:" + globEscape(gitDir) + ".path"
}

// restoreShared puts the shared no-mistakes URLs back to before, undoing what
// no-mistakes init wrote there: when gate is known, only if the shared value is
// still exactly that gate, so a later change by someone else is kept; when no
// gate was named, whenever the value differs from before. If there was no
// shared URL before, the remote section init added is removed again.
func (s setup) restoreShared(shared string, before []string, gate string) error {
	now, err := s.configAll(shared, urlKey)
	if err != nil {
		return err
	}
	if slices.Equal(now, before) || gate != "" && !slices.Equal(now, []string{gate}) {
		return nil
	}
	if len(before) == 0 {
		_, err := s.git("config", "--file", shared, "--remove-section", "remote."+remote)
		return err
	}
	if _, err := s.git("config", "--file", shared, "--replace-all", urlKey, before[0]); err != nil {
		return err
	}
	for _, url := range before[1:] {
		if _, err := s.git("config", "--file", shared, "--add", urlKey, url); err != nil {
			return err
		}
	}
	return nil
}

// init runs no-mistakes init, showing its output.
func (s setup) init() error {
	cmd := exec.Command(s.noMistakes, "init")
	cmd.Dir = s.dir
	cmd.Stdout, cmd.Stderr = s.out, s.errOut
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("no-mistakes is not installed; see SETUP.md")
		}
		return fmt.Errorf("no-mistakes init: %w", err)
	}
	return nil
}

// gatePath asks no-mistakes which gate it registered for this worktree. The
// gate's name is no-mistakes' own choice and survives a moved worktree, so it
// is read, not worked out.
func (s setup) gatePath() (string, error) {
	cmd := exec.Command(s.noMistakes, "status")
	cmd.Dir = s.dir
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("no-mistakes status: %w", err)
	}
	for line := range strings.Lines(string(out)) {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "gate:"); ok {
			path = strings.TrimSpace(path)
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				return path, nil
			}
			return "", fmt.Errorf("no-mistakes status names gate %q, which is not a folder", path)
		}
	}
	return "", errors.New("no-mistakes status did not name this worktree's gate")
}

// canResetURLs checks that this Git lets a later config file clear a remote's
// URL list with an empty value, which the pin relies on. Git 2.46 added it.
func (s setup) canResetURLs() error {
	got, err := s.git("-c", "remote.probe.url=/first", "-c", "remote.probe.url=", "-c", "remote.probe.url=/second",
		"ls-remote", "--get-url", "probe")
	if err != nil {
		return err
	}
	if got != "/second" {
		version, _ := s.git("--version")
		return fmt.Errorf("%s cannot clear a remote's URL list, which keeping each worktree on its own gate needs; "+
			"install Git 2.46 or later, then run make gate again (nothing was changed)", version)
	}
	return nil
}

// writePin writes the worktree's remote: an empty url clears the shared list,
// then this worktree's gate. pushurl keeps pushes on it even if the shared
// remote is later re-added below the include.
func (s setup) writePin(pin, gate string) error {
	tmp := pin + ".tmp"
	_ = os.Remove(tmp)
	for _, args := range [][]string{
		{"config", "--file", tmp, "--add", urlKey, ""},
		{"config", "--file", tmp, "--add", urlKey, gate},
		{"config", "--file", tmp, pushKey, gate},
	} {
		if _, err := s.git(args...); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	return os.Rename(tmp, pin)
}

// checkPin confirms that fetches and pushes from this worktree now resolve to
// its own gate.
func (s setup) checkPin(gate string) error {
	fetch, err := s.git("remote", "get-url", remote)
	if err != nil {
		return err
	}
	push, err := s.git("remote", "get-url", "--push", "--all", remote)
	if err != nil {
		return err
	}
	if fetch != gate || push != gate {
		return fmt.Errorf("the pinned remote resolves to %q for fetch and %q for push, not %q; nothing was pinned", fetch, push, gate)
	}
	return nil
}

// pruneStale removes the includes of worktrees that no longer exist, so the
// shared config does not keep one line for every worktree ever made.
func (s setup) pruneStale(shared string) error {
	entries, err := s.configRegexp(shared, `^includeif\.gitdir:.*\.path$`)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		key, pin, ok := strings.Cut(entry, "\n")
		if !ok || filepath.Base(pin) != pinFile {
			continue
		}
		if _, err := os.Stat(filepath.Dir(pin)); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		// Git lists the key as includeif.<subsection>.path but matches
		// --remove-section against the section's name as written.
		section := "includeIf." + strings.TrimSuffix(strings.TrimPrefix(key, "includeif."), ".path")
		if _, err := s.git("config", "--file", shared, "--remove-section", section); err != nil {
			return err
		}
	}
	return nil
}

// configAll reads every value of key in one config file; none is not an error.
func (s setup) configAll(file, key string) ([]string, error) {
	return s.configValues("config", "--file", file, "--null", "--get-all", key)
}

// configRegexp reads every "key\nvalue" entry whose key matches pattern.
func (s setup) configRegexp(file, pattern string) ([]string, error) {
	return s.configValues("config", "--file", file, "--null", "--get-regexp", pattern)
}

func (s setup) configValues(args ...string) ([]string, error) {
	out, err := s.git(args...)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(out, "\x00"), "\x00"), nil
}

func (s setup) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// globEscape quotes the characters includeIf's gitdir pattern treats as a glob.
func globEscape(path string) string {
	var b strings.Builder
	for _, r := range path {
		if strings.ContainsRune(`\*?[`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}
