//go:build !windows

package main

import (
	"syscall"
	"testing"
	"time"
)

// A closed terminal sends SIGHUP; bam must treat it like Ctrl-C so a running
// set records itself interrupted.
func TestHangupEndsTheContext(t *testing.T) {
	ctx, stop := notifyContext()
	defer stop()
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("SIGHUP did not end the context")
	}
}
