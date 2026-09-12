package middleware

import (
	"reflect"
	"strings"

	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/toothdy/cool-admin-go-next/modules/base/service"
)

// 菜单响应
type TranslateHandler struct {
	translate *service.TranslateService
}

// 翻译中间件
func NewTranslateHandler(translate *service.TranslateService) *TranslateHandler {
	return &TranslateHandler{translate: translate}
}

// 仅处理菜单接口的 name 字段
func (handler *TranslateHandler) Handle(request *ghttp.Request) {
	if request == nil {
		return
	}
	request.Middleware.Next()
	if handler == nil || handler.translate == nil {
		return
	}
	path := request.URL.Path
	if path != "/admin/base/comm/permmenu" && path != "/admin/base/sys/menu/list" {
		return
	}
	translateNames(request.GetHandlerResponse(), request.GetHeader("language"), handler.translate)
}

func translateNames(value any, language string, translator *service.TranslateService) {
	if value == nil {
		return
	}
	if mapValue, ok := value.(map[string]any); ok {
		for key, child := range mapValue {
			if strings.EqualFold(key, "name") {
				if text, ok := child.(string); ok {
					mapValue[key] = translator.Translate(language, text)
				}
				continue
			}
			translateNames(child, language, translator)
		}
		return
	}
	if sliceValue, ok := value.([]any); ok {
		for _, child := range sliceValue {
			translateNames(child, language, translator)
		}
		return
	}
	reflectValue := reflect.ValueOf(value)
	for reflectValue.Kind() == reflect.Pointer || reflectValue.Kind() == reflect.Interface {
		if reflectValue.IsNil() {
			return
		}
		reflectValue = reflectValue.Elem()
	}
	switch reflectValue.Kind() {
	case reflect.Slice, reflect.Array:
		for index := 0; index < reflectValue.Len(); index++ {
			child := reflectValue.Index(index)
			if child.CanAddr() {
				translateNames(child.Addr().Interface(), language, translator)
				continue
			}
			if child.CanInterface() {
				translateNames(child.Interface(), language, translator)
			}
		}
	case reflect.Map:
		if reflectValue.Type().Key().Kind() != reflect.String {
			return
		}
		for _, key := range reflectValue.MapKeys() {
			if !reflectValue.MapIndex(key).CanInterface() {
				continue
			}
			child := reflectValue.MapIndex(key)
			if strings.EqualFold(key.String(), "name") && child.Kind() == reflect.String {
				translated := reflect.ValueOf(translator.Translate(language, child.String()))
				if translated.Type().AssignableTo(child.Type()) {
					reflectValue.SetMapIndex(key, translated)
				}
				continue
			}
			translateNames(child.Interface(), language, translator)
		}
	case reflect.Struct:
		typeOf := reflectValue.Type()
		for index := 0; index < reflectValue.NumField(); index++ {
			field := reflectValue.Field(index)
			definition := typeOf.Field(index)
			jsonName := strings.Split(definition.Tag.Get("json"), ",")[0]
			if (definition.Name == "Name" || jsonName == "name") && field.Kind() == reflect.String && field.CanSet() {
				field.SetString(translator.Translate(language, field.String()))
				continue
			}
			if field.CanInterface() {
				translateNames(field.Interface(), language, translator)
			}
		}
	}
}
