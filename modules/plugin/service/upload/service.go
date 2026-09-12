package upload

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/toothdy/cool-admin-go-next/cool-next/auth"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnhttp"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/host/manager"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
	"github.com/toothdy/cool-admin-go-next/modules/plugin"
)

const (
	uploadTarget  = "upload"
	uploadMethod  = "upload"
	modeMethod    = "mode"
	pluginTempDir = ".upload-tmp"
)

// 上传接口请求
type Request struct {
	File *ghttp.UploadFile `file:"file" json:"-"`
	Key  string            `form:"key" json:"key"`
}

// 前端上传模式
type ModeResult struct {
	Mode string `json:"mode"`
	Type string `json:"type"`
}

type preparedInvoker interface {
	InvokeJSON(context.Context, string, string, json.RawMessage) (json.RawMessage, error)
	InvokeJSONPrepared(context.Context, string, string, manager.InputPreparer) (json.RawMessage, error)
}

type identity struct {
	source   string
	typeName string
	id       uint64
}

type result struct {
	URL  string `json:"url"`
	Mode string `json:"mode"`
	Type string `json:"type"`
}

type pluginUploadRequest struct {
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	MIME     string `json:"mime"`
	Key      string `json:"key,omitempty"`
}

// WASM 与本地上传统一服务
type Service struct {
	manager  preparedInvoker
	dataRoot string
	local    *localStore
}

var _ gnhttp.StaticFileInstaller = (*Service)(nil)

// 创建上传服务
func New(pluginManager *manager.Manager, config plugin.Config) (*Service, error) {
	return newService(pluginManager, config)
}

func newService(pluginManager preparedInvoker, config plugin.Config) (*Service, error) {
	if pluginManager == nil || strings.TrimSpace(config.DataRoot) == "" {
		return nil, exception.Core("插件上传服务依赖无效")
	}
	dataRoot, err := filepath.Abs(config.DataRoot)
	if err != nil {
		return nil, exception.Core("插件数据根目录无效")
	}
	local, err := newLocal(config.Upload)
	if err != nil {
		return nil, err
	}

	return &Service{manager: pluginManager, dataRoot: dataRoot, local: local}, nil
}

// 上传后台身份的文件
func (service *Service) AdminUpload(ctx context.Context, request *Request) (any, error) {
	adminIdentity, err := auth.Admin(ctx)
	if err != nil {
		return nil, err
	}

	return service.save(ctx, request, identity{source: "admin-upload", typeName: "admin", id: adminIdentity.UserID})
}

// 上传 App 身份的文件
func (service *Service) AppUpload(ctx context.Context, request *Request) (any, error) {
	appIdentity, err := auth.App(ctx)
	if err != nil {
		return nil, err
	}

	return service.save(ctx, request, identity{source: "app-upload", typeName: "app", id: appIdentity.ID})
}

// 返回后台身份的上传模式
func (service *Service) AdminMode(ctx context.Context) (ModeResult, error) {
	adminIdentity, err := auth.Admin(ctx)
	if err != nil {
		return ModeResult{}, err
	}

	return service.mode(ctx, identity{source: "admin-upload-mode", typeName: "admin", id: adminIdentity.UserID})
}

// 返回 App 身份的上传模式
func (service *Service) AppMode(ctx context.Context) (ModeResult, error) {
	appIdentity, err := auth.App(ctx)
	if err != nil {
		return ModeResult{}, err
	}

	return service.mode(ctx, identity{source: "app-upload-mode", typeName: "app", id: appIdentity.ID})
}

// 注册本地上传目录的静态文件服务
func (service *Service) InstallStaticFiles(server *ghttp.Server) error {
	if server == nil {
		return exception.Core("上传静态文件服务未初始化")
	}

	server.AddStaticPath("/upload", service.local.root)
	return nil
}

// 解析属于本地上传配置的公开 URL
func (service *Service) ResolveManagedURL(rawURL string) (ManagedLocation, bool) {
	return service.local.resolveManagedURL(rawURL)
}

