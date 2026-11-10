package cli

import (
	"os/exec"
	"strings"

	"github.com/r0jjames/bam-cli/internal/errs"
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
