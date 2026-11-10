package app

import (
	"context"
	"regexp"

	"github.com/rojcarranza/bam/internal/provider"
)

// ExplainLimit is how many failed builds of a tree get a cause line when a
// watch ends. Each one costs a log read, so a tree with more says so.
const ExplainLimit = 5

// secretText matches a secret-looking name followed by its value, as a log
// prints it: password=hunter2, "api_secret": "x", DB_PASSWORD: x.
var secretText = regexp.MustCompile(`(?i)([\w.-]*(?:password|secret|passphrase|sshkey)[\w.-]*["']?\s*[=:]\s*)("[^"]*"|'[^']*'|\S+)`)

// MaskSecretText replaces the value after every secret-looking name in s
// with MaskedDisplay, so a cause line never prints one.
func MaskSecretText(s string) string {
	return secretText.ReplaceAllString(s, "${1}"+MaskedDisplay)
}

// ExplainBuild is the likely cause of a failed build, masked, or "" when
// no log line matched or the logs could not be read. It never fails: a
// missing cause only costs the hint.
func (s *Service) ExplainBuild(ctx context.Context, b provider.Build) string {
	inv, err := s.Investigate(ctx, b, InvestigateOptions{Max: 1})
	if err != nil {
		return ""
	}
	return MaskSecretText(inv.LikelyCause)
}

// ExplainTree sets Cause and Explained on up to ExplainLimit failed builds
// of t that have a failed job, in tree order. Not-run, repeated and
// superseded builds do not count.
func (s *Service) ExplainTree(ctx context.Context, t Tree) {
	if t.Root == nil {
		return
	}
	n := 0
	var walk func(*TreeNode)
	walk = func(k *TreeNode) {
		if k.Superseded {
			return
		}
		if !k.NotRun && !k.Loop && k.Build.State == provider.StateFailed && len(k.Build.FailedJobs()) > 0 && n < ExplainLimit {
			n++
			k.Cause, k.Explained = s.ExplainBuild(ctx, k.Build), true
		}
		for _, c := range k.Children {
			walk(c)
		}
	}
	walk(t.Root)
}
