package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// 选择当前环境配置并替换运行时目录占位符
func normalizeConfig(source map[string]json.RawMessage, mode string, baseDir string) (map[string]json.RawMessage, error) {
	if source == nil {
		return nil, fmt.Errorf("插件配置必须是 JSON object")
	}

	selected := source
	hasEnvironment := false
	for key := range source {
		if strings.HasPrefix(key, "@") {
			hasEnvironment = true
			break
		}
	}

	if hasEnvironment {
		keys := envKeys(mode)
		if len(keys) == 0 {
			return nil, fmt.Errorf("插件配置不支持运行模式 %q", mode)
		}

		var found bool
		for _, key := range keys {
			raw, exists := source[key]
			if !exists {
				continue
			}

			var environment map[string]json.RawMessage
			if err := json.Unmarshal(raw, &environment); err != nil {
				return nil, fmt.Errorf("插件环境配置 %s 必须是 JSON object: %w", key, err)
			}
			if environment == nil {
				return nil, fmt.Errorf("插件环境配置 %s 必须是 JSON object", key)
			}
			selected = environment
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("插件配置缺少运行模式 %q 对应的环境对象", mode)
		}
	}

	result := make(map[string]json.RawMessage, len(selected))
	for key, raw := range selected {
		normalized, err := normValue(raw, baseDir)
		if err != nil {
			return nil, fmt.Errorf("插件配置字段 %q 无效: %w", key, err)
		}
		result[key] = normalized
	}
	return result, nil
}

// 使用旧管理员配置顶层覆盖新默认配置
func mergeConfig(defaults, previous map[string]json.RawMessage) map[string]json.RawMessage {
	merged := make(map[string]json.RawMessage, len(defaults)+len(previous))
	for key, raw := range defaults {
		merged[key] = append(json.RawMessage(nil), raw...)
	}
	for key, raw := range previous {
		merged[key] = append(json.RawMessage(nil), raw...)
	}
	return merged
}

// 返回运行模式对应的首选键和兼容键
func envKeys(mode string) []string {
	switch mode {
	case "develop":
		return []string{"@develop", "@local"}
	case "testing":
		return []string{"@testing", "@unittest"}
	case "staging":
		return []string{"@staging"}
	case "product":
		return []string{"@product", "@prod"}
	default:
		return nil
	}
}

// 深拷贝 JSON 值并递归替换字符串占位符
func normValue(raw json.RawMessage, baseDir string) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("包含多个 JSON 值")
		}
		return nil, err
	}

	normalized, err := json.Marshal(replaceBaseDir(value, baseDir))
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

// 递归替换 JSON 字符串中的目录占位符
func replaceBaseDir(value any, baseDir string) any {
	switch current := value.(type) {
	case string:
		return strings.ReplaceAll(current, "@baseDir", baseDir)
	case []any:
		for index := range current {
			current[index] = replaceBaseDir(current[index], baseDir)
		}
	case map[string]any:
		for key := range current {
			current[key] = replaceBaseDir(current[key], baseDir)
		}
	}
	return value
}
