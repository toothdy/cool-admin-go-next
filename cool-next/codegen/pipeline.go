package codegen

import (
	"bytes"
	"context"
	"go/format"
	"os"
	"path/filepath"
)

const (
	pipelineModulesRoot = "modules"
	pipelineOutputFile  = "modules.go"
)

// 生成流水线选项
type PipelineOptions struct {
	Dir string // Go module 根目录
}

type pipelinePaths struct {
	dir    string
	target string
}

// 构建内存候选源码
func buildCandidate(ctx context.Context, options PipelineOptions) ([]byte, error) {
	paths, err := resolvePaths(options)
	if err != nil {
		return nil, err
	}
	model, err := analyzeOverlay(ctx, Options{Dir: paths.dir, ModulesRoot: pipelineModulesRoot}, map[string][]byte{
		paths.target: []byte("package modules\n"),
	})
	if err != nil {
		return nil, err
	}
	descriptors, err := CompileDescriptors(model)
	if err != nil {
		return nil, err
	}
	graph, err := BuildGraphWithDescriptors(model, descriptors)
	if err != nil {
		return nil, err
	}
	source, err := Render(model, graph, descriptors)
	if err != nil {
		return nil, err
	}
	formatted, err := format.Source(source)
	if err != nil {
		return nil, pipelineError("CG091", "格式化生成候选失败: "+err.Error(), paths)
	}
	return bytes.Clone(formatted), nil
}

func pipelineTarget(options PipelineOptions) (string, error) {
	paths, err := resolvePaths(options)
	if err != nil {
		return "", err
	}
	return paths.target, nil
}

func resolvePaths(options PipelineOptions) (pipelinePaths, error) {
	if options.Dir == "" {
		return pipelinePaths{}, pipelineOptionError("工作区目录不能为空")
	}
	dir, err := filepath.Abs(options.Dir)
	if err != nil {
		return pipelinePaths{}, pipelineOptionError("工作区目录无效")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return pipelinePaths{}, pipelineOptionError("工作区目录不存在")
	}
	return pipelinePaths{
		dir:    dir,
		target: filepath.Join(dir, pipelineModulesRoot, pipelineOutputFile),
	}, nil
}

func pipelineOptionError(message string) error {
	return &DiagnosticError{diagnostics: []Diagnostic{{Code: "CG090", Message: message}}}
}

func pipelineError(code, message string, paths pipelinePaths) error {
	position := positionFromPath(paths.dir, paths.target)
	return &DiagnosticError{diagnostics: []Diagnostic{{Code: code, Message: message, Position: position}}}
}
