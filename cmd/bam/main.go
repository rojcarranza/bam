package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rojcarranza/bam/internal/cli"
)

// notifyContext ends when bam is interrupted, terminated or its terminal
// closes (hangup), so a running set records itself interrupted and releases
// its lock instead of staying "running" forever.
func notifyContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
}

func main() {
	ctx, stop := notifyContext()
	code := cli.Execute(ctx, os.Args[1:], cli.SystemEnv())
	stop()
	os.Exit(code)
}
