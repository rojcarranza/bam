package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatching(t *testing.T) {
	type kv struct{ k, v string }
	items := []kv{{"PROJ-API", "Api"}, {"OPS-DEPLOY", "Deploy Prod"}, {"LAB-X", "x"}}
	fields := func(i kv) []string { return []string{i.k, i.v} }

	assert.Equal(t, []kv{{"OPS-DEPLOY", "Deploy Prod"}}, matching(items, "dEpLoY", fields), "case is ignored")
	assert.Equal(t, []kv{{"LAB-X", "x"}}, matching(items, "lab", fields), "the first field matches")
	assert.Equal(t, []kv{{"OPS-DEPLOY", "Deploy Prod"}}, matching(items, "prod", fields), "a later field matches")
	assert.Equal(t, items, matching(items, "", fields), "empty text keeps everything")
	assert.Nil(t, matching(items, "nope", fields), "no match is nil")
}
