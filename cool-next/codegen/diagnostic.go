package codegen

import (
	"fmt"
	"sort"
	"strings"
)

// 源码诊断
type Diagnostic struct {
	Code     string   // 稳定错误码
	Message  string   // 错误说明
	Position Position // 源码位置
}

// 多个源码诊断
type DiagnosticError struct {
	diagnostics []Diagnostic
}

// 返回稳定诊断文本
func (e *DiagnosticError) Error() string {
	if e == nil || len(e.diagnostics) == 0 {
		return ""
	}
	lines := make([]string, len(e.diagnostics))
	for index, diagnostic := range e.diagnostics {
		lines[index] = fmt.Sprintf("%s:%d:%d [%s] %s", diagnostic.Position.File, diagnostic.Position.Line, diagnostic.Position.Column, diagnostic.Code, diagnostic.Message)
	}
	return strings.Join(lines, "\n")
}

// 返回诊断副本
func (e *DiagnosticError) Diagnostics() []Diagnostic {
	if e == nil {
		return nil
	}
	return append([]Diagnostic(nil), e.diagnostics...)
}

// 排序诊断
func sortDiagnostics(diagnostics []Diagnostic) {
	sort.Slice(diagnostics, func(left, right int) bool {
		first, second := diagnostics[left], diagnostics[right]
		if first.Position.File != second.Position.File {
			return first.Position.File < second.Position.File
		}
		if first.Position.Line != second.Position.Line {
			return first.Position.Line < second.Position.Line
		}
		if first.Position.Column != second.Position.Column {
			return first.Position.Column < second.Position.Column
		}
		if first.Code != second.Code {
			return first.Code < second.Code
		}
		return first.Message < second.Message
	})
}
