package codegen

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// 原子提交所需的临时文件能力
type atomicFile interface {
	Write([]byte) (int, error)
	Chmod(os.FileMode) error
	Sync() error
	Close() error
	Name() string
}

// 单次提交使用的文件系统操作
type atomicFileOps struct {
	createTemp func(string, string) (atomicFile, error)
	rename     func(string, string) error
	remove     func(string) error
}

// 创建真实文件操作集
func newAtomicFileOps() atomicFileOps {
	return atomicFileOps{
		createTemp: func(directory, pattern string) (atomicFile, error) {
			return os.CreateTemp(directory, pattern)
		},
		rename: os.Rename,
		remove: os.Remove,
	}
}

// 原子替换目标文件
func atomicWriteFile(target string, data []byte) error {
	return writeFileWithOps(target, data, newAtomicFileOps())
}

// 使用指定文件系统操作提交内容
func writeFileWithOps(target string, data []byte, ops atomicFileOps) error {
	directory := filepath.Dir(target)
	file, err := ops.createTemp(directory, "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	temporaryPath := file.Name()
	cleanup := func(shouldClose bool) {
		if shouldClose {
			_ = file.Close()
		}
		_ = ops.remove(temporaryPath)
	}

	written, err := file.Write(data)
	if err != nil {
		cleanup(true)
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if written != len(data) {
		cleanup(true)
		return fmt.Errorf("写入临时文件失败: %w", io.ErrShortWrite)
	}
	if err := file.Chmod(0o644); err != nil {
		cleanup(true)
		return fmt.Errorf("设置临时文件权限失败: %w", err)
	}
	if err := file.Sync(); err != nil {
		cleanup(true)
		return fmt.Errorf("同步临时文件失败: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup(true)
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := ops.rename(temporaryPath, target); err != nil {
		cleanup(false)
		return fmt.Errorf("替换目标文件失败: %w", err)
	}
	return nil
}
