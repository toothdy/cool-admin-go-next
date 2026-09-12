package upload

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/modules/plugin"
)

const (
	uploadMaxBytes    = int64(100 << 20)
	uploadRandomBytes = 16
	uploadTempPrefix  = ".upload-"
	uploadDateLayout  = "20060102"
)

// 本地受管上传文件的位置
type ManagedLocation struct {
	Root         string
	RelativePath string
	Key          string
}

// 本地上传文件存储
type localStore struct {
	root          string
	publicBaseURL string
	publicURL     *url.URL
	maxBytes      int64
	allowedExts   map[string]struct{}
	now           func() time.Time
	random        io.Reader
}

// 按 Plugin 配置创建本地上传实现
func newLocal(config plugin.UploadConfig) (*localStore, error) {
	if config.Root == "" {
		return nil, exception.Core("上传根目录配置无效")
	}
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, exception.Core("上传根目录配置无效")
	}
	publicBaseURL := strings.TrimRight(strings.TrimSpace(config.PublicBaseURL), "/")
	parsedURL, err := url.Parse(publicBaseURL)
	if err != nil || parsedURL.Host == "" ||
		(parsedURL.Scheme != "http" && parsedURL.Scheme != "https") ||
		parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return nil, exception.Core("上传公开地址配置无效")
	}
	maxBytes := config.MaxBytes
	if maxBytes == 0 {
		maxBytes = uploadMaxBytes
	}
	if maxBytes < 0 {
		return nil, exception.Core("上传大小配置无效")
	}
	allowedExts, err := normalizeExts(config.AllowedExtensions)
	if err != nil {
		return nil, err
	}

	return &localStore{
		root:          root,
		publicBaseURL: publicBaseURL,
		publicURL:     parsedURL,
		maxBytes:      maxBytes,
		allowedExts:   allowedExts,
		now:           time.Now,
		random:        cryptorand.Reader,
	}, nil
}

// 解析属于当前本地上传配置的公开 URL
func (store *localStore) resolveManagedURL(rawURL string) (ManagedLocation, bool) {
	if store == nil || store.publicURL == nil || strings.Contains(rawURL, "#") {
		return ManagedLocation{}, false
	}
	candidate, err := url.Parse(rawURL)
	if err != nil || candidate.Opaque != "" || candidate.User != nil || candidate.RawQuery != "" || candidate.ForceQuery ||
		!strings.EqualFold(candidate.Scheme, store.publicURL.Scheme) ||
		!strings.EqualFold(candidate.Host, store.publicURL.Host) {
		return ManagedLocation{}, false
	}
	prefix := strings.TrimRight(store.publicURL.EscapedPath(), "/") + "/upload/"
	remainder, isManaged := strings.CutPrefix(candidate.EscapedPath(), prefix)
	if !isManaged {
		return ManagedLocation{}, false
	}
	escapedDate, escapedName, exists := strings.Cut(remainder, "/")
	if !exists || strings.Contains(escapedName, "/") {
		return ManagedLocation{}, false
	}
	date, dateErr := url.PathUnescape(escapedDate)
	name, nameErr := url.PathUnescape(escapedName)
	if dateErr != nil || nameErr != nil || !validUploadDate(date) || !validUploadBasename(name) {
		return ManagedLocation{}, false
	}

	return ManagedLocation{
		Root:         store.root,
		RelativePath: filepath.Join(date, name),
		Key:          "/upload/" + date + "/" + url.PathEscape(name),
	}, true
}

