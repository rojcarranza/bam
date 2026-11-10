package view

import (
	"bytes"
	"testing"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintLogsHeadersOnlyForSeveralJobs(t *testing.T) {
	var buf bytes.Buffer
	one := []app.JobLog{{Job: provider.Job{Key: "PROJ-B-INT-4", Name: "Integration", State: provider.StateFailed}, Lines: []string{"a", "b"}}}
	require.NoError(t, PrintLogs(&buf, one))
	assert.Equal(t, "a\nb\n", buf.String())

	buf.Reset()
	two := append(one, app.JobLog{Job: provider.Job{Key: "PROJ-B-UNIT-4", Name: "Unit", State: provider.StateFailed}, Lines: []string{"c"}})
	require.NoError(t, PrintLogs(&buf, two))
	assert.Equal(t, "==> PROJ-B-INT-4  Integration (failed) <==\na\nb\n\n==> PROJ-B-UNIT-4  Unit (failed) <==\nc\n", buf.String())
}

func TestPagerCommand(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	assert.Equal(t, "most", PagerCommand(env(map[string]string{"BAM_PAGER": "most", "PAGER": "more"}), "bat", "linux"))
	assert.Equal(t, "bat", PagerCommand(env(map[string]string{"PAGER": "more"}), "bat", "linux"))
	assert.Equal(t, "more", PagerCommand(env(map[string]string{"PAGER": "more"}), "", "linux"))
	assert.Equal(t, "less -FRX", PagerCommand(env(nil), "", "darwin"))
	assert.Equal(t, "", PagerCommand(env(nil), "", "windows"))
}
