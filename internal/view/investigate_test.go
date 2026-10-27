package view

import (
	"testing"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
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
		"try: bam build logs PROJ-PLAN-44 --job LINT\n"+
		"try: bam build logs PROJ-PLAN-44 --job UNIT\n"+
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