func (service *Service) save(ctx context.Context, request *Request, current identity) (any, error) {
	if request == nil {
		return nil, exception.Core("上传接口未初始化")
	}
	if request.File == nil {
		payload, err := service.credentials(ctx, request.Key, current)
		if err != nil {
			return nil, err
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.UseNumber()
		if err = decoder.Decode(&value); err != nil {
			return nil, exception.Core("上传插件凭证无效")
		}

		return value, nil
	}
	uploaded, err := service.upload(ctx, request.File, request.Key, current)
	if err != nil {
		return nil, err
	}

	return uploaded.URL, nil
}

func (service *Service) upload(ctx context.Context, file *ghttp.UploadFile, key string, current identity) (result, error) {
	if file == nil || file.FileHeader == nil {
		return result{}, exception.Validate("上传文件无效")
	}
	if service.local.maxBytes <= 0 || file.Size < 0 || file.Size > service.local.maxBytes {
		return result{}, exception.Validate("上传文件超过大小限制")
	}
	if err := service.local.checkNames(file.Filename, key); err != nil {
		return result{}, err
	}
	if err := validateIdentity(current); err != nil {
		return result{}, exception.Validate("上传身份摘要无效")
	}
	ctx = manager.WithInvocationSource(ctx, current.source, manager.IdentitySummary{Type: current.typeName, ID: current.id})
	payload, err := service.manager.InvokeJSONPrepared(ctx, uploadTarget, uploadMethod, func(metadata manager.GenerationMetadata) (json.RawMessage, func(), error) {
		return service.prepareFile(file, key, service.local.maxBytes, metadata.Key)
	})
	if err != nil {
		if !pluginUnavailable(err) {
			return result{}, err
		}
		url, localErr := service.local.save(file, key)
		if localErr != nil {
			return result{}, localErr
		}

		return result{URL: url, Mode: "local", Type: "local"}, nil
	}

	var uploaded result
	if err = decodeStrict(payload, &uploaded); err != nil {
		return result{}, protocol.WrapError(protocol.ErrorInvalidOutput, "上传插件返回无效", err)
	}
	if err = validateResult(uploaded); err != nil {
		return result{}, protocol.WrapError(protocol.ErrorInvalidOutput, "上传插件返回无效", err)
	}

	return uploaded, nil
}

func (service *Service) credentials(ctx context.Context, key string, current identity) (json.RawMessage, error) {
	if err := validateIdentity(current); err != nil {
		return nil, exception.Validate("上传身份摘要无效")
	}
	ctx = manager.WithInvocationSource(ctx, current.source, manager.IdentitySummary{Type: current.typeName, ID: current.id})
	input := json.RawMessage(`{}`)
	if key != "" {
		encoded, err := json.Marshal(map[string]string{"key": key})
		if err != nil {
			return nil, exception.Core("上传凭证参数无效")
		}
		input = encoded
	}
	payload, err := service.manager.InvokeJSON(ctx, uploadTarget, uploadMethod, input)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, protocol.NewError(protocol.ErrorInvalidOutput, "上传插件凭证无效")
	}

	return append(json.RawMessage(nil), trimmed...), nil
}

func (service *Service) mode(ctx context.Context, current identity) (ModeResult, error) {
	if err := validateIdentity(current); err != nil {
		return ModeResult{}, exception.Validate("上传身份摘要无效")
	}
	ctx = manager.WithInvocationSource(ctx, current.source, manager.IdentitySummary{Type: current.typeName, ID: current.id})
	payload, err := service.manager.InvokeJSON(ctx, uploadTarget, modeMethod, json.RawMessage(`{}`))
	if err != nil {
		if pluginUnavailable(err) {
			return ModeResult{Mode: "local", Type: "local"}, nil
		}

		return ModeResult{}, err
	}
	var value ModeResult
	if err = decodeStrict(payload, &value); err != nil {
		return ModeResult{}, protocol.WrapError(protocol.ErrorInvalidOutput, "上传插件模式无效", err)
	}
	if strings.TrimSpace(value.Mode) == "" || strings.TrimSpace(value.Type) == "" || len(value.Mode) > 64 || len(value.Type) > 64 {
		return ModeResult{}, protocol.NewError(protocol.ErrorInvalidOutput, "上传插件模式无效")
	}

	return value, nil
}

