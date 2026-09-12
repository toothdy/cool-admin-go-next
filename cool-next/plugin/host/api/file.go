package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

var (
	errInvalidFilePath = errors.New("插件文件路径无效")
	errUnsafeFilePath  = errors.New("插件文件路径不安全")
)

type filePathRequest struct {
	Path string `json:"path"`
}

type fileReadResponse struct {
	Data []byte `json:"data"`
	Size int64  `json:"size"`
}

type fileWriteRequest struct {
	Path string `json:"path"`
	Data []byte `json:"data"`
}

type fileWriteResponse struct {
	Size int64 `json:"size"`
}

type fileDeleteResponse struct {
	Deleted bool `json:"deleted"`
}

type fileListEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

type fileListResponse struct {
	Entries []fileListEntry `json:"entries"`
}

type fileHandler struct {
	dataRoot   string
	maxPayload int64
}

func fileOperations(config Config, _ Dependencies) ([]operation, error) {
	handler, err := newFileHandler(config)
	if err != nil {
		return nil, err
	}

	return []operation{
		{name: "file.read", handle: handler.handleRead},
		{name: "file.write", handle: handler.handleWrite},
		{name: "file.delete", handle: handler.handleDelete},
		{name: "file.list", handle: handler.handleList},
	}, nil
}

