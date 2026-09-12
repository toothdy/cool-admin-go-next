package db

import "github.com/toothdy/cool-admin-go-next/cool-next/db/driver"

// 无敏感信息的数据库启动诊断
type Diagnostic struct {
	Group        string              // 框架数据库组
	Kind         driver.Kind         // 数据库方言
	Version      driver.Version      // 数据库版本
	Capabilities driver.Capabilities // 已验证能力
	Tables       []string            // 事务保障表
}

func newDiagnostic(group string, report driver.Report, tables []string) Diagnostic {
	return Diagnostic{
		Group:        group,
		Kind:         report.Dialect.Kind(),
		Version:      report.Version,
		Capabilities: report.Capabilities,
		Tables:       append([]string(nil), tables...),
	}
}

func cloneDiagnostic(diagnostic Diagnostic) Diagnostic {
	diagnostic.Tables = append([]string(nil), diagnostic.Tables...)

	return diagnostic
}
