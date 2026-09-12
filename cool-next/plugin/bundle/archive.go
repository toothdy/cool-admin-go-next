package bundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ManifestFile = "plugin.json"
	ModuleFile   = "plugin.wasm"

	maxPackageBytes   int64  = 32 << 20
	maxUnpackedBytes  uint64 = 64 << 20
	defaultMaxEntries        = 256
)

var archiveTime = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// 插件制品资源限制
type Limits struct {
	MaxPackageBytes  int64
	MaxUnpackedBytes uint64
	MaxEntries       int
}

// 返回默认制品限制
func DefaultLimits() Limits {
	return Limits{
		MaxPackageBytes:  maxPackageBytes,
		MaxUnpackedBytes: maxUnpackedBytes,
		MaxEntries:       defaultMaxEntries,
	}
}

// 已校验的插件制品
type Package struct {
	Manifest Manifest
	Files    map[string][]byte
	SHA256   string
	Size     int64
}

// 以默认限制读取插件制品
func Read(data []byte) (Package, error) {
	return ReadWithLimits(data, DefaultLimits())
}

// 读取并校验插件制品
func ReadWithLimits(data []byte, limits Limits) (Package, error) {
	if err := limits.validate(); err != nil {
		return Package{}, err
	}
	if int64(len(data)) > limits.MaxPackageBytes {
		return Package{}, fmt.Errorf("插件包超过 %d 字节限制", limits.MaxPackageBytes)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Package{}, fmt.Errorf("打开插件包失败: %w", err)
	}
	if len(reader.File) > limits.MaxEntries {
		return Package{}, fmt.Errorf("插件包条目数超过 %d 个限制", limits.MaxEntries)
	}

	files := make(map[string][]byte, len(reader.File))
	seen := make(map[string]struct{}, len(reader.File))
	var unpacked uint64
	for _, entry := range reader.File {
		name, isDirectory, err := normalizeEntryPath(entry.Name)
		if err != nil {
			return Package{}, fmt.Errorf("插件包路径 %q 不合法: %w", entry.Name, err)
		}
		if _, ok := seen[name]; ok {
			return Package{}, fmt.Errorf("插件包包含重复路径 %q", name)
		}
		seen[name] = struct{}{}
		if isDirectory {
			if !entry.FileInfo().IsDir() || entry.UncompressedSize64 != 0 {
				return Package{}, fmt.Errorf("插件包目录 %q 的类型不合法", entry.Name)
			}
			continue
		}
		if entry.Mode()&os.ModeType != 0 {
			return Package{}, fmt.Errorf("插件包条目 %q 不是普通文件", entry.Name)
		}
		if entry.UncompressedSize64 > limits.MaxUnpackedBytes-unpacked {
			return Package{}, fmt.Errorf("插件包解压大小超过 %d 字节限制", limits.MaxUnpackedBytes)
		}
		content, err := readEntry(entry, limits.MaxUnpackedBytes-unpacked)
		if err != nil {
			return Package{}, fmt.Errorf("读取插件包条目 %q 失败: %w", entry.Name, err)
		}
		unpacked += uint64(len(content))
		files[name] = content
	}

	manifest, err := checkPackageFiles(files)
	if err != nil {
		return Package{}, err
	}
	digest := sha256.Sum256(data)

	return Package{
		Manifest: manifest,
		Files:    files,
		SHA256:   hex.EncodeToString(digest[:]),
		Size:     int64(len(data)),
	}, nil
}

// 以默认限制生成可复现插件制品
func Pack(files map[string][]byte) ([]byte, error) {
	return PackWithLimits(files, DefaultLimits())
}

