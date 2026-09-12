package crud

import (
	"fmt"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

// 中止无效查询 DSL
func panicCore(format string, arguments ...any) {
	panic(exception.Core(fmt.Sprintf(format, arguments...)))
}
