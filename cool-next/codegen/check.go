package codegen

import (
	"bytes"
	"context"
	"os"
)

type pipelineExecution struct {
	build    func(context.Context, PipelineOptions) ([]byte, error)
	readFile func(string) ([]byte, error)
	target   func(PipelineOptions) (string, error)
	validate func(context.Context, PipelineOptions, []byte) error
	write    func(string, []byte) error
}

// 生成并原子替换唯一注册表
func Generate(ctx context.Context, options PipelineOptions) error {
	return newExecution().generate(ctx, options)
}

// 检查生成稳定性和已提交文件新鲜度
func Check(ctx context.Context, options PipelineOptions) error {
	return newExecution().check(ctx, options)
}

func newExecution() pipelineExecution {
	return pipelineExecution{
		build:    buildCandidate,
		readFile: os.ReadFile,
		target:   pipelineTarget,
		validate: checkCandidate,
		write:    atomicWriteFile,
	}
}

func (execution pipelineExecution) generate(ctx context.Context, options PipelineOptions) error {
	candidate, err := execution.build(ctx, options)
	if err != nil {
		return err
	}
	if err := execution.validate(ctx, options, candidate); err != nil {
		return err
	}
	target, err := execution.target(options)
	if err != nil {
		return err
	}
	if err := execution.write(target, candidate); err != nil {
		return pipelineStageError(options, "CG093", "原子替换生成文件失败")
	}
	return nil
}

func (execution pipelineExecution) check(ctx context.Context, options PipelineOptions) error {
	first, err := execution.build(ctx, options)
	if err != nil {
		return err
	}
	second, err := execution.build(ctx, options)
	if err != nil {
		return err
	}
	if !bytes.Equal(first, second) {
		return pipelineStageError(options, "CG094", "生成结果不稳定")
	}
	if err := execution.validate(ctx, options, first); err != nil {
		return err
	}
	target, err := execution.target(options)
	if err != nil {
		return err
	}
	committed, err := execution.readFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return pipelineStageError(options, "CG095", "生成文件不存在")
		}
		return pipelineStageError(options, "CG095", "读取已提交生成文件失败")
	}
	if !bytes.Equal(first, committed) {
		return pipelineStageError(options, "CG096", "生成文件已过期")
	}
	return nil
}

func pipelineStageError(options PipelineOptions, code, message string) error {
	paths, err := resolvePaths(options)
	if err != nil {
		return err
	}
	return pipelineError(code, message, paths)
}
