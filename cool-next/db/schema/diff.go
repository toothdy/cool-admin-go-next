package schema

import (
	"fmt"
	"slices"
	"strings"
)

// 返回稳定排序的结构差异
func compare(expected Table, actual Table) []Difference {
	if actual.Name == "" {
		return []Difference{{
			Table: expected.Name, Kind: "table", Expected: "存在", Actual: "缺失", Safe: true,
		}}
	}

	actualColumns := make(map[string]Column, len(actual.Columns))
	for _, column := range actual.Columns {
		actualColumns[column.Name] = column
	}
	differences := make([]Difference, 0)
	for _, column := range expected.Columns {
		current, exists := actualColumns[column.Name]
		if !exists {
			differences = append(differences, Difference{
				Table: expected.Name, Subject: column.Name, Kind: "column", Expected: "存在", Actual: "缺失",
				Safe: column.Nullable,
			})
			continue
		}
		if column.Type != current.Type {
			differences = append(differences, Difference{
				Table: expected.Name, Subject: column.Name, Kind: "type", Expected: column.Type, Actual: current.Type,
			})
		}
		if column.Nullable != current.Nullable {
			differences = append(differences, Difference{
				Table: expected.Name, Subject: column.Name, Kind: "nullable",
				Expected: fmt.Sprintf("%t", column.Nullable), Actual: fmt.Sprintf("%t", current.Nullable),
			})
		}
		if column.Primary != current.Primary {
			differences = append(differences, Difference{
				Table: expected.Name, Subject: column.Name, Kind: "primary",
				Expected: fmt.Sprintf("%t", column.Primary), Actual: fmt.Sprintf("%t", current.Primary),
			})
		}
		if column.AutoIncrement && !current.AutoIncrement {
			differences = append(differences, Difference{
				Table: expected.Name, Subject: column.Name, Kind: "autoIncrement", Expected: "true", Actual: "false",
			})
		}
	}
	for _, column := range actual.Columns {
		if !slices.ContainsFunc(expected.Columns, func(expected Column) bool { return expected.Name == column.Name }) {
			differences = append(differences, Difference{
				Table: expected.Name, Subject: column.Name, Kind: "column", Expected: "缺失", Actual: "存在",
			})
		}
	}

	actualIndexes := make(map[string]Index, len(actual.Indexes))
	for _, index := range actual.Indexes {
		actualIndexes[index.Name] = index
	}
	for _, index := range expected.Indexes {
		current, exists := actualIndexes[index.Name]
		if !exists {
			differences = append(differences, Difference{
				Table: expected.Name, Subject: index.Name, Kind: "index", Expected: "存在", Actual: "缺失", Safe: true,
			})
			continue
		}
		if index.Unique != current.Unique || !slices.Equal(index.Fields, current.Fields) {
			differences = append(differences, Difference{
				Table: expected.Name, Subject: index.Name, Kind: "index",
				Expected: formatIndex(index), Actual: formatIndex(current),
			})
		}
	}
	for _, index := range actual.Indexes {
		if index.Name != "PRIMARY" && !slices.ContainsFunc(expected.Indexes, func(expected Index) bool { return expected.Name == index.Name }) {
			differences = append(differences, Difference{
				Table: expected.Name, Subject: index.Name, Kind: "index", Expected: "缺失", Actual: "存在",
			})
		}
	}
	slices.SortFunc(differences, func(left, right Difference) int {
		return strings.Compare(left.Kind+"\x00"+left.Subject, right.Kind+"\x00"+right.Subject)
	})

	return differences
}

func formatIndex(index Index) string {
	prefix := "index"
	if index.Unique {
		prefix = "unique"
	}
	return prefix + "(" + strings.Join(index.Fields, ",") + ")"
}

// 返回可读的校验错误
func (e *ValidationError) Error() string {
	items := make([]string, 0, len(e.Report.Differences))
	for _, difference := range e.Report.Differences {
		name := difference.Table
		if difference.Subject != "" {
			name += "." + difference.Subject
		}
		items = append(items, fmt.Sprintf("%s %s: 期望 %s，实际 %s", name, difference.Kind, difference.Expected, difference.Actual))
	}
	return "数据库 Schema 校验失败: " + strings.Join(items, "; ")
}