func (service *Service) prepareFile(
	file *ghttp.UploadFile,
	key string,
	maxBytes int64,
	pluginKey string,
) (json.RawMessage, func(), error) {
	if !validPluginKey(pluginKey) {
		return nil, nil, exception.Core("上传插件 key 无效")
	}
	if strings.ContainsRune(file.Filename, 0) || len(file.Filename) > 255 {
		return nil, nil, exception.Validate("上传文件名无效")
	}
	if err := os.MkdirAll(service.dataRoot, 0o700); err != nil {
		return nil, nil, exception.Core("插件上传临时目录不可用")
	}
	dataRoot, err := os.OpenRoot(service.dataRoot)
	if err != nil {
		return nil, nil, exception.Core("插件上传临时目录不可用")
	}
	info, err := dataRoot.Lstat(pluginKey)
	if errors.Is(err, os.ErrNotExist) {
		if err = dataRoot.Mkdir(pluginKey, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			dataRoot.Close()
			return nil, nil, exception.Core("插件上传临时目录不可用")
		}
		info, err = dataRoot.Lstat(pluginKey)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		dataRoot.Close()
		return nil, nil, exception.Core("插件上传临时目录不安全")
	}
	root, err := dataRoot.OpenRoot(pluginKey)
	dataRoot.Close()
	if err != nil {
		return nil, nil, exception.Core("插件上传临时目录不可用")
	}
	if err = root.MkdirAll(pluginTempDir, 0o700); err != nil {
		root.Close()
		return nil, nil, exception.Core("插件上传临时目录不可用")
	}
	info, err = root.Lstat(pluginTempDir)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		root.Close()
		return nil, nil, exception.Core("插件上传临时目录不安全")
	}
	name := randomTemporaryName()
	relative := filepath.Join(pluginTempDir, name)
	destination, err := root.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		root.Close()
		return nil, nil, exception.Core("创建插件上传临时文件失败")
	}
	cleanup := func() {
		_ = destination.Close()
		_ = root.Remove(relative)
		_ = root.Close()
	}

	source, err := file.Open()
	if err != nil {
		return nil, cleanup, exception.Validate("上传文件无效")
	}
	defer source.Close()
	buffer := make([]byte, 512)
	read, readErr := io.ReadFull(source, buffer)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return nil, cleanup, exception.Validate("上传文件无效")
	}
	buffer = buffer[:read]
	contentType := detectContentType(file.Header.Get("Content-Type"), buffer)
	limit := maxBytes + 1
	if maxBytes == math.MaxInt64 {
		limit = maxBytes
	}
	written, err := io.Copy(destination, io.LimitReader(io.MultiReader(bytes.NewReader(buffer), source), limit))
	if err != nil {
		return nil, cleanup, exception.Core("暂存插件上传文件失败")
	}
	if written > maxBytes {
		return nil, cleanup, exception.Validate("上传文件超过大小限制")
	}
	if err = destination.Sync(); err != nil {
		return nil, cleanup, exception.Core("暂存插件上传文件失败")
	}
	if err = destination.Close(); err != nil {
		return nil, cleanup, exception.Core("暂存插件上传文件失败")
	}
	request := pluginUploadRequest{
		Path:     "/data/" + filepath.ToSlash(filepath.Join(pluginTempDir, name)),
		Filename: filepath.Base(file.Filename),
		Size:     written,
		MIME:     contentType,
		Key:      key,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, cleanup, exception.Core("编码插件上传请求失败")
	}

	return payload, cleanup, nil
}

func pluginUnavailable(err error) bool {
	var pluginError *protocol.PluginError
	if !errors.As(err, &pluginError) || pluginError == nil {
		return false
	}

	return pluginError.Code == protocol.ErrorNotFound || pluginError.Code == protocol.ErrorDisabled
}

func decodeStrict(payload json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("响应包含多余 JSON")
		}
		return err
	}

	return nil
}

func validateResult(value result) error {
	if strings.TrimSpace(value.Mode) == "" || strings.TrimSpace(value.Type) == "" || len(value.Mode) > 64 || len(value.Type) > 64 {
		return errors.New("mode 或 type 无效")
	}
	parsed, err := url.Parse(value.URL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("url 无效")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("url 协议无效")
	}

	return nil
}

func detectContentType(header string, sample []byte) string {
	if header != "" && len(header) <= 255 && !strings.ContainsAny(header, "\r\n") {
		if mediaType, _, err := mime.ParseMediaType(header); err == nil && mediaType != "" {
			return mediaType
		}
	}
	if len(sample) == 0 {
		return "application/octet-stream"
	}

	return http.DetectContentType(sample)
}

func randomTemporaryName() string {
	value := make([]byte, 16)
	rand.Read(value)

	return hex.EncodeToString(value)
}

func validPluginKey(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		current := value[index]
		if current >= 'a' && current <= 'z' || current >= '0' && current <= '9' || current == '-' {
			continue
		}
		return false
	}

	return true
}

func validateIdentity(value identity) error {
	if value.source == "" || value.typeName == "" || value.id == 0 {
		return fmt.Errorf("上传身份摘要无效")
	}

	return nil
}
