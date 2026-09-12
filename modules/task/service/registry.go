package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

// 任务调用目标
type Callable func(ctx context.Context, arguments []any) (any, error)

// 任务调用目标注册表
type Registry struct {
	callables map[string]Callable
}

// 任务调用注册表，新增任务目标在此登记
func NewRegistry(demo *DemoService) *Registry {

	return &Registry{callables: map[string]Callable{
		"taskDemoService.test": demo.Test,
	}}
}

// 执行任务调用目标
func (registry *Registry) Invoke(ctx context.Context, expression string) (any, error) {
	target, arguments, err := ParseInvocation(expression)
	if err != nil {
		return nil, err
	}
	callable, exists := registry.callables[target]
	if !exists {
		return nil, exception.Comm("任务调用目标不存在: " + target)
	}

	return callable(ctx, arguments)
}

// 解析任务调用字符串
func ParseInvocation(expression string) (string, []any, error) {
	trimmed := strings.TrimSpace(expression)
	open := strings.Index(trimmed, "(")
	if trimmed == "" || open <= 0 || !strings.HasSuffix(trimmed, ")") {
		return "", nil, exception.Validate("任务调用字符串无效: " + expression)
	}
	target := strings.TrimSpace(trimmed[:open])
	if strings.Count(target, ".") != 1 || strings.HasPrefix(target, ".") || strings.HasSuffix(target, ".") {
		return "", nil, exception.Validate("任务调用字符串无效: " + expression)
	}
	segments, err := splitArguments(trimmed[open+1 : len(trimmed)-1])
	if err != nil {
		return "", nil, err
	}
	arguments := make([]any, len(segments))
	for index, segment := range segments {
		var decoded any
		if json.Unmarshal([]byte(segment), &decoded) != nil {
			decoded = segment
		}
		arguments[index] = decoded
	}

	return target, arguments, nil
}

// 按顶层逗号切分参数，括号与引号内的逗号不参与切分
func splitArguments(source string) ([]string, error) {
	if strings.TrimSpace(source) == "" {
		return nil, nil
	}
	var (
		result  []string
		depth   int
		quote   rune
		escaped bool
		current strings.Builder
	)
	for _, character := range source {
		switch {
		case escaped:
			escaped = false
		case quote != 0 && character == '\\':
			escaped = true
		case quote != 0:
			if character == quote {
				quote = 0
			}
		case character == '"' || character == '\'':
			quote = character
		case character == '(' || character == '[' || character == '{':
			depth++
		case character == ')' || character == ']' || character == '}':
			depth--
			if depth < 0 {
				return nil, exception.Validate("任务调用参数括号不匹配")
			}
		case character == ',' && depth == 0:
			result = append(result, strings.TrimSpace(current.String()))
			current.Reset()
			continue
		}
		current.WriteRune(character)
	}
	if depth != 0 || quote != 0 {
		return nil, exception.Validate("任务调用参数括号不匹配")
	}
	result = append(result, strings.TrimSpace(current.String()))
	for _, segment := range result {
		if segment == "" {
			return nil, exception.Validate("任务调用参数不能为空")
		}
	}

	return result, nil
}
