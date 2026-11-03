package cli

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// Piece 5 (SCR-54): o falls back to the Windows browser on WSL when no Linux
// opener is installed.
func TestOpenURLFallsBackToWindowsOnWSL(t *testing.T) {
	noLinux := func(string) error { return errors.New("no opener") }
	url := "https://bamboo.example.com/browse/PROJ-API-142"

	t.Run("WSL without a Linux opener uses Windows", func(t *testing.T) {
		var got string
		err := openURL(url, noLinux, true, func(u string) error { got = u; return nil })
		require.NoError(t, err)
		require.Equal(t, url, got)
	})
	t.Run("a working Linux opener is used alone", func(t *testing.T) {
		called := false
		err := openURL(url, func(string) error { return nil }, true, func(string) error { called = true; return nil })
		require.NoError(t, err)
		require.False(t, called)
	})
	t.Run("outside WSL the Linux error stands and Windows is not tried", func(t *testing.T) {
		called := false
		err := openURL(url, noLinux, false, func(string) error { called = true; return nil })
		require.EqualError(t, err, "no opener")
		require.False(t, called)
	})
	t.Run("both failing reports both", func(t *testing.T) {
		err := openURL(url, noLinux, true, func(string) error { return errors.New("no wslview") })
		require.ErrorContains(t, err, "no opener")
		require.ErrorContains(t, err, "no wslview")
	})
}
