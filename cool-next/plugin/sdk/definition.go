package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

var methodNamePattern = regexp.MustCompile(`^[a-z][A-Za-z0-9]{0,63}$`)

// 原始 JSON 插件方法
type RawHandler func(context.Context, json.RawMessage) (json.RawMessage, error)

// 插件生命周期回调
type LifecycleHandler func(context.Context) error

// 插件定义选项
type Option func(*Definition) error

// 插件方法与生命周期定义
type Definition struct {
	methods  map[string]RawHandler
	ready    LifecycleHandler
	shutdown LifecycleHandler
	valid    bool
}

// 创建插件定义
func Define(options ...Option) Definition {
	definition := Definition{methods: make(map[string]RawHandler)}
	for _, option := range options {
		if option == nil {
			panic("插件定义选项不能为空")
		}
		if err := option(&definition); err != nil {
			panic(err)
		}
	}
	definition.valid = true

	return definition
}

// 注册类型化插件方法
func Method[Request, Response any](name string, handler func(context.Context, Request) (Response, error)) Option {
	if handler == nil {
		return invalidOption("插件方法处理器不能为空")
	}

	return RawMethod(name, func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		var request Request
		if err := json.Unmarshal(input, &request); err != nil {
			return nil, protocol.WrapError(protocol.ErrorInvalidInput, "插件方法参数无效", err)
		}
		response, err := handler(ctx, request)
		if err != nil {
			return nil, err
		}
		result, err := json.Marshal(response)
		if err != nil {
			return nil, protocol.WrapError(protocol.ErrorInvalidOutput, "插件方法响应无效", err)
		}

		return result, nil
	})
}

// 注册原始 JSON 插件方法
func RawMethod(name string, handler RawHandler) Option {
	return func(definition *Definition) error {
		if definition == nil {
			return fmt.Errorf("插件定义不能为空")
		}
		if !methodNamePattern.MatchString(name) {
			return fmt.Errorf("插件方法名无效: %s", name)
		}
		if handler == nil {
			return fmt.Errorf("插件方法 %s 处理器不能为空", name)
		}
		if _, exists := definition.methods[name]; exists {
			return fmt.Errorf("插件方法重复: %s", name)
		}
		definition.methods[name] = handler

		return nil
	}
}

// 注册插件 Ready 回调
func Ready(handler LifecycleHandler) Option {
	return func(definition *Definition) error {
		if definition == nil {
			return fmt.Errorf("插件定义不能为空")
		}
		if handler == nil {
			return fmt.Errorf("插件 Ready 回调不能为空")
		}
		if definition.ready != nil {
			return fmt.Errorf("插件 Ready 回调重复")
		}
		definition.ready = handler

		return nil
	}
}

// 注册插件 Shutdown 回调
func Shutdown(handler LifecycleHandler) Option {
	return func(definition *Definition) error {
		if definition == nil {
			return fmt.Errorf("插件定义不能为空")
		}
		if handler == nil {
			return fmt.Errorf("插件 Shutdown 回调不能为空")
		}
		if definition.shutdown != nil {
			return fmt.Errorf("插件 Shutdown 回调重复")
		}
		definition.shutdown = handler

		return nil
	}
}

func invalidOption(message string) Option {
	return func(*Definition) error {
		return fmt.Errorf("%s", strings.TrimSpace(message))
	}
}

func (definition Definition) clone() Definition {
	result := Definition{
		methods:  make(map[string]RawHandler, len(definition.methods)),
		ready:    definition.ready,
		shutdown: definition.shutdown,
		valid:    definition.valid,
	}
	for name, handler := range definition.methods {
		result.methods[name] = handler
	}

	return result
}
