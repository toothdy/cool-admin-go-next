package driver

import (
	"regexp"

	"github.com/gogf/gf/v2/errors/gerror"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// 构造已校验的方言值
func New(kind Kind) (Dialect, error) {
	if !kind.valid() {
		return Dialect{}, gerror.Newf("不支持的数据库类型: %s", kind)
	}

	return Dialect{kind: kind}, nil
}

// 返回方言分类
func (d Dialect) Kind() Kind {
	return d.kind
}

// 引用已校验的单段标识符
func (d Dialect) Quote(identifier string) (string, error) {
	if !d.kind.valid() {
		return "", gerror.Newf("不支持的数据库类型: %s", d.kind)
	}
	if !identifierPattern.MatchString(identifier) {
		return "", gerror.Newf("数据库标识符无效: %q", identifier)
	}
	if d.kind == MySQL {
		return "`" + identifier + "`", nil
	}

	return `"` + identifier + `"`, nil
}

func (k Kind) valid() bool {
	switch k {
	case MySQL, PostgreSQL, SQLite:
		return true
	default:
		return false
	}
}
