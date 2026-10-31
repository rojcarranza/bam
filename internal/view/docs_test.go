package view

import (
	"bytes"
	"strconv"
	"testing"

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