func newFileHandler(config Config) (*fileHandler, error) {
	if strings.TrimSpace(config.DataRoot) == "" {
		return nil, errors.New("插件数据根目录不能为空")
	}
	if config.MaxPayloadBytes == 0 {
		return nil, errors.New("宿主调用载荷上限必须大于 0")
	}
	absolute, err := filepath.Abs(config.DataRoot)
	if err != nil {
		return nil, fmt.Errorf("解析插件数据根目录失败: %w", err)
	}
	if err = os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("创建插件数据根目录失败: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("解析插件数据根目录真实路径失败: %w", err)
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return nil, fmt.Errorf("读取插件数据根目录失败: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("插件数据根目录不是目录")
	}

	return &fileHandler{dataRoot: realPath, maxPayload: int64(config.MaxPayloadBytes)}, nil
}

func (handler *fileHandler) handleRead(
	ctx context.Context,
	_ int64,
	payload json.RawMessage,
) (json.RawMessage, error) {
	key, err := pluginKey(ctx)
	if err != nil {
		return nil, err
	}

	return handler.read(ctx, key, payload)
}

func (handler *fileHandler) handleWrite(
	ctx context.Context,
	_ int64,
	payload json.RawMessage,
) (json.RawMessage, error) {
	key, err := pluginKey(ctx)
	if err != nil {
		return nil, err
	}

	return handler.write(ctx, key, payload)
}

func (handler *fileHandler) handleDelete(
	ctx context.Context,
	_ int64,
	payload json.RawMessage,
) (json.RawMessage, error) {
	key, err := pluginKey(ctx)
	if err != nil {
		return nil, err
	}

	return handler.delete(ctx, key, payload)
}

func (handler *fileHandler) handleList(
	ctx context.Context,
	_ int64,
	payload json.RawMessage,
) (json.RawMessage, error) {
	key, err := pluginKey(ctx)
	if err != nil {
		return nil, err
	}

	return handler.list(ctx, key, payload)
}

func (handler *fileHandler) read(
	ctx context.Context,
	pluginKeyValue string,
	payload json.RawMessage,
) (json.RawMessage, error) {
	var request filePathRequest
	if err := handler.decodeRequest(payload, &request); err != nil {
		return nil, fileInputError("文件读取参数无效", err)
	}
	relative, err := normalizeFilePath(request.Path, false)
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrorInvalidInput, "文件读取路径无效", err)
	}
	root, err := handler.openPluginRoot(pluginKeyValue)
	if err != nil {
		return nil, fileError(ctx, "插件数据目录不可用", err)
	}
	defer root.Close()
	info, err := checkFilePath(root, relative)
	if err != nil {
		return nil, fileError(ctx, "文件读取路径无效", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fileError(ctx, "文件读取路径无效", errUnsafeFilePath)
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, fileError(ctx, "读取插件文件失败", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() {
		if err == nil {
			err = errUnsafeFilePath
		}
		return nil, fileError(ctx, "文件读取路径无效", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, handler.maxPayload+1))
	if err != nil {
		return nil, fileError(ctx, "读取插件文件失败", err)
	}
	if int64(len(data)) > handler.maxPayload {
		return nil, protocol.NewError(protocol.ErrorResourceExhausted, "插件文件内容超限")
	}

	return handler.marshalResponse(fileReadResponse{Data: data, Size: int64(len(data))})
}

func (handler *fileHandler) write(
	ctx context.Context,
	pluginKeyValue string,
	payload json.RawMessage,
) (json.RawMessage, error) {
	var request fileWriteRequest
	if err := handler.decodeRequest(payload, &request); err != nil {
		return nil, fileInputError("文件写入参数无效", err)
	}
	if int64(len(request.Data)) > handler.maxPayload {
		return nil, protocol.NewError(protocol.ErrorResourceExhausted, "插件文件内容超限")
	}
	relative, err := normalizeFilePath(request.Path, false)
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrorInvalidInput, "文件写入路径无效", err)
	}
	root, err := handler.openPluginRoot(pluginKeyValue)
	if err != nil {
		return nil, fileError(ctx, "插件数据目录不可用", err)
	}
	defer root.Close()
	if err = ensureFileParents(root, filepath.Dir(relative)); err != nil {
		return nil, fileError(ctx, "文件写入路径无效", err)
	}
	if info, statErr := checkFilePath(root, relative); statErr == nil {
		if !info.Mode().IsRegular() {
			return nil, fileError(ctx, "文件写入路径无效", errUnsafeFilePath)
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return nil, fileError(ctx, "文件写入路径无效", statErr)
	}
	if err = fileContextError(ctx); err != nil {
		return nil, fileError(ctx, "文件写入失败", err)
	}
	temporary, file, err := createRootTemp(root, filepath.Dir(relative))
	if err != nil {
		return nil, fileError(ctx, "创建插件临时文件失败", err)
	}
	isRenamed := false
	defer func() {
		_ = file.Close()
		if !isRenamed {
			_ = root.Remove(temporary)
		}
	}()
	if _, err = file.Write(request.Data); err != nil {
		return nil, fileError(ctx, "写入插件文件失败", err)
	}
	if err = file.Close(); err != nil {
		return nil, fileError(ctx, "关闭插件文件失败", err)
	}
	if err = root.Rename(temporary, relative); err != nil {
		return nil, fileError(ctx, "发布插件文件失败", err)
	}
	isRenamed = true

	return handler.marshalResponse(fileWriteResponse{Size: int64(len(request.Data))})
}

func (handler *fileHandler) delete(
	ctx context.Context,
	pluginKeyValue string,
	payload json.RawMessage,
) (json.RawMessage, error) {
	var request filePathRequest
	if err := handler.decodeRequest(payload, &request); err != nil {
		return nil, fileInputError("文件删除参数无效", err)
	}
	relative, err := normalizeFilePath(request.Path, false)
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrorInvalidInput, "文件删除路径无效", err)
	}
	root, err := handler.openPluginRoot(pluginKeyValue)
	if err != nil {
		return nil, fileError(ctx, "插件数据目录不可用", err)
	}
	defer root.Close()
	info, err := checkFilePath(root, relative)
	if errors.Is(err, fs.ErrNotExist) {
		return handler.marshalResponse(fileDeleteResponse{})
	}
	if err != nil {
		return nil, fileError(ctx, "文件删除路径无效", err)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return nil, fileError(ctx, "文件删除路径无效", errUnsafeFilePath)
	}
	if err = root.Remove(relative); err != nil {
		return nil, fileError(ctx, "删除插件文件失败", err)
	}

	return handler.marshalResponse(fileDeleteResponse{Deleted: true})
}

