package bundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

const SchemaVersion = 1

var (
	keyPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	semverPattern  = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	requiredFields = []string{"schemaVersion", "name", "key", "singleton", "version", "author", "runtime", "config"}
)

// 插件制品清单
type Manifest struct {
	SchemaVersion int                        `json:"schemaVersion"`
	Name          string                     `json:"name"`
	Key           string                     `json:"key"`
	Hook          string                     `json:"hook,omitempty"`
	Singleton     bool                       `json:"singleton"`
	Version       string                     `json:"version"`
	Description   string                     `json:"description,omitempty"`
	Author        string                     `json:"author"`
	Logo          string                     `json:"logo,omitempty"`
	Readme        string                     `json:"readme,omitempty"`
	Runtime       Runtime                    `json:"runtime"`
	Config        map[string]json.RawMessage `json:"config"`
}

// 插件运行时要求
type Runtime struct {
	ABI            string `json:"abi"`
	Module         string `json:"module"`
	MinHostVersion string `json:"minHostVersion"`
}

// 严格解析并校验 Manifest
func ParseManifest(data []byte) (Manifest, error) {
	if !utf8.Valid(data) {
		return Manifest{}, errors.New("plugin.json 必须是合法 UTF-8")
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("解析 plugin.json 失败: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return Manifest{}, err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Manifest{}, fmt.Errorf("读取 plugin.json 字段失败: %w", err)
	}
	for _, field := range requiredFields {
		if _, ok := fields[field]; !ok {
			return Manifest{}, fmt.Errorf("plugin.json 缺少字段 %q", field)
		}
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}

	return manifest, nil
}

// 校验 Manifest 字段
func (manifest Manifest) Validate() error {
	if manifest.SchemaVersion != SchemaVersion {
		return fmt.Errorf("不支持的 plugin.json schemaVersion %d", manifest.SchemaVersion)
	}
	if err := validateText("name", manifest.Name, 100, true); err != nil {
		return err
	}
	if !keyPattern.MatchString(manifest.Key) || manifest.Key == "plugin" {
		return fmt.Errorf("plugin.json key %q 不合法", manifest.Key)
	}
	if manifest.Hook != "" && !keyPattern.MatchString(manifest.Hook) {
		return fmt.Errorf("plugin.json hook %q 不合法", manifest.Hook)
	}
	if _, err := parseVersion(manifest.Version); err != nil {
		return fmt.Errorf("plugin.json version 不合法: %w", err)
	}
	if err := validateText("description", manifest.Description, 500, false); err != nil {
		return err
	}
	if err := validateText("author", manifest.Author, 100, true); err != nil {
		return err
	}
	if manifest.Runtime.ABI != protocol.Name {
		return fmt.Errorf("plugin.json runtime.abi 必须为 %q", protocol.Name)
	}
	if manifest.Runtime.Module != ModuleFile {
		return fmt.Errorf("plugin.json runtime.module 必须为 %q", ModuleFile)
	}
	if _, err := parseVersion(manifest.Runtime.MinHostVersion); err != nil {
		return fmt.Errorf("plugin.json runtime.minHostVersion 不合法: %w", err)
	}
	if manifest.Config == nil {
		return errors.New("plugin.json config 必须是 JSON object")
	}
	for field, value := range map[string]string{"logo": manifest.Logo, "readme": manifest.Readme} {
		if value != "" {
			normalized, err := normalizePath(value)
			if err != nil {
				return fmt.Errorf("plugin.json %s 不合法: %w", field, err)
			}
			if normalized != value {
				return fmt.Errorf("plugin.json %s 必须使用规范路径", field)
			}
		}
	}

	return nil
}

// 检查宿主版本是否满足 Manifest 要求
func (manifest Manifest) CheckHostVersion(hostVersion string) error {
	host, err := parseVersion(hostVersion)
	if err != nil {
		return fmt.Errorf("宿主版本不合法: %w", err)
	}
	minimum, err := parseVersion(manifest.Runtime.MinHostVersion)
	if err != nil {
		return fmt.Errorf("最低宿主版本不合法: %w", err)
	}
	if cmpVersions(host, minimum) < 0 {
		return fmt.Errorf("插件要求宿主版本不低于 %s，当前为 %s", manifest.Runtime.MinHostVersion, hostVersion)
	}

	return nil
}

func validateText(field, value string, maximum int, required bool) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("plugin.json %s 不是合法 UTF-8", field)
	}
	if required && strings.TrimSpace(value) == "" {
		return fmt.Errorf("plugin.json %s 不能为空", field)
	}
	if utf8.RuneCountInString(value) > maximum {
		return fmt.Errorf("plugin.json %s 不能超过 %d 个字符", field, maximum)
	}

	return nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("plugin.json 包含多余 JSON")
		}
		return fmt.Errorf("读取 plugin.json 结尾失败: %w", err)
	}

	return nil
}

type semanticVersion struct {
	major      string
	minor      string
	patch      string
	prerelease []string
}

func parseVersion(value string) (semanticVersion, error) {
	match := semverPattern.FindStringSubmatch(value)
	if match == nil {
		return semanticVersion{}, fmt.Errorf("%q 不是 SemVer", value)
	}
	prerelease := []string(nil)
	if match[4] != "" {
		prerelease = strings.Split(match[4], ".")
		for _, identifier := range prerelease {
			if isNumeric(identifier) && len(identifier) > 1 && identifier[0] == '0' {
				return semanticVersion{}, fmt.Errorf("%q 的预发布数字标识不能有前导零", value)
			}
		}
	}

	return semanticVersion{
		major:      match[1],
		minor:      match[2],
		patch:      match[3],
		prerelease: prerelease,
	}, nil
}

func cmpVersions(left, right semanticVersion) int {
	for _, pair := range [][2]string{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if compared := cmpNumeric(pair[0], pair[1]); compared != 0 {
			return compared
		}
	}
	if len(left.prerelease) == 0 || len(right.prerelease) == 0 {
		switch {
		case len(left.prerelease) == len(right.prerelease):
			return 0
		case len(left.prerelease) == 0:
			return 1
		default:
			return -1
		}
	}
	for index := 0; index < len(left.prerelease) && index < len(right.prerelease); index++ {
		if compared := cmpPrerelease(left.prerelease[index], right.prerelease[index]); compared != 0 {
			return compared
		}
	}

	return compareInts(len(left.prerelease), len(right.prerelease))
}

func cmpPrerelease(left, right string) int {
	leftNumeric, rightNumeric := isNumeric(left), isNumeric(right)
	switch {
	case leftNumeric && rightNumeric:
		return cmpNumeric(left, right)
	case leftNumeric:
		return -1
	case rightNumeric:
		return 1
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func cmpNumeric(left, right string) int {
	if len(left) != len(right) {
		return compareInts(len(left), len(right))
	}
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}

	return 0
}

func compareInts(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func isNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}

	return true
}
