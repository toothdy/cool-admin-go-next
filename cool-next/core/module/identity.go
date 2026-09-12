package module

import (
	"fmt"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"path/filepath"
	"strings"
)

// 模块目录身份
type Identity struct {
	key string
}

// 返回稳定模块键
func (i Identity) Key() string {
	return i.key
}

// 从 modules 根目录下的模块目录创建身份
func NewIdentity(modulesRoot, directory string) (Identity, error) {
	if err := checkDir("modules 根目录", modulesRoot); err != nil {
		return Identity{}, exception.Core(fmt.Sprintf("模块目录无效: %v", err))
	}
	if err := checkDir("模块目录", directory); err != nil {
		return Identity{}, exception.Core(fmt.Sprintf("模块目录无效: %v", err))
	}

	relative, err := filepath.Rel(modulesRoot, directory)
	if err != nil {
		return Identity{}, exception.Core(fmt.Sprintf("模块目录无效: 无法计算相对路径: %v", err))
	}
	if err := checkDir("模块相对目录", relative); err != nil {
		return Identity{}, exception.Core(fmt.Sprintf("模块目录无效: %v", err))
	}

	return Identity{key: filepath.ToSlash(relative)}, nil
}

// 校验相对目录文本
func checkDir(field, directory string) error {
	if directory == "" {
		return fmt.Errorf("%s不能为空", field)
	}
	if filepath.IsAbs(directory) || filepath.VolumeName(directory) != "" {
		return fmt.Errorf("%s必须是相对路径", field)
	}

	normalized := filepath.ToSlash(directory)
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "" {
			return fmt.Errorf("%s不能包含空路径段", field)
		}
		if segment == "." || segment == ".." {
			return fmt.Errorf("%s不能包含 %q", field, segment)
		}
	}

	return nil
}

// 校验模块身份
func checkIdentity(identity Identity) error {
	if err := checkDir("模块身份", identity.key); err != nil {
		return err
	}

	return nil
}