// 保存 multipart 文件并返回公开 URL
func (store *localStore) save(file *ghttp.UploadFile, key string) (string, error) {
	if store == nil || file == nil || file.FileHeader == nil {
		return "", exception.Validate("上传文件无效")
	}
	if err := store.checkNames(file.Filename, key); err != nil {
		return "", err
	}
	name := key
	if name == "" {
		var err error
		name, err = store.randomName(file.Filename)
		if err != nil {
			return "", exception.Core("生成上传文件名失败")
		}
	}
	if !validUploadBasename(name) {
		return "", exception.Validate("上传文件名无效")
	}
	if file.Size > store.maxBytes {
		return "", exception.Validate("上传文件超过大小限制")
	}

	date := store.now().Format(uploadDateLayout)
	root, err := store.openRoot(true)
	if err != nil {
		return "", exception.Core("保存上传文件失败")
	}
	defer root.Close()
	if err = root.MkdirAll(date, 0o750); err != nil {
		return "", exception.Core("保存上传文件失败")
	}
	directory, err := root.Lstat(date)
	if err != nil || !directory.IsDir() {
		return "", exception.Core("保存上传文件失败")
	}

	source, err := file.Open()
	if err != nil {
		return "", exception.Validate("上传文件无效")
	}
	defer source.Close()

	temporaryName, err := store.randomHex(uploadRandomBytes)
	if err != nil {
		return "", exception.Core("生成上传临时文件名失败")
	}
	temporary := filepath.Join(date, uploadTempPrefix+temporaryName)
	target := filepath.Join(date, name)
	destination, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", exception.Core("保存上传文件失败")
	}
	temporaryExists := true
	defer func() {
		_ = destination.Close()
		if temporaryExists {
			_ = root.Remove(temporary)
		}
	}()

	limit := store.maxBytes + 1
	if store.maxBytes == math.MaxInt64 {
		limit = store.maxBytes
	}
	written, copyErr := io.Copy(destination, io.LimitReader(source, limit))
	if copyErr != nil {
		return "", exception.Core("保存上传文件失败")
	}
	if written > store.maxBytes {
		return "", exception.Validate("上传文件超过大小限制")
	}
	if err = destination.Sync(); err != nil {
		return "", exception.Core("保存上传文件失败")
	}
	if err = destination.Close(); err != nil {
		return "", exception.Core("保存上传文件失败")
	}
	if err = root.Link(temporary, target); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", exception.Validate("上传文件已存在")
		}
		return "", exception.Core("保存上传文件失败")
	}
	if err = root.Remove(temporary); err != nil {
		_ = root.Remove(target)
		return "", exception.Core("保存上传文件失败")
	}
	temporaryExists = false

	return store.publicBaseURL + "/upload/" + date + "/" + url.PathEscape(name), nil
}

func (store *localStore) openRoot(create bool) (*os.Root, error) {
	if create {
		if err := os.MkdirAll(store.root, 0o750); err != nil {
			return nil, err
		}
	}

	return os.OpenRoot(store.root)
}

func (store *localStore) randomName(filename string) (string, error) {
	name, err := store.randomHex(uploadRandomBytes)
	if err != nil {
		return "", err
	}

	return name + safeUploadExtension(filename), nil
}

func normalizeExts(exts []string) (map[string]struct{}, error) {
	if len(exts) == 0 {
		exts = plugin.DefaultUploadExts()
	}
	allowed := make(map[string]struct{}, len(exts))
	for _, extension := range exts {
		normalized := strings.ToLower(strings.TrimSpace(extension))
		if normalized == "" || safeUploadExtension("upload"+normalized) != normalized {
			return nil, exception.Core("上传扩展名配置无效")
		}
		allowed[normalized] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, exception.Core("上传扩展名配置不能为空")
	}

	return allowed, nil
}

func (store *localStore) checkNames(filename, key string) error {
	for _, candidate := range []string{filename, key} {
		if candidate == "" {
			continue
		}
		if _, allowed := store.allowedExts[safeUploadExtension(candidate)]; !allowed {
			return exception.Validate("上传文件类型不支持")
		}
	}

	return nil
}

func (store *localStore) randomHex(size int) (string, error) {
	random := make([]byte, size)
	if _, err := io.ReadFull(store.random, random); err != nil {
		return "", err
	}

	return hex.EncodeToString(random), nil
}

func safeUploadExtension(filename string) string {
	filename = strings.ReplaceAll(filename, `\`, "/")
	extension := path.Ext(path.Base(filename))
	if len(extension) < 2 || len(extension) > 11 {
		return ""
	}
	for _, character := range extension[1:] {
		isLetter := character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z'
		isNumber := character >= '0' && character <= '9'
		if !isLetter && !isNumber {
			return ""
		}
	}

	return strings.ToLower(extension)
}

func validUploadBasename(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsRune(name, 0) && !strings.ContainsAny(name, `/\`) &&
		!filepath.IsAbs(name) && filepath.VolumeName(name) == "" && filepath.Base(name) == name
}

func validUploadDate(value string) bool {
	if len(value) != len(uploadDateLayout) {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	parsed, err := time.Parse(uploadDateLayout, value)

	return err == nil && parsed.Format(uploadDateLayout) == value
}
