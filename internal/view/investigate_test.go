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
		"\nLikely cause: panic: boom (job UNIT)\n"+
		"try: bam build logs PROJ-PLAN-44 --job LINT\n"+
		"try: bam build logs PROJ-PLAN-44 --job UNIT\n", buf.String())
}
