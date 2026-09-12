package gnservice

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/tx"
)

var forbiddenTokens = map[string]bool{
	"ALTER": true, "ANALYZE": true, "ATTACH": true, "CALL": true,
	"COPY": true, "CREATE": true, "DELETE": true, "DETACH": true,
	"DROP": true, "EXEC": true, "EXECUTE": true, "GRANT": true,
	"INSERT": true, "INTO": true, "LOAD": true, "LOCK": true,
	"MERGE": true, "PRAGMA": true, "REINDEX": true, "REPLACE": true,
	"REVOKE": true, "SETVAL": true, "TRUNCATE": true, "UPDATE": true,
	"UPSERT": true, "VACUUM": true,
}

var nativeOrderPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// 已校验的只读原生查询
type NativeStatement struct {
	arguments []any
	query     string
}

// 构造只读原生查询
func NativeSQL(query string, arguments ...any) (NativeStatement, error) {
	tokens, err := scanNativeSQL(query)
	if err != nil {
		return NativeStatement{}, err
	}
	if len(tokens) == 0 || tokens[0] != "SELECT" && tokens[0] != "WITH" {
		return NativeStatement{}, exception.Validate("原生查询只允许 SELECT 或 CTE")
	}
	hasSelect := false
	for _, token := range tokens {
		if token == "SELECT" {
			hasSelect = true
		}
		if forbiddenTokens[token] {
			return NativeStatement{}, exception.Validate(fmt.Sprintf("原生查询包含只读范围外的关键字 %s", token))
		}
	}
	if !hasSelect {
		return NativeStatement{}, exception.Validate("CTE 必须包含 SELECT")
	}

	cloned := make([]any, len(arguments))
	for index, argument := range arguments {
		cloned[index] = cloneData(argument)
	}

	return NativeStatement{arguments: cloned, query: strings.TrimSpace(query)}, nil
}

// 执行只读原生查询
func (base *Base[E, ID]) NativeQuery(
	ctx context.Context,
	statement NativeStatement,
	destination any,
) error {
	if err := base.validate(ctx); err != nil {
		return err
	}
	if statement.query == "" {
		return exception.Validate("原生查询语句无效")
	}
	if isNil(destination) || reflect.TypeOf(destination).Kind() != reflect.Pointer {
		return exception.Validate("原生查询目标必须是非 nil 指针")
	}
	arguments := make([]any, len(statement.arguments))
	for index, argument := range statement.arguments {
		arguments[index] = cloneData(argument)
	}
	transaction, group, exists := tx.Current(ctx)
	if exists {
		if group != base.group {
			return exception.Core(fmt.Sprintf("事务数据库组不匹配: 当前 %s，请求 %s", group, base.group))
		}
		if transaction == nil {
			return exception.Core("当前框架事务无效")
		}
		if err := transaction.Ctx(ctx).GetScan(destination, statement.query, arguments...); err != nil {
			return exception.WrapCore(err, "执行原生只读查询失败")
		}

		return nil
	}
	if err := base.database.GetScan(ctx, destination, statement.query, arguments...); err != nil {
		return exception.WrapCore(err, "执行原生只读查询失败")
	}

	return nil
}

// 执行只读原生 SQL 获得分页数据
func (base *Base[E, ID]) SQLRenderPage(
	ctx context.Context,
	statement NativeStatement,
	query Query,
	destination any,
	autoSort ...bool,
) (Pagination, error) {
	if err := base.validate(ctx); err != nil {
		return Pagination{}, err
	}
	if statement.query == "" {
		return Pagination{}, exception.Validate("原生查询语句无效")
	}
	arguments := make([]any, len(statement.arguments))
	for index, argument := range statement.arguments {
		arguments[index] = cloneData(argument)
	}
	rawSQL := strings.TrimSuffix(statement.query, ";")
	transaction, group, exists := tx.Current(ctx)
	var pageModel *gdb.Model
	if exists {
		if group != base.group {
			return Pagination{}, exception.Core(fmt.Sprintf("事务数据库组不匹配: 当前 %s，请求 %s", group, base.group))
		}
		if transaction == nil {
			return Pagination{}, exception.Core("当前框架事务无效")
		}
		pageModel = transaction.Raw("SELECT * FROM ("+rawSQL+") AS a", arguments...).Ctx(ctx)
	} else {
		pageModel = base.database.Raw("SELECT * FROM ("+rawSQL+") AS a", arguments...).Ctx(ctx)
	}
	if len(autoSort) == 0 || autoSort[0] {
		var err error
		pageModel, err = applyPageOrder(pageModel, query)
		if err != nil {
			return Pagination{}, err
		}
	}

	return renderPage(pageModel, query, destination)
}

