package service

import "strings"

// 翻译服务
type TranslateService struct {
	translations map[string]map[string]string
}

// 翻译服务
func NewTranslate() *TranslateService {
	return &TranslateService{translations: map[string]map[string]string{
		"en": {
			"系统管理": "System Management",
			"权限管理": "Permission Management",
			"用户列表": "User List",
			"角色列表": "Role List",
			"菜单列表": "Menu List",
			"部门列表": "Department List",
			"请求日志": "Request Logs",
		},
		"zh-tw": {
			"系统管理": "系統管理",
			"权限管理": "權限管理",
			"用户列表": "用戶列表",
			"角色列表": "角色列表",
			"菜单列表": "菜單列表",
			"部门列表": "部門列表",
			"请求日志": "請求日誌",
		},
	}}
}

// 翻译 Base 文本，未知语言或词条返回原文
func (service *TranslateService) Translate(language, text string) string {
	if service == nil || text == "" {
		return text
	}
	language = normalizeLanguage(language)
	if language == "zh-cn" || language == "" {
		return text
	}
	if values := service.translations[language]; values != nil {
		if value := values[text]; value != "" {
			return value
		}
	}

	return text
}

func normalizeLanguage(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	language = strings.ReplaceAll(language, "_", "-")
	if index := strings.IndexByte(language, ','); index >= 0 {
		language = language[:index]
	}
	if index := strings.IndexByte(language, ';'); index >= 0 {
		language = language[:index]
	}
	switch {
	case strings.HasPrefix(language, "zh-tw"), strings.HasPrefix(language, "zh-hk"):
		return "zh-tw"
	case strings.HasPrefix(language, "zh"):
		return "zh-cn"
	case strings.HasPrefix(language, "en"):
		return "en"
	default:
		return language
	}
}
