package errs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNearest(t *testing.T) {
	names := []string{"develop", "feat/foo", "feat/bar", "release"}
	assert.Equal(t, "develop", Nearest("devlop", names))
	assert.Equal(t, "feat/foo", Nearest("feat", names), "ties go to the earlier candidate")
	assert.Empty(t, Nearest("zzzzzz", names))
	assert.Equal(t, "develop", Nearest("devlop", []string{"devops", "develop"}), "nearest first")
	assert.Equal(t, "plan", Nearest("pln", []string{"open", "plan"}), "one edit at most for short words")
	assert.Empty(t, Nearest("del", []string{"set"}), "two edits rewrite a short word")
	assert.Equal(t, "run", Nearest("rnu", []string{"run"}), "a swap is one edit")
	assert.Equal(t, "logs", Nearest("log", []string{"login", "logs"}), "shortest substring match first")
	assert.Empty(t, Nearest("go", []string{"logout"}), "two letters match only as a prefix")
	assert.Equal(t, "logout", Nearest("lo", []string{"logout"}))
	assert.Equal(t, "Abc", Nearest("abd", []string{"Abc", "abe"}), "equal distance keeps the earlier candidate")
}

func TestNearestKey(t *testing.T) {
	m := map[string]int{"work": 1, "home": 2, "worker": 3}
	assert.Equal(t, "work", NearestKey("wrk", m))
	assert.Empty(t, NearestKey("zzz", map[string]int{}))
}
