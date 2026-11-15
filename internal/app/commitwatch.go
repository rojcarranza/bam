package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
)

// CommitWaitDefault is how long bam watch @ waits for Bamboo to start a
// build of the checked-out commit when --timeout is not given. Bamboo finds
// a push by polling the repository, which can take minutes.
const CommitWaitDefault = 10 * time.Minute

// commitScan is how many of a branch's newest builds are checked for the commit.
const commitScan = 10

// CommitPlan returns the plan (branch) key whose build of the checked-out
// commit "bam watch" follows. arg "@" means the plan of the last build bam
// started in this repository; any other arg is a preset or plan key. branch
// is as for ResolvePlan.
func (s *Service) CommitPlan(ctx context.Context, arg, branch string) (string, error) {
	if arg == CheckedOutMarker {
		if branch != "" && branch != CheckedOutMarker {
			return "", errs.Usagef("%s already means the checked-out branch, so --branch %s does not apply", arg, branch).
				WithTry("bam watch PROJ-PLAN --branch @, or drop --branch")
		}
		rec, ok, err := s.State.Last(s.Config().RepoRoot)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errs.Usagef("no build triggered from this repository yet, so bam does not know the plan").
				WithTry("bam watch PROJ-PLAN --branch @")
		}
		if rec.Origin != "" && rec.Origin != s.Origin {
			return "", errs.Configf("the last build %s ran on %s, not on server %q", rec.BuildKey, rec.Origin, s.Server.Alias).
				WithTry("pass --server with the alias of " + rec.Origin)
		}
		b, err := s.Build(ctx, rec.BuildKey)
		if err != nil {
			return "", err
		}
		arg, branch = b.MasterKey, CheckedOutMarker
		if arg == "" {
			arg = b.PlanKey
		}
	}
	ref, err := s.ResolvePlan(ctx, arg, branch)
	if err != nil {
		return "", err
	}
	return ref.PlanKey, nil
}

// BuildOfCommit returns the key of the newest build of planKey made from the
// checked-out commit, waiting up to limit (CommitWaitDefault when 0) for
// Bamboo to start one. note receives a line to show when bam falls back. A
// commit that is not on the remote has no build to wait for: bam says so and
// returns the newest build of the plan.
func (s *Service) BuildOfCommit(ctx context.Context, planKey string, limit time.Duration, note func(string)) (string, error) {
	if s.Head == nil {
		return "", errs.Usagef("bam cannot read the checked-out commit here").WithTry("name a build instead of @")
	}
	sha, pushed, err := s.Head()
	if err != nil {
		return "", err
	}
	short := provider.Revision{Revision: sha}.Short()
	if !pushed {
		note(fmt.Sprintf("commit %s is not pushed, so no build of it will appear: following the newest build of %s", short, planKey))
		builds, err := s.P.ListBuilds(ctx, planKey, provider.ListOptions{Limit: 1, Unfinished: true})
		if err != nil {
			return "", err
		}
		if len(builds) == 0 {
			return "", errs.Bamboof("no builds of %s yet", planKey).WithTry("git push")
		}
		return builds[0].Key, nil
	}
	if limit <= 0 {
		limit = CommitWaitDefault
	}
	deadline := s.Clock.Now().Add(limit)
	announced := false
	for {
		key, err := s.findCommit(ctx, planKey, sha)
		if err != nil || key != "" {
			return key, err
		}
		if !announced {
			note(fmt.Sprintf("waiting for a build of %s on %s", short, planKey))
			announced = true
		}
		if !s.Clock.Now().Before(deadline) {
			return "", errs.New(errs.KindTimeout, fmt.Sprintf("no build of commit %s appeared on %s within %s", short, planKey, limit)).
				WithWhy("Bamboo has not started one: the push may not have reached it, or the plan does not build this branch").
				WithTry("bam watch " + planKey + " to follow the newest build, or bam run to start one")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-s.Clock.After(MinPoll):
		}
	}
}

// findCommit is the newest of planKey's latest builds made from sha, or "".
func (s *Service) findCommit(ctx context.Context, planKey, sha string) (string, error) {
	builds, err := s.P.ListBuilds(ctx, planKey, provider.ListOptions{Limit: commitScan, Unfinished: true})
	if err != nil {
		return "", err
	}
	for _, b := range builds {
		if b.Revision == "" && len(b.Revisions) == 0 {
			if b, err = s.Build(ctx, b.Key); err != nil {
				return "", err
			}
		}
		if builtFrom(b, sha) {
			return b.Key, nil
		}
	}
	return "", nil
}

// builtFrom reports whether any repository of b was at sha.
func builtFrom(b provider.Build, sha string) bool {
	same := func(r string) bool {
		return len(r) >= 7 && len(sha) >= 7 && (strings.HasPrefix(r, sha) || strings.HasPrefix(sha, r))
	}
	if same(b.Revision) {
		return true
	}
	for _, r := range b.Revisions {
		if same(r.Revision) {
			return true
		}
	}
	return false
}
