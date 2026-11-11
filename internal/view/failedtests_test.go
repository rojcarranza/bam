package view

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-11 P3): build show and investigate print each failed test's
// message under its name; secrets in a message are masked everywhere.
func failedBuild() provider.Build {
	return provider.Build{Key: "PROJ-PLAN-44", State: provider.StateFailed,
		FailedTests: []string{"app.AuthTest.TestLogin", "app.AuthTest.TestRefresh"},
		FailedTestDetails: []provider.FailedTest{
			{Name: "app.AuthTest.TestLogin", Message: "expected 200 but was 401\nlogin with password=hunter2 failed"},
			{Name: "app.AuthTest.TestRefresh"},
		}}
}

func TestBuildDetailPrintsFailedTestMessages(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, BuildDetail(o, failedBuild()))
	out := buf.String()
	assert.Contains(t, out, "  failed tests:\n  app.AuthTest.TestLogin\n      expected 200 but was 401\n      login with password=******** failed\n  app.AuthTest.TestRefresh\n")
	assert.NotContains(t, out, "hunter2")
}

func TestInvestigationPrintsFailedTestMessages(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, Investigation(o, app.Investigation{Build: failedBuild()}))
	assert.Contains(t, buf.String(), "Failed tests:\n  app.AuthTest.TestLogin\n      expected 200 but was 401\n")
	assert.NotContains(t, buf.String(), "hunter2")
}

func TestBuildJSONHasFailedTestDetailsBesideNames(t *testing.T) {
	data, err := json.Marshal(BuildJSON(failedBuild()))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	assert.Equal(t, []any{"app.AuthTest.TestLogin", "app.AuthTest.TestRefresh"}, m["failed_tests"])
	d := m["failed_test_details"].([]any)
	require.Len(t, d, 2)
	assert.Equal(t, "app.AuthTest.TestLogin", d[0].(map[string]any)["name"])
	assert.NotContains(t, string(data), "hunter2")
	empty, _ := json.Marshal(BuildJSON(provider.Build{}))
	assert.Contains(t, string(empty), `"failed_test_details":[]`)
}

func TestMaskMessage(t *testing.T) {
	for in, want := range map[string]string{
		`token: abc123 rejected`:               "token: ******** rejected",
		`Authorization: Bearer eyJ.x.y`:        "Authorization: Bearer ********",
		`{"db_password": "s3cret"}`:            `{"db_password": "********"}`,
		`plain assertion failed: got 3 want 4`: `plain assertion failed: got 3 want 4`,
	} {
		assert.Equal(t, want, MaskMessage(in), in)
	}
	assert.True(t, strings.HasSuffix(MaskMessage(strings.Repeat("x", 5000)), "..."))
}
