package gnrecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"

	"github.com/gogf/gf/v2/errors/gerror"
)

const (
	maxOperatorTypeLen  = 32
	maxOperatorIDLen    = 128
	maximumParamsLength = 4096
	truncatedParams     = `{"truncated":true}`
)

// 删除来源输入
type AuditInput struct {
	Source       string
	Params       map[string]any
	OperatorType string
	OperatorID   string
}

// 已脱敏删除来源
type Audit struct {
	source       string
	params       string
	operatorType string
	operatorID   string
}

type auditContextKey struct{}

// 构造已脱敏删除来源
func NewAudit(input AuditInput) (Audit, error) {
	source, err := sanitizeSource(input.Source)
	if err != nil {
		return Audit{}, err
	}
	params, err := sanitizeParams(input.Params)
	if err != nil {
		return Audit{}, err
	}
	operatorType := strings.TrimSpace(input.OperatorType)
	operatorID := strings.TrimSpace(input.OperatorID)
	if len(operatorType) > maxOperatorTypeLen {
		return Audit{}, gerror.New("操作者类型过长")
	}
	if len(operatorID) > maxOperatorIDLen {
		return Audit{}, gerror.New("操作者 ID 过长")
	}

	return Audit{
		source:       source,
		params:       params,
		operatorType: operatorType,
		operatorID:   operatorID,
	}, nil
}

// 将删除来源写入上下文
func WithAudit(ctx context.Context, audit Audit) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	return context.WithValue(ctx, auditContextKey{}, audit)
}

func currentAudit(ctx context.Context) Audit {
	if ctx == nil {
		return Audit{}
	}
	audit, _ := ctx.Value(auditContextKey{}).(Audit)

	return audit
}

func sanitizeSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", nil
	}
	parsed, err := url.ParseRequestURI(source)
	if err != nil {
		return "", gerror.Wrap(err, "删除来源 URL 无效")
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""

	return parsed.String(), nil
}

func sanitizeParams(params map[string]any) (string, error) {
	if params == nil {
		return "", nil
	}
	withoutFiles, _ := removeFileValues(params)
	encoded, err := json.Marshal(withoutFiles)
	if err != nil {
		return "", gerror.Wrap(err, "编码删除参数失败")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err = decoder.Decode(&value); err != nil {
		return "", gerror.Wrap(err, "解析删除参数失败")
	}
	encoded, err = json.Marshal(sanitizeParamValue(value))
	if err != nil {
		return "", gerror.Wrap(err, "编码脱敏删除参数失败")
	}
	if len(encoded) > maximumParamsLength {
		return truncatedParams, nil
	}

	return string(encoded), nil
}

func removeFileValues(value any) (any, bool) {
	if value == nil {
		return nil, true
	}
	valueType := reflect.TypeOf(value)
	for valueType.Kind() == reflect.Pointer {
		valueType = valueType.Elem()
	}
	if isFileType(valueType) {
		return nil, false
	}
	current := reflect.ValueOf(value)
	for current.Kind() == reflect.Interface || current.Kind() == reflect.Pointer {
		if current.IsNil() {
			return nil, true
		}
		current = current.Elem()
	}
	switch current.Kind() {
	case reflect.Map:
		if current.Type().Key().Kind() != reflect.String {
			return value, true
		}
		result := make(map[string]any, current.Len())
		iterator := current.MapRange()
		for iterator.Next() {
			item, keep := removeFileValues(iterator.Value().Interface())
			if keep {
				result[iterator.Key().String()] = item
			}
		}

		return result, true
	case reflect.Slice, reflect.Array:
		result := make([]any, 0, current.Len())
		for index := 0; index < current.Len(); index++ {
			item, keep := removeFileValues(current.Index(index).Interface())
			if keep {
				result = append(result, item)
			}
		}

		return result, true
	default:
		return value, true
	}
}

func isFileType(valueType reflect.Type) bool {
	if valueType == nil {
		return false
	}
	if valueType.PkgPath() == "mime/multipart" && valueType.Name() == "FileHeader" {
		return true
	}
	if valueType.PkgPath() != "github.com/gogf/gf/v2/net/ghttp" {
		return false
	}

	return valueType.Name() == "UploadFile" || valueType.Name() == "UploadFiles"
}

func sanitizeParamValue(value any) any {
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for key, item := range current {
			if isSensitiveKey(key) {
				continue
			}
			result[key] = sanitizeParamValue(item)
		}

		return result
	case []any:
		result := make([]any, len(current))
		for index, item := range current {
			result[index] = sanitizeParamValue(item)
		}

		return result
	default:
		return current
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
	for _, sensitive := range []string{"authorization", "cookie", "password", "passwd", "secret", "token"} {
		if strings.Contains(normalized, sensitive) {
			return true
		}
	}
	for _, fileKey := range []string{"file", "files", "upload", "uploads", "attachment", "attachments"} {
		if normalized == fileKey {
			return true
		}
	}

	return false
}
