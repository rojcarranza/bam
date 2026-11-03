package bamboo

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BAM-23: the run form picks a repository branch from Bamboo's list,
// GET plan/{key}/vcsBranches.
func TestListRepoBranchesReadsTheWrappedList(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/plan/PROJ-BUILD/vcsBranches": {fixture: "vcsBranches.json"},
	})
	got, err := c.ListRepoBranches(ctx, "PROJ-BUILD")
	require.NoError(t, err)
	assert.Equal(t, []string{"main", "develop", "release/1.0", "feature/PROJ-100", "feature/PROJ-101", "feature/PROJ-101-extra", "fix/ignore-undotted-config"}, got)
}

func TestListRepoBranchesReadsABareArrayToo(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/plan/PROJ-BUILD/vcsBranches": {fixture: "vcsBranches_array.json"},
	})
	got, err := c.ListRepoBranches(ctx, "PROJ-BUILD")
	require.NoError(t, err)
	assert.Equal(t, []string{"main", "feature/PROJ-101"}, got)
}

// A server without the resource answers 404 or 405: unsupported, so the form
// falls back to typing.
func TestListRepoBranchesOnAServerWithoutTheEndpointIsUnsupported(t *testing.T) {
	for _, status := range []int{404, 405} {
		c, _ := newTestServer(t, map[string]*route{
			"GET /rest/api/latest/plan/PROJ-BUILD/vcsBranches": {status: status, body: `{"message":"no"}`},
		})
		_, err := c.ListRepoBranches(ctx, "PROJ-BUILD")
		require.Error(t, err)
		assert.True(t, errors.Is(err, errs.ErrUnsupported), "status %d", status)
	}
}

// TestRecordedVCSBranchesDecode runs a recording (make record) through the
// same decoding. It skips until one is recorded. BAM-26: recorded from a
// Bamboo 12.1 plan whose repository has 17 branches; every branch Bamboo
// counts in its answer must reach the run form's picker.
func TestRecordedVCSBranchesDecode(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "recorded", "vcsBranches.json"))
	if err != nil {
		t.Skip("recorded/vcsBranches.json not recorded")
	}
	var answer struct {
		Branches struct {
			Size int `json:"size"`
		} `json:"branches"`
	}
	require.NoError(t, json.Unmarshal(raw, &answer))
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/plan/REC-PLAN/vcsBranches": {fixture: "recorded/vcsBranches.json"},
	})
	got, err := c.ListRepoBranches(ctx, "REC-PLAN")
	require.NoError(t, err)
	require.NotZero(t, answer.Branches.Size, "the recording counts its branches")
	assert.Len(t, got, answer.Branches.Size, "every branch Bamboo counted is decoded")
	for _, name := range got {
		assert.NotEmpty(t, name)
	}
}
