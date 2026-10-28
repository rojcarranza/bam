package errs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClosest(t *testing.T) {
	names := []string{"develop", "feat/foo", "feat/bar", "release"}
	assert.Equal(t, []string{"develop"}, Closest("devlop", names, 3))
	assert.Equal(t, []string{"feat/foo", "feat/bar"}, Closest("feat", names, 3))
	assert.Empty(t, Closest("zzzzzz", names, 3))
	assert.Equal(t, []string{"develop", "devops"}, Closest("devlop", []string{"devops", "develop"}, 2), "nearest first")
	assert.Equal(t, []string{"plan"}, Closest("pln", []string{"open", "plan"}, 2), "one edit at most for short words")
	assert.Empty(t, Closest("del", []string{"set"}, 1), "two edits rewrite a short word")
	assert.Equal(t, []string{"run"}, Closest("rnu", []string{"run"}, 1), "a swap is one edit")
	assert.Equal(t, []string{"logs"}, Closest("log", []string{"login", "logs"}, 1), "shortest substring match first")
	assert.Empty(t, Closest("go", []string{"logout"}, 1), "two letters match only as a prefix")
	assert.Equal(t, []string{"logout"}, Closest("lo", []string{"logout"}, 1))
}
