package view

import (
	"bytes"
	"errors"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/stretchr/testify/assert"
)

func TestPrintError(t *testing.T) {
	var buf bytes.Buffer
	err := errs.Bamboof("could not start build of PROJ-BUILD").
		WithWhy(`branch "feat/foo" not found on server "work"`).
		WithTry("bam plan branches PROJ-BUILD").Wrap(errors.New("HTTP 400"))
	PrintError(&buf, err, false)
	assert.Equal(t, "error: could not start build of PROJ-BUILD\n"+
		"  branch \"feat/foo\" not found on server \"work\"\n"+
		"  try: bam plan branches PROJ-BUILD\n", buf.String())

	buf.Reset()
	PrintError(&buf, err, true)
	assert.Contains(t, buf.String(), "  cause: HTTP 400\n")

	buf.Reset()
	PrintError(&buf, errors.New("boom"), false)
	assert.Equal(t, "error: boom\n", buf.String())

	buf.Reset()
	PrintWarnings(&buf, []string{"a", "b"})
	assert.Equal(t, "warning: a\nwarning: b\n", buf.String())
}
