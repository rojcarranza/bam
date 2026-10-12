package view

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgressLineRewritesOnlyAChangedText(t *testing.T) {
	var b bytes.Buffer
	l := NewProgressLine(&b, 80)
	l.Show("reading builds 1/4 plans")
	l.Show("reading builds 1/4 plans")
	l.Show("reading builds 2/4 plans")
	l.Clear()
	l.Clear()
	require.Equal(t, "\r\x1b[Kreading builds 1/4 plans\r\x1b[Kreading builds 2/4 plans\r\x1b[K", b.String())
}

func TestProgressLineNeverWraps(t *testing.T) {
	var b bytes.Buffer
	NewProgressLine(&b, 12).Show("reading builds 12/40 plans")
	require.Equal(t, "\r\x1b[Kreading bu…", b.String(), "cut to one column short of the width")
}

func TestProgressLineClearIsANoOpWhenNothingShows(t *testing.T) {
	var b bytes.Buffer
	NewProgressLine(&b, 80).Clear()
	require.Empty(t, b.String())
	var nilLine *ProgressLine
	nilLine.Show("x")
	nilLine.Clear()
}
