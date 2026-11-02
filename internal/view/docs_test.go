package view

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func TestDocsEmptyEncodesAsArray(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, WriteJSON(&buf, Docs([]int(nil), strconv.Itoa)))
	require.Equal(t, "[]\n", buf.String())
}

func TestDocsMapsInOrder(t *testing.T) {
	require.Equal(t, []string{"1", "2", "3"}, Docs([]int{1, 2, 3}, strconv.Itoa))
}

// Nested lists built with Docs stay [] when empty, as the JSON contract needs.
func TestNestedDocsEmptyEncodeAsArrays(t *testing.T) {
	enc := func(v any) string {
		var buf bytes.Buffer
		require.NoError(t, WriteJSON(&buf, v))
		return buf.String()
	}
	b := enc(BuildJSON(provider.Build{Stages: []provider.Stage{{Name: "Build"}}}))
	require.Contains(t, b, `"revisions": []`)
	require.Contains(t, b, `"jobs": []`)
	require.Contains(t, enc(BuildJSON(provider.Build{})), `"stages": []`)

	inv := app.Investigation{Jobs: []app.JobErrors{{Job: provider.Job{Key: "PROJ-PLAN-JOB1-1"}}}}
	require.Contains(t, enc(InvestigationJSON(inv)), `"errors": []`)
	require.Contains(t, enc(InvestigationJSON(app.Investigation{})), `"jobs": []`)

	require.Contains(t, enc(SetJSON(app.SetInfo{})), `"stages": []`)
	require.Contains(t, enc(SetJSON(app.SetInfo{Stages: [][]app.SetRunInfo{{}}})), `"stages": [
    []
  ]`)
	require.Contains(t, enc(SetPlanJSON(app.SetPlan{})), `"stages": []`)
}