func (handler *fileHandler) list(
	ctx context.Context,
	pluginKeyValue string,
	payload json.RawMessage,
) (json.RawMessage, error) {
	var request filePathRequest
	if err := handler.decodeRequest(payload, &request); err != nil {
		return nil, fileInputError("文件列表参数无效", err)
	}
	relative, err := normalizeFilePath(request.Path, true)
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrorInvalidInput, "文件列表路径无效", err)
	}
	root, err := handler.openPluginRoot(pluginKeyValue)
	if err != nil {
		return nil, fileError(ctx, "插件数据目录不可用", err)
	}
	defer root.Close()
	info, err := checkFilePath(root, relative)
	if err != nil {
		return nil, fileError(ctx, "文件列表路径无效", err)
	}
	if !info.IsDir() {
		return nil, fileError(ctx, "文件列表路径无效", errUnsafeFilePath)
	}
	directory, err := root.Open(relative)
	if err != nil {
		return nil, fileError(ctx, "读取插件文件列表失败", err)
	}
	defer directory.Close()
	entries := make([]fileListEntry, 0)
	estimatedSize := int64(len(`{"entries":[]}`))
	for {
		if err = fileContextError(ctx); err != nil {
			return nil, fileError(ctx, "读取插件文件列表失败", err)
		}
		batch, readErr := directory.ReadDir(128)
		for _, entry := range batch {
			entryPath := filepath.Join(relative, entry.Name())
			entryInfo, statErr := checkFilePath(root, entryPath)
			if statErr != nil {
				return nil, fileError(ctx, "插件文件列表包含不安全条目", statErr)
			}
			current := fileListEntry{Name: entry.Name(), Size: entryInfo.Size()}
			switch {
			case entryInfo.IsDir():
				current.Type = "directory"
			case entryInfo.Mode().IsRegular():
				current.Type = "file"
			default:
				return nil, fileError(ctx, "插件文件列表包含不安全条目", errUnsafeFilePath)
			}
			encoded, marshalErr := json.Marshal(current)
			if marshalErr != nil {
				return nil, fileError(ctx, "编码插件文件列表失败", marshalErr)
			}
			estimatedSize += int64(len(encoded) + 1)
			if estimatedSize > handler.maxPayload {
				return nil, protocol.NewError(protocol.ErrorResourceExhausted, "插件文件列表超限")
			}
			entries = append(entries, current)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fileError(ctx, "读取插件文件列表失败", readErr)
		}
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Name < entries[right].Name
	})

	return handler.marshalResponse(fileListResponse{Entries: entries})
}

func (handler *fileHandler) openPluginRoot(pluginKeyValue string) (*os.Root, error) {
	if !validFilePluginKey(pluginKeyValue) {
		return nil, errors.New("插件 key 无效")
	}
	dataRoot, err := os.OpenRoot(handler.dataRoot)
	if err != nil {
		return nil, err
	}
	defer dataRoot.Close()
	info, err := dataRoot.Lstat(pluginKeyValue)
	if errors.Is(err, fs.ErrNotExist) {
		if err = dataRoot.Mkdir(pluginKeyValue, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		info, err = dataRoot.Lstat(pluginKeyValue)
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errUnsafeFilePath
	}
	realPath, err := filepath.EvalSymlinks(filepath.Join(handler.dataRoot, pluginKeyValue))
	if err != nil {
		return nil, err
	}
	if !pathContained(handler.dataRoot, realPath) {
		return nil, errUnsafeFilePath
	}

	return dataRoot.OpenRoot(pluginKeyValue)
}

func (handler *fileHandler) decodeRequest(payload json.RawMessage, target any) error {
	if int64(len(payload)) > handler.maxPayload {
		return protocol.NewError(protocol.ErrorResourceExhausted, "插件文件请求载荷超限")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("插件文件参数包含多余 JSON")
		}
		return err
	}

	return nil
}

