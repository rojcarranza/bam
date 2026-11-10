package cli

import (
	"os/exec"
	"strings"

	"github.com/rojcarranza/bam/internal/errs"
)

// gitBranch is the branch checked out in dir. A detached HEAD has none.
func gitBranch(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "symbolic-ref", "--short", "-q", "HEAD").Output()
	if err != nil {
		return "", errs.Usagef("no branch is checked out here").
			WithWhy("this is not a git repository, or HEAD is detached").
			WithTry("name the branch instead of @")
	}
	return strings.TrimSpace(string(out)), nil
}

// checkedOutBranch is what "@" stands for.
func (r *runtime) checkedOutBranch() (string, error) {
	if r.env.GitBranch == nil {
		return "", errs.Usagef("bam cannot read the checked-out branch here").WithTry("name the branch instead of @")
	}
	return r.env.GitBranch(r.env.WorkDir)
}

// gitHead is the commit checked out in dir and whether any remote-tracking
// branch contains it, which is whether a push of it reached the remote.
func gitHead(dir string) (string, bool, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "-q", "--verify", "HEAD").Output()
	if err != nil {
		return "", false, errs.Usagef("no commit is checked out here").
			WithWhy("this is not a git repository, or it has no commits").
			WithTry("run bam from a repository with a commit")
	}
	sha := strings.TrimSpace(string(out))
	out, err = exec.Command("git", "-C", dir, "branch", "-r", "--contains", sha).Output()
	return sha, err == nil && strings.TrimSpace(string(out)) != "", nil
}

// checkedOutCommit is the commit "bam watch @" waits for a build of.
func (r *runtime) checkedOutCommit() (string, bool, error) {
	if r.env.GitHead == nil {
		return "", false, errs.Usagef("bam cannot read the checked-out commit here").WithTry("name a build instead of @")
	}
	return r.env.GitHead(r.env.WorkDir)
}
