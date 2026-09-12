package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/codegen"
)

const (
	exitSuccess = 0
	exitFailure = 1
	exitUsage   = 2
)

const helpText = `Cool 代码生成工具

	用法:
	  cool generate
	  cool check
	  cool build
	  cool run
	  cool outbox <list|show|replay>
`

type commandFunc func(context.Context, codegen.PipelineOptions) error
type workspaceCommand func(context.Context, string, io.Writer, io.Writer) error

type commandSet struct {
	generate commandFunc
	check    commandFunc
	build    workspaceCommand
	run      workspaceCommand
	outbox   outboxCommand
}

func run(ctx context.Context, args []string, cwd string, stdout, stderr io.Writer) int {
	return runCommands(ctx, args, cwd, stdout, stderr, commandSet{
		generate: codegen.Generate,
		check:    codegen.Check,
		build:    buildApplication,
		run:      runApplication,
		outbox:   runOutbox,
	})
}

func runCommands(
	ctx context.Context,
	args []string,
	cwd string,
	stdout io.Writer,
	stderr io.Writer,
	commands commandSet,
) int {
	if len(args) == 1 && isHelp(args[0]) {
		_, _ = io.WriteString(stdout, helpText)
		return exitSuccess
	}
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, helpText)
		return exitUsage
	}
	if args[0] == "outbox" {
		if commands.outbox == nil {
			_, _ = io.WriteString(stderr, "Outbox 命令未配置\n")
			return exitFailure
		}

		return commands.outbox(ctx, args[1:], cwd, stdout, stderr)
	}
	if len(args) > 1 {
		_, _ = fmt.Fprintf(stderr, "命令 %q 不接受额外参数\n\n%s", args[0], helpText)
		return exitUsage
	}

	var (
		execute func() error
		summary string
	)
	options := codegen.PipelineOptions{Dir: cwd}
	switch args[0] {
	case "generate":
		execute = func() error { return commands.generate(ctx, options) }
		summary = "生成完成\n"
	case "check":
		execute = func() error { return commands.check(ctx, options) }
		summary = "检查通过\n"
	case "build":
		execute = func() error {
			if err := commands.check(ctx, options); err != nil {
				return err
			}
			return commands.build(ctx, cwd, stdout, stderr)
		}
		summary = "构建完成\n"
	case "run":
		execute = func() error {
			if err := commands.check(ctx, options); err != nil {
				return err
			}
			return commands.run(ctx, cwd, stdout, stderr)
		}
	default:
		_, _ = fmt.Fprintf(stderr, "未知命令 %q\n\n%s", args[0], helpText)
		return exitUsage
	}

	if err := execute(); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitFailure
	}
	_, _ = io.WriteString(stdout, summary)
	return exitSuccess
}

func buildApplication(ctx context.Context, cwd string, stdout, stderr io.Writer) error {
	if err := os.MkdirAll(filepath.Join(cwd, "bin"), 0o755); err != nil {
		return fmt.Errorf("创建构建目录失败: %w", err)
	}

	return runGoCommand(ctx, cwd, stdout, stderr, "build", "-buildvcs=false", "-o", filepath.Join("bin", "cool-admin-go-next"), ".")
}

func runApplication(ctx context.Context, cwd string, stdout, stderr io.Writer) error {
	err := runGoCommand(ctx, cwd, stdout, stderr, "run", "-buildvcs=false", ".")
	if ctx.Err() != nil {
		return nil
	}

	return err
}

func runGoCommand(ctx context.Context, cwd string, stdout, stderr io.Writer, args ...string) error {
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = cwd
	command.Stdout = stdout
	command.Stderr = stderr
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if err := command.Process.Signal(os.Interrupt); err != nil {
			return err
		}
		return os.ErrProcessDone
	}
	command.WaitDelay = 5 * time.Second
	if err := command.Run(); err != nil {
		return fmt.Errorf("go %s 失败: %w", args[0], err)
	}

	return nil
}

func isHelp(arg string) bool {
	return arg == "help" || arg == "-h" || arg == "--help"
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	cwd, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "获取工作区目录失败: %v\n", err)
		stop()
		os.Exit(exitFailure)
	}
	exitCode := run(ctx, os.Args[1:], cwd, os.Stdout, os.Stderr)
	stop()
	os.Exit(exitCode)
}