func (handler *fileHandler) marshalResponse(value any) (json.RawMessage, error) {
	result, err := json.Marshal(value)
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrorHostCallFailed, "编码插件文件响应失败", err)
	}
	if int64(len(result)) > handler.maxPayload {
		return nil, protocol.NewError(protocol.ErrorResourceExhausted, "插件文件响应载荷超限")
	}

	return result, nil
}

func normalizeFilePath(value string, allowRoot bool) (string, error) {
	if value == "" || strings.ContainsRune(value, 0) || strings.Contains(value, `\`) || path.IsAbs(value) {
		return "", errInvalidFilePath
	}
	if len(value) >= 2 && value[1] == ':' {
		return "", errInvalidFilePath
	}
	for _, component := range strings.Split(value, "/") {
		if component == ".." {
			return "", errInvalidFilePath
		}
	}
	cleaned := path.Clean(value)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errInvalidFilePath
	}
	if cleaned == "." && !allowRoot {
		return "", errInvalidFilePath
	}

	return filepath.FromSlash(cleaned), nil
}

func checkFilePath(root *os.Root, relative string) (fs.FileInfo, error) {
	if relative == "." {
		return root.Lstat(relative)
	}
	components := strings.Split(filepath.ToSlash(relative), "/")
	current := ""
	var info fs.FileInfo
	for index, component := range components {
		current = filepath.Join(current, component)
		var err error
		info, err = root.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errUnsafeFilePath
		}
		if index < len(components)-1 && !info.IsDir() {
			return nil, errUnsafeFilePath
		}
		realPath, err := filepath.EvalSymlinks(filepath.Join(root.Name(), current))
		if err != nil {
			return nil, err
		}
		if !pathContained(root.Name(), realPath) {
			return nil, errUnsafeFilePath
		}
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return nil, errUnsafeFilePath
	}

	return info, nil
}

func ensureFileParents(root *os.Root, relative string) error {
	if relative == "." {
		return nil
	}
	components := strings.Split(filepath.ToSlash(relative), "/")
	current := ""
	for _, component := range components {
		current = filepath.Join(current, component)
		info, err := root.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			if err = root.Mkdir(current, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
			info, err = root.Lstat(current)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errUnsafeFilePath
		}
		realPath, err := filepath.EvalSymlinks(filepath.Join(root.Name(), current))
		if err != nil {
			return err
		}
		if !pathContained(root.Name(), realPath) {
			return errUnsafeFilePath
		}
	}

	return nil
}

func createRootTemp(root *os.Root, directory string) (string, *os.File, error) {
	for range 10 {
		random := make([]byte, 12)
		rand.Read(random)
		name := filepath.Join(directory, ".cool-write-"+hex.EncodeToString(random))
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return name, file, err
	}

	return "", nil, errors.New("创建唯一插件临时文件失败")
}

func pathContained(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}

	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validFilePluginKey(value string) bool {
	if len(value) == 0 || len(value) > 64 || value == "plugin" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' {
			continue
		}
		return false
	}

	return true
}

func fileContextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}

	return ctx.Err()
}

func fileInputError(message string, err error) error {
	var pluginError *protocol.PluginError
	if errors.As(err, &pluginError) {
		return err
	}

	return protocol.WrapError(protocol.ErrorInvalidInput, message, err)
}

func fileError(ctx context.Context, message string, err error) error {
	var pluginError *protocol.PluginError
	if errors.As(err, &pluginError) {
		return err
	}
	if errors.Is(err, errInvalidFilePath) || errors.Is(err, errUnsafeFilePath) {
		return protocol.WrapError(protocol.ErrorInvalidInput, message, err)
	}
	if errors.Is(err, context.DeadlineExceeded) || ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return protocol.WrapError(protocol.ErrorTimeout, "插件文件操作超时", err)
	}

	return protocol.WrapError(protocol.ErrorHostCallFailed, message, err)
}
