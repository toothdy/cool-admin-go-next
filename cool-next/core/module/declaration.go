package module

import (
	"fmt"
	"go/token"
	"strings"
	"unicode"
)

// 静态组件符号引用
type ComponentRef string

// 创建静态组件符号引用
func Ref(symbol string) ComponentRef {
	return ComponentRef(symbol)
}

// 模块静态声明
type Declaration[T any] struct {
	Name              string         // 展示名称
	Description       string         // 模块说明
	Order             int            // 同层排序值
	Middlewares       []ComponentRef // 模块中间件
	GlobalMiddlewares []ComponentRef // 全局中间件
	Defaults          T              // 默认配置
}

// 校验模块声明
func checkDecl[T any](declaration Declaration[T]) error {
	if err := checkText("名称", declaration.Name); err != nil {
		return err
	}
	if err := checkText("描述", declaration.Description); err != nil {
		return err
	}
	if err := checkRefs("中间件", declaration.Middlewares); err != nil {
		return err
	}

	return checkRefs("全局中间件", declaration.GlobalMiddlewares)
}

// 校验展示文本
func checkText(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("模块%s不能为空", field)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("模块%s不能包含控制字符", field)
		}
	}

	return nil
}

// 校验中间件静态引用
func checkRefs(group string, refs []ComponentRef) error {
	seen := make(map[ComponentRef]struct{}, len(refs))
	for _, ref := range refs {
		if err := checkRef(ref); err != nil {
			return fmt.Errorf("模块%s引用 %q 无效: %w", group, ref, err)
		}
		if _, exists := seen[ref]; exists {
			return fmt.Errorf("模块%s存在重复引用 %q", group, ref)
		}
		seen[ref] = struct{}{}
	}

	return nil
}

// 校验单个静态引用
func checkRef(ref ComponentRef) error {
	value := string(ref)
	if value == "" {
		return fmt.Errorf("引用不能为空")
	}
	for _, segment := range strings.Split(value, ".") {
		if !token.IsIdentifier(segment) {
			return fmt.Errorf("必须是 Go 选择器路径")
		}
	}

	return nil
}
