//go:build windows

package cli

// processAlive cannot tell on Windows, so every lock holder counts as alive
// and --force stays the way out.
func processAlive(int) bool { return true }