func applyPageOrder(model *gdb.Model, query Query) (*gdb.Model, error) {
	orders := []string{"id"}
	directions := []string{"desc"}
	if query.request != nil && (query.request.Has("order") || query.request.Has("sort")) {
		var valid bool
		orders, valid = query.request.Strings("order")
		if !valid {
			return nil, exception.Validate("原生分页排序字段无效")
		}
		directions, valid = query.request.Strings("sort")
		if !valid || len(orders) == 0 || len(orders) != len(directions) {
			return nil, exception.Validate("原生分页排序参数无效")
		}
	}
	for index, order := range orders {
		if !nativeOrderPattern.MatchString(order) {
			return nil, exception.Validate("原生分页排序字段无效")
		}
		direction := strings.ToLower(directions[index])
		if direction != "asc" && direction != "desc" {
			return nil, exception.Validate("原生分页排序方向无效")
		}
		column := model.QuoteWord("a") + "." + model.QuoteWord(order)
		model = model.Order(column, direction)
	}

	return model, nil
}

func scanNativeSQL(query string) ([]string, error) {
	var tokens []string
	for index := 0; index < len(query); {
		switch {
		case isSQLSpace(query[index]):
			index++
		case strings.HasPrefix(query[index:], "--"):
			index = skipLineComment(query, index+2)
		case query[index] == '#':
			index = skipLineComment(query, index+1)
		case strings.HasPrefix(query[index:], "/*"):
			next, err := skipBlockComment(query, index)
			if err != nil {
				return nil, err
			}
			index = next
		case query[index] == '\'' || query[index] == '"' || query[index] == '`':
			next, err := skipQuotedSQL(query, index, query[index])
			if err != nil {
				return nil, err
			}
			index = next
		case query[index] == '[':
			closing := strings.IndexByte(query[index+1:], ']')
			if closing < 0 {
				return nil, exception.Validate("原生查询包含未闭合的标识符")
			}
			index += closing + 2
		case query[index] == '$':
			if next, exists := skipDollarQuote(query, index); exists {
				if next < 0 {
					return nil, exception.Validate("原生查询包含未闭合的字符串")
				}
				index = next
				continue
			}
			index++
		case query[index] == ';':
			if !nativeSQLTailOnly(query[index+1:]) {
				return nil, exception.Validate("原生查询只能包含一条语句")
			}
			return tokens, nil
		case isSQLWordStart(query[index]):
			end := index + 1
			for end < len(query) && isSQLWordPart(query[end]) {
				end++
			}
			tokens = append(tokens, strings.ToUpper(query[index:end]))
			index = end
		default:
			index++
		}
	}

	return tokens, nil
}

func skipLineComment(query string, index int) int {
	for index < len(query) && query[index] != '\n' && query[index] != '\r' {
		index++
	}

	return index
}

func skipBlockComment(query string, index int) (int, error) {
	depth := 1
	for index += 2; index < len(query); {
		switch {
		case strings.HasPrefix(query[index:], "/*"):
			depth++
			index += 2
		case strings.HasPrefix(query[index:], "*/"):
			depth--
			index += 2
			if depth == 0 {
				return index, nil
			}
		default:
			index++
		}
	}

	return 0, exception.Validate("原生查询包含未闭合的注释")
}

func skipQuotedSQL(query string, index int, quote byte) (int, error) {
	for index++; index < len(query); index++ {
		if query[index] == '\\' && quote == '\'' && index+1 < len(query) {
			index++
			continue
		}
		if query[index] != quote {
			continue
		}
		if index+1 < len(query) && query[index+1] == quote {
			index++
			continue
		}

		return index + 1, nil
	}

	return 0, exception.Validate("原生查询包含未闭合的字符串或标识符")
}

func skipDollarQuote(query string, index int) (int, bool) {
	closing := strings.IndexByte(query[index+1:], '$')
	if closing < 0 {
		return 0, false
	}
	tagEnd := index + closing + 2
	tag := query[index:tagEnd]
	for _, character := range tag[1 : len(tag)-1] {
		if character != '_' && (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') && (character < '0' || character > '9') {
			return 0, false
		}
	}
	end := strings.Index(query[tagEnd:], tag)
	if end < 0 {
		return -1, true
	}

	return tagEnd + end + len(tag), true
}

func nativeSQLTailOnly(query string) bool {
	for index := 0; index < len(query); {
		switch {
		case isSQLSpace(query[index]):
			index++
		case strings.HasPrefix(query[index:], "--"):
			index = skipLineComment(query, index+2)
		case query[index] == '#':
			index = skipLineComment(query, index+1)
		case strings.HasPrefix(query[index:], "/*"):
			next, err := skipBlockComment(query, index)
			if err != nil {
				return false
			}
			index = next
		default:
			return false
		}
	}

	return true
}

func isSQLSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	default:
		return false
	}
}

func isSQLWordStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isSQLWordPart(value byte) bool {
	return isSQLWordStart(value) || value >= '0' && value <= '9'
}
