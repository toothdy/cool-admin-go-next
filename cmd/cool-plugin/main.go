package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	cwd, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "获取工作目录失败: %v\n", err)
		stop()
		os.Exit(exitFailure)
	}
	exitCode := run(ctx, os.Args[1:], cwd, os.Stdout, os.Stderr)
	stop()
	os.Exit(exitCode)
}
