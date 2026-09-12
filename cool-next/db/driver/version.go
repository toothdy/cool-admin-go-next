package driver

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/gogf/gf/v2/errors/gerror"
)

var versionPattern = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

// 提取服务器语义版本
func ParseVersion(raw string) (Version, error) {
	matched := versionPattern.FindString(raw)
	if matched == "" {
		return Version{}, gerror.Newf("无法解析数据库版本: %q", raw)
	}

	components := regexp.MustCompile(`\d+`).FindAllString(matched, -1)
	values := [3]int{}
	for index, component := range components {
		value, err := strconv.Atoi(component)
		if err != nil {
			return Version{}, gerror.Wrap(err, "解析数据库版本数字")
		}
		values[index] = value
	}

	return Version{Major: values[0], Minor: values[1], Patch: values[2]}, nil
}

// 校验运行时数据库基线
func ValidateVersion(kind Kind, raw string) (Version, error) {
	if kind == MySQL && strings.Contains(strings.ToLower(raw), "mariadb") {
		return Version{}, gerror.New("数据库产品必须为 MySQL 8.x，不接受 MariaDB")
	}
	version, err := ParseVersion(raw)
	if err != nil {
		return Version{}, err
	}

	supported := false
	switch kind {
	case MySQL:
		supported = version.Major == 8
	case PostgreSQL:
		supported = version.Major > 9 || version.Major == 9 && version.Minor >= 5
	case SQLite:
		supported = version.Major > 3 || version.Major == 3 && version.Minor >= 24
	default:
		return Version{}, gerror.Newf("不支持的数据库类型: %s", kind)
	}
	if !supported {
		return Version{}, gerror.Newf(
			"数据库版本不满足基线: %s %d.%d.%d",
			kind,
			version.Major,
			version.Minor,
			version.Patch,
		)
	}

	return version, nil
}
