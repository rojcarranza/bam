package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func guideCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// Typing filters on the label and the detail; enter returns the index in the
// list that was passed in, not in the filtered one.
func TestChooseReturnsTheOriginalIndex(t *testing.T) {
	items := []Choice{{"PROJ-BUILD", "Build"}, {"PROJ-DEPLOY", "Deploy to an environment"}, {"OPS-PROV", "Provision"}}
	var out bytes.Buffer
	i, err := Choose(guideCtx(t), strings.NewReader("environ\r"), &out, "plan?", items)
	require.NoError(t, err)
	assert.Equal(t, 1, i)
	assert.NotContains(t, out.String(), "\x1b[?1049h")

	i, err = Choose(guideCtx(t), strings.NewReader("\x1b[B\r"), &bytes.Buffer{}, "plan?", items)
	require.NoError(t, err)
	assert.Equal(t, 1, i, "down moves to the second row")

	_, err = Choose(guideCtx(t), strings.NewReader("\x1b"), &bytes.Buffer{}, "plan?", items)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestChooseViewShowsAnEmptyFilter(t *testing.T) {
	m := newChooser("plan?", []Choice{{"A", "x"}})
	m.list.setQuery("zzz")
	assert.Contains(t, m.View(), "nothing matches")
}

func TestAskReturnsTheEditedText(t *testing.T) {
	got, err := Ask(guideCtx(t), strings.NewReader("-qa\r"), &bytes.Buffer{}, "name?", "deploy")
	require.NoError(t, err)
	assert.Equal(t, "deploy-qa", got)

	_, err = Ask(guideCtx(t), strings.NewReader("\x03"), &bytes.Buffer{}, "name?", "deploy")
	assert.ErrorIs(t, err, context.Canceled)
}
