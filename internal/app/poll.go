package app

import (
	"errors"

	"github.com/rojcarranza/bam/internal/errs"
)

// pollTries is how many failed polls in a row end a watch; one dropped
// connection no longer does.
const pollTries = 3

// pollPhase is where a pollSession stands.
type pollPhase int

const (
	pollFirst    pollPhase = iota // no poll has succeeded yet
	pollLive                      // the last poll succeeded
	pollRetrying                  // the last poll failed; asking again
	pollEnded                     // watching is over
)

// pollSession is the one rule every polling watch (a build, a log follow, a
// set run's build, a tree) uses for a failed poll: the first poll, an error
// that retrying cannot fix (a mistyped key, a bad config, a refused token)
// or pollTries failures in a row end it; any other failure is retried.
type pollSession struct {
	phase pollPhase
	fails int
}

// ok records a successful poll.
func (p *pollSession) ok() {
	if p.phase != pollEnded {
		p.phase, p.fails = pollLive, 0
	}
}

// fail records a failed poll and reports whether to poll again.
func (p *pollSession) fail(err error) bool {
	if p.phase == pollEnded {
		return false
	}
	p.fails++
	if p.phase == pollFirst || permanentPollErr(err) || p.fails >= pollTries {
		p.phase = pollEnded
		return false
	}
	p.phase = pollRetrying
	return true
}

// permanentPollErr reports whether retrying a poll cannot help.
func permanentPollErr(err error) bool {
	switch errs.KindOf(err) {
	case errs.KindConfig, errs.KindAuth, errs.KindUsage:
		return true
	}
	return errors.Is(err, errs.ErrNotFound)
}
