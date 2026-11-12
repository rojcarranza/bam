package app

import (
	"fmt"
	"testing"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// P15 of the BAM-11 plan: a failed watch says why for every failed build of
// the tree, up to five, with secrets masked.
func failedChild(i int) *TreeNode {
	key := fmt.Sprintf("OPS-C%d-9", i)
	job := fmt.Sprintf("OPS-C%d-JOB1-9", i)
	return &TreeNode{PlanKey: fmt.Sprintf("OPS-C%d", i), Build: provider.Build{Key: key, PlanKey: fmt.Sprintf("OPS-C%d", i), State: provider.StateFailed,
		Stages: []provider.Stage{{Name: "Run", Jobs: []provider.Job{{Key: job, State: provider.StateFailed}}}}}}
}

func TestExplainTreeCoversFailedChildOfSuccessfulParent(t *testing.T) {
	p := fakeBamboo()
	p.Logs = map[string][]string{"OPS-C1-JOB1-9": {"x", "Error: BucketAlreadyExists"}}
	child := failedChild(1)
	root := &TreeNode{PlanKey: "OPS-P", Build: provider.Build{Key: "OPS-P-9", State: provider.StateSuccess}, Children: []*TreeNode{child}}
	newService(t, p).ExplainTree(bg, Tree{Root: root})
	assert.False(t, root.Explained)
	assert.True(t, child.Explained)
	assert.Equal(t, "Error: BucketAlreadyExists (job JOB1)", child.Cause)
}

func TestExplainTreeStopsAtFive(t *testing.T) {
	p := fakeBamboo()
	p.Logs = map[string][]string{}
	root := &TreeNode{PlanKey: "OPS-P", Build: provider.Build{Key: "OPS-P-9", State: provider.StateSuccess}}
	for i := 1; i <= 7; i++ {
		p.Logs[fmt.Sprintf("OPS-C%d-JOB1-9", i)] = []string{"Error: boom"}
		root.Children = append(root.Children, failedChild(i))
	}
	newService(t, p).ExplainTree(bg, Tree{Root: root})
	n := 0
	for _, c := range root.Children {
		if c.Explained {
			n++
		}
	}
	assert.Equal(t, ExplainLimit, n)
	assert.False(t, root.Children[5].Explained)
}

func TestExplainTreeMasksSecrets(t *testing.T) {
	p := fakeBamboo()
	p.Logs = map[string][]string{"OPS-C1-JOB1-9": {"Error: login failed with db_password=hunter2 for jdoe"}}
	child := failedChild(1)
	newService(t, p).ExplainTree(bg, Tree{Root: child})
	require.True(t, child.Explained)
	assert.NotContains(t, child.Cause, "hunter2")
	assert.Contains(t, child.Cause, "db_password="+MaskedDisplay)
}

func TestMaskSecretText(t *testing.T) {
	for in, want := range map[string]string{
		`Error: password: s3cr3t failed`: `Error: password: ******** failed`,
		`token "api_secret": "a b" bad`:  `token "api_secret": "********" bad`,
		`Error: secret: Bearer abc123`:   `Error: secret: Bearer ********`,
		`Error: api_key=k1 rejected`:     `Error: api_key=******** rejected`,
		`PGPASSWORD=hunter2 psql -h db`:  `PGPASSWORD=******** psql -h db`,
		`login dbpassword=x failed`:      `login dbpassword=******** failed`,
		`MYSECRET=x`:                     `MYSECRET=********`,
		`appsshkey: x`:                   `appsshkey: ********`,
		`Error: no space left on device`: `Error: no space left on device`,
	} {
		assert.Equal(t, want, MaskSecretText(in), in)
	}
}