// 生成可复现插件制品
func PackWithLimits(files map[string][]byte, limits Limits) ([]byte, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if len(files) > limits.MaxEntries {
		return nil, fmt.Errorf("插件包条目数超过 %d 个限制", limits.MaxEntries)
	}

	names := make([]string, 0, len(files))
	var unpacked uint64
	for name, content := range files {
		normalized, err := normalizePath(name)
		if err != nil {
			return nil, fmt.Errorf("插件包路径 %q 不合法: %w", name, err)
		}
		if normalized != name {
			return nil, fmt.Errorf("插件包路径 %q 不是规范路径", name)
		}
		if uint64(len(content)) > limits.MaxUnpackedBytes-unpacked {
			return nil, fmt.Errorf("插件包解压大小超过 %d 字节限制", limits.MaxUnpackedBytes)
		}
		unpacked += uint64(len(content))
		names = append(names, name)
	}
	if _, err := checkPackageFiles(files); err != nil {
		return nil, err
	}
	sort.Strings(names)

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		header := &zip.FileHeader{
			Name:     name,
			Method:   zip.Deflate,
			Modified: archiveTime,
		}
		header.SetMode(0o644)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			_ = writer.Close()
			return nil, fmt.Errorf("创建插件包条目 %q 失败: %w", name, err)
		}
		if _, err := entry.Write(files[name]); err != nil {
			_ = writer.Close()
			return nil, fmt.Errorf("写入插件包条目 %q 失败: %w", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("完成插件包失败: %w", err)
	}
	result := buffer.Bytes()
	if _, err := ReadWithLimits(result, limits); err != nil {
		return nil, fmt.Errorf("校验生成的插件包失败: %w", err)
	}

	return append([]byte(nil), result...), nil
}

func (limits Limits) validate() error {
	if limits.MaxPackageBytes <= 0 || limits.MaxUnpackedBytes == 0 || limits.MaxEntries <= 0 {
		return errors.New("插件包限制必须为正数")
	}
	if limits.MaxUnpackedBytes >= math.MaxInt64 {
		return errors.New("插件包解压限制不能超过 int64")
	}

	return nil
}

func checkPackageFiles(files map[string][]byte) (Manifest, error) {
	manifestData, ok := files[ManifestFile]
	if !ok {
		return Manifest{}, fmt.Errorf("插件包缺少 %s", ManifestFile)
	}
	if _, ok := files[ModuleFile]; !ok {
		return Manifest{}, fmt.Errorf("插件包缺少 %s", ModuleFile)
	}
	manifest, err := ParseManifest(manifestData)
	if err != nil {
		return Manifest{}, err
	}
	for field, name := range map[string]string{"logo": manifest.Logo, "readme": manifest.Readme} {
		if name == "" {
			continue
		}
		if _, ok := files[name]; !ok {
			return Manifest{}, fmt.Errorf("plugin.json %s 引用的文件 %q 不存在", field, name)
		}
	}

	return manifest, nil
}

func readEntry(entry *zip.File, maximum uint64) ([]byte, error) {
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	content, err := io.ReadAll(io.LimitReader(reader, int64(maximum)+1))
	if err != nil {
		return nil, err
	}
	if uint64(len(content)) > maximum {
		return nil, errors.New("条目解压大小超过限制")
	}

	return content, nil
}

func normalizeEntryPath(name string) (string, bool, error) {
	isDirectory := strings.HasSuffix(name, "/")
	if isDirectory {
		name = strings.TrimSuffix(name, "/")
	}
	normalized, err := normalizePath(name)
	if err != nil {
		return "", false, err
	}
	if normalized != name {
		return "", false, errors.New("路径不是规范路径")
	}

	return normalized, isDirectory, nil
}

func normalizePath(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '\x00') {
		return "", errors.New("路径不能为空或包含 NUL")
	}
	if !utf8.ValidString(name) {
		return "", errors.New("路径必须是合法 UTF-8")
	}
	if strings.Contains(name, "\\") {
		return "", errors.New("路径不能包含反斜杠")
	}
	if strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", errors.New("路径不能是绝对路径")
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("路径不能跳出插件包")
	}

	return cleaned, nil
}
