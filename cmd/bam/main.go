package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rojcarranza/bam/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, os.Args[1:], cli.SystemEnv())
	stop()
	os.Exit(code)
}
