package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
)

const (
	exitSuccess = 0
	exitFailure = 1
	exitUsage   = 2
)

const helpText = `Cool Go 插件开发工具

用法:
  cool-plugin init --module <Go module> [目录]
  cool-plugin check [目录]
  cool-plugin test [目录]
  cool-plugin build [目录]
  cool-plugin pack [目录]
`

type projectCommands struct {
	initialize func(context.Context, string, string) error
	check      func(context.Context, string, io.Writer, io.Writer) (artifactProject, error)
	test       func(context.Context, string, io.Writer, io.Writer) error
	build      func(context.Context, string, io.Writer, io.Writer) error
	pack       func(context.Context, string, io.Writer, io.Writer) (string, error)
}

func run(ctx context.Context, args []string, cwd string, stdout, stderr io.Writer) int {
	commands := newProjectCommands()

	return runCommands(ctx, args, cwd, stdout, stderr, commands)
}

func runCommands(ctx context.Context, args []string, cwd string, stdout, stderr io.Writer, commands projectCommands) int {
	if len(args) == 1 && isHelp(args[0]) {
		_, _ = io.WriteString(stdout, helpText)
		return exitSuccess
	}
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, helpText)
		return exitUsage
	}

	var err error
	switch args[0] {
	case "init":
		err = runInit(ctx, args[1:], cwd, stderr, commands)
		if err == nil {
			_, _ = io.WriteString(stdout, "初始化完成\n")
		}
	case "check":
		directory, directoryErr := commandDirectory(args[1:], cwd)
		if directoryErr != nil {
			err = directoryErr
			break
		}
		var project artifactProject
		project, err = commands.check(ctx, directory, stdout, stderr)
		if err == nil {
			_, _ = fmt.Fprintf(stdout, "检查通过: %s v%s\n", project.manifest.Key, project.manifest.Version)
		}
	case "test":
		directory, directoryErr := commandDirectory(args[1:], cwd)
		if directoryErr != nil {
			err = directoryErr
			break
		}
		err = commands.test(ctx, directory, stdout, stderr)
		if err == nil {
			_, _ = io.WriteString(stdout, "测试通过\n")
		}
	case "build":
		directory, directoryErr := commandDirectory(args[1:], cwd)
		if directoryErr != nil {
			err = directoryErr
			break
		}
		err = commands.build(ctx, directory, stdout, stderr)
		if err == nil {
			_, _ = io.WriteString(stdout, "构建完成: plugin.wasm\n")
		}
	case "pack":
		directory, directoryErr := commandDirectory(args[1:], cwd)
		if directoryErr != nil {
			err = directoryErr
			break
		}
		var output string
		output, err = commands.pack(ctx, directory, stdout, stderr)
		if err == nil {
			_, _ = fmt.Fprintf(stdout, "打包完成: %s\n", output)
		}
	default:
		_, _ = fmt.Fprintf(stderr, "未知命令 %q\n\n%s", args[0], helpText)
		return exitUsage
	}
	if err != nil {
		if isUsageError(err) {
			_, _ = fmt.Fprintln(stderr, err)
			return exitUsage
		}
		_, _ = fmt.Fprintln(stderr, err)
		return exitFailure
	}

	return exitSuccess
}

func runInit(ctx context.Context, args []string, cwd string, stderr io.Writer, commands projectCommands) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	module := flags.String("module", "", "插件 Go module")
	if err := flags.Parse(args); err != nil {
		return usageError{message: "init 参数无效"}
	}
	if *module == "" {
		return usageError{message: "init 必须提供 --module"}
	}
	if flags.NArg() > 1 {
		return usageError{message: "init 最多接受一个目录参数"}
	}
	directory := cwd
	if flags.NArg() == 1 {
		directory = resolveDirectory(cwd, flags.Arg(0))
	}
	if err := commands.initialize(ctx, directory, *module); err != nil {
		return err
	}

	return nil
}

func commandDirectory(args []string, cwd string) (string, error) {
	if len(args) > 1 {
		return "", usageError{message: "命令最多接受一个目录参数"}
	}
	if len(args) == 0 {
		return cwd, nil
	}

	return resolveDirectory(cwd, args[0]), nil
}

func resolveDirectory(cwd, directory string) string {
	if filepath.IsAbs(directory) {
		return filepath.Clean(directory)
	}

	return filepath.Join(cwd, directory)
}

func isHelp(value string) bool {
	return value == "help" || value == "-h" || value == "--help"
}

type usageError struct {
	message string
}

func (err usageError) Error() string {
	return err.message
}

func isUsageError(err error) bool {
	_, ok := err.(usageError)

	return ok
}
