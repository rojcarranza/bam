package view

import (
	"testing"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvestigationNoMatchAndRepeats(t *testing.T) {
	o, buf := testOut(false)
	inv := app.Investigation{
		Build: provider.Build{Key: "PROJ-PLAN-44", State: provider.StateFailed},
		Jobs: []app.JobErrors{
			{Job: provider.Job{Name: "Lint"}, Stage: "Check", Short: "LINT"},
			{Job: provider.Job{Name: "Unit"}, Stage: "Check", Short: "UNIT", Errors: []app.ErrorBlock{
				{Line: 3, Text: "panic: boom", Start: 3, Context: []string{"panic: boom"}, Count: 2},
			}},
		},
		LikelyCause: "panic: boom (job UNIT)",
	}
	require.NoError(t, Investigation(o, inv))
	require.Equal(t, "PROJ-PLAN-44  ✗ failed\n"+
		"\nCheck / Lint (LINT)  no known error line\n"+
		"\nCheck / Unit (UNIT)  first error at line 3\n"+
		"  >     3  panic: boom\n"+
		"         (seen 2 times)\n"+
		"\nFailed tests: none reported\n"+
		"Likely cause: panic: boom (job UNIT)\n"+
		"try: bam logs PROJ-PLAN-44 --job LINT\n"+
		"try: bam logs PROJ-PLAN-44 --job UNIT\n"+
		"try: bam build rerun PROJ-PLAN-44\n"+
		"try: bam open PROJ-PLAN-44\n", buf.String())
}

func TestInvestigationFailedTests(t *testing.T) {
	o, buf := testOut(false)
	b := provider.Build{Key: "PROJ-PLAN-44", State: provider.StateFailed, FailedTests: []string{"a", "b", "c", "d", "e", "f", "g"}}
	require.NoError(t, Investigation(o, app.Investigation{Build: b}))
	require.Contains(t, buf.String(), "\nFailed tests: a, b, c, d, e (+2 more)\n")
	b.FailedTests = b.FailedTests[:2]
	buf.Reset()
	require.NoError(t, Investigation(o, app.Investigation{Build: b}))
	require.Contains(t, buf.String(), "\nFailed tests: a, b\n")
}

func TestInvestigationParentTreeHint(t *testing.T) {
	o, buf := testOut(false)
	b := provider.Build{Key: "PROJ-PLAN-44", State: provider.StateFailed, Reason: "Child of PROJ-API-142"}
	require.NoError(t, Investigation(o, app.Investigation{Build: b}))
	require.Contains(t, buf.String(), "try: bam build tree PROJ-API-142\ntry: bam build rerun PROJ-PLAN-44\n")
	b.Reason, b.Parent = "Rebuilt by jdoe", "PROJ-API-140"
	buf.Reset()
	require.NoError(t, Investigation(o, app.Investigation{Build: b}))
	require.Contains(t, buf.String(), "try: bam build tree PROJ-API-140\n")
	b.Parent = ""
	buf.Reset()
	require.NoError(t, Investigation(o, app.Investigation{Build: b}))
	require.NotContains(t, buf.String(), "bam build tree")
}

func TestChangesLine(t *testing.T) {
	since := provider.Build{Key: "PROJ-PLAN-41"}
	assert.Equal(t, "Changed since last success: no earlier successful build\n", changesLine(app.Comparison{}))
	assert.Equal(t, "Changed since last success (PROJ-PLAN-41): app a1b2c3d (was 9f8e7d6), old (removed, was x), var NEW=y (new)\n",
		changesLine(app.Comparison{Since: since,
			Revisions: []app.Change{{Name: "app", Was: "9f8e7d6aaaa", Now: "a1b2c3dbbbb"}, {Name: "old", Was: "x"}},
			Variables: []app.Change{{Name: "NEW", Now: "y"}}}))
	assert.Equal(t, "Changed since last success (PROJ-PLAN-41): no revision or variable changed, variables not readable on this server\n",
		changesLine(app.Comparison{Since: since}))
}

func TestInvestigationTruncatedLog(t *testing.T) {
	o, buf := testOut(false)
	inv := app.Investigation{
		Build: provider.Build{Key: "PROJ-PLAN-44", State: provider.StateFailed},
		Jobs:  []app.JobErrors{{Job: provider.Job{Name: "Lint"}, Stage: "Check", Short: "LINT", Skipped: 1000}},
	}
	require.NoError(t, Investigation(o, inv))
	require.Contains(t, buf.String(), "\nCheck / Lint (LINT)  no known error line\n  log truncated: the first 1000 lines were not read\n")
	require.Equal(t, 1000, InvestigationJSON(inv).Jobs[0].SkippedLines)
}

func TestInvestigationChildTreeHint(t *testing.T) {
	o, buf := testOut(false)
	b := provider.Build{Key: "PROJ-API-142", State: provider.StateFailed}
	require.NoError(t, Investigation(o, app.Investigation{Build: b, Children: []string{"OPS-DEPLOY"}}))
	require.Contains(t, buf.String(), "try: bam build tree PROJ-API-142\ntry: bam build rerun PROJ-API-142\n")
}

func TestInvestigationOverlappingBlocksPrintEachLineOnce(t *testing.T) {
	o, buf := testOut(false)
	ctx := []string{"start", "Error: timeout", "Failing task since return code of [terraform] was 1"}
	inv := app.Investigation{
		Build: provider.Build{Key: "PROJ-PLAN-44", State: provider.StateFailed},
		Jobs: []app.JobErrors{{Job: provider.Job{Name: "Apply"}, Stage: "Deploy", Short: "APPLY", Errors: []app.ErrorBlock{
			{Line: 2, Text: ctx[1], Start: 1, Context: ctx, Count: 1},
			{Line: 3, Text: ctx[2], Start: 2, Context: ctx[1:], Count: 1},
		}}},
	}
	require.NoError(t, Investigation(o, inv))
	assert.Contains(t, buf.String(), "first error at line 2\n"+
		"        1  start\n"+
		"  >     2  Error: timeout\n"+
		"  >     3  Failing task since return code of [terraform] was 1\n\n")
}
