package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/os/gtime"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 种子写入所需的最小 gdb 事务能力
type Model interface {
	Model(...any) *gdb.Model
}

// 尚未解码的种子记录，键为业务字段的 JSON 名
type Record map[string]json.RawMessage

// 带层级归属的种子记录
type TreeNode struct {
	Record    Record
	Key       string // 本节点的种子内唯一键
	ParentKey string // 父节点的种子内唯一键，根节点为空
}

// 按 Descriptor 构造插入/更新用 DO，自动补齐 createTime/updateTime
func NewDO(descriptor gnentity.RuntimeDescriptor, values map[string]any, isInsert bool) (gnentity.DOValue, error) {
	do := descriptor.NewDO()
	now := gtime.Now()
	if isInsert {
		if err := do.SetColumn("createTime", *now); err != nil {
			return nil, exception.WrapCore(err, "设置初始化创建时间失败")
		}
	}
	if err := do.SetColumn("updateTime", *now); err != nil {
		return nil, exception.WrapCore(err, "设置初始化更新时间失败")
	}
	for field, value := range values {
		if metadata, exists := descriptor.Field(field); exists && !metadata.Persistent() {
			return nil, exception.Core("数据库种子不能写入非持久化字段: " + descriptor.Table() + "." + metadata.JSONName())
		}
		if err := do.SetColumn(field, value); err != nil {
			return nil, exception.WrapCore(err, "构造初始化数据失败")
		}
	}

	return do, nil
}

// 按父子依赖顺序补齐或更新树形记录，返回种子键到实际 ID 的映射
func SyncTree(
	ctx context.Context,
	transaction Model,
	descriptor gnentity.RuntimeDescriptor,
	nodes []TreeNode,
) (map[string]uint64, error) {
	ids := make(map[string]uint64, len(nodes))
	for len(ids) < len(nodes) {
		progressed := false
		for _, node := range nodes {
			if _, exists := ids[node.Key]; exists {
				continue
			}
			parentID, ready := uint64(0), node.ParentKey == ""
			if !ready {
				parentID, ready = ids[node.ParentKey]
			}
			if !ready {
				continue
			}
			values, valuesErr := node.Record.Values(descriptor)
			if valuesErr != nil {
				return nil, valuesErr
			}
			if node.ParentKey == "" {
				values["parentId"] = nil
			} else {
				values["parentId"] = parentID
			}
			model := transaction.Model(descriptor.Table()).Ctx(ctx)
			existing, err := model.Where("seedKey", node.Key).One()
			if err != nil {
				return nil, exception.WrapCore(err, "查询初始化树节点失败")
			}
			if existing == nil {
				data, dataErr := NewDO(descriptor, values, true)
				if dataErr != nil {
					return nil, dataErr
				}
				id, insertErr := model.Data(data.DBData()).InsertAndGetId()
				if insertErr != nil {
					return nil, exception.WrapCore(insertErr, "写入初始化树节点失败")
				}
				if id <= 0 {
					return nil, exception.Core("初始化树节点返回了无效 ID")
				}
				ids[node.Key] = uint64(id)
			} else {
				id := existing["id"].Uint64()
				if id == 0 {
					return nil, exception.Core("初始化树节点缺少 ID")
				}
				data, dataErr := NewDO(descriptor, values, false)
				if dataErr != nil {
					return nil, dataErr
				}
				if _, err = model.Where("id", id).Data(data.DBData()).Update(); err != nil {
					return nil, exception.WrapCore(err, "同步初始化树节点失败")
				}
				ids[node.Key] = id
			}
			progressed = true
		}
		if !progressed {
			return nil, exception.Core("初始化树节点存在无法解析的父子依赖")
		}
	}

	return ids, nil
}

// 解码完整种子记录并保留显式主键
func (record Record) SeedData(descriptor gnentity.RuntimeDescriptor) (any, error) {
	for name := range record {
		field, exists := descriptor.JSON(name)
		if !exists {
			return nil, exception.Core("数据库种子包含未知字段: " + descriptor.Table() + "." + name)
		}
		if !field.Persistent() {
			return nil, exception.Core("数据库种子不能包含非持久化字段: " + descriptor.Table() + "." + name)
		}
	}
	values, err := record.seedValues(descriptor, true)
	if err != nil {
		return nil, err
	}
	do, err := NewDO(descriptor, values, true)
	if err != nil {
		return nil, err
	}

	return do.DBData(), nil
}

// 按 Descriptor 把 JSON 字段解码为可写入的列值
func (record Record) Values(descriptor gnentity.RuntimeDescriptor) (map[string]any, error) {
	return record.seedValues(descriptor, false)
}

func (record Record) seedValues(descriptor gnentity.RuntimeDescriptor, includePrimary bool) (map[string]any, error) {
	values := make(map[string]any, len(record))
	for name, raw := range record {
		field, exists := descriptor.JSON(name)
		if !exists || field.SystemMaintained() || (field.Primary() && !includePrimary) {
			continue
		}
		if !field.Persistent() {
			return nil, exception.Core("数据库种子不能包含非持久化字段: " + descriptor.Table() + "." + name)
		}
		value, err := DecodeValue(raw, field)
		if err != nil {
			return nil, err
		}
		values[field.Name()] = value
	}

	return values, nil
}

// 字符串字段，缺失或为空视为错误
func (record Record) String(name string) (string, error) {
	raw, exists := record[name]
	if !exists {
		return "", exception.Core("初始化数据缺少 " + name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" {
		return "", exception.Core("初始化数据 " + name + " 无效")
	}

	return value, nil
}

// 无符号整型字段，缺失、null 或零值返回 false
func (record Record) Uint64(name string) (uint64, bool) {
	raw, exists := record[name]
	if !exists || string(raw) == "null" {
		return 0, false
	}
	var value uint64
	if json.Unmarshal(raw, &value) != nil || value == 0 {
		return 0, false
	}

	return value, true
}

// 按字段的逻辑类型和 Go 类型解码一段 JSON 原始值
func DecodeValue(raw json.RawMessage, field gnentity.Field) (any, error) {
	target := reflect.New(field.GoType())
	if field.LogicalType() == gnentity.LogicalBool {
		if err := json.Unmarshal(raw, target.Interface()); err == nil {
			return target.Elem().Interface(), nil
		}
		var number int
		if err := json.Unmarshal(raw, &number); err == nil && (number == 0 || number == 1) {
			return number == 1, nil
		}
	}
	if field.LogicalType() == gnentity.LogicalJSON {
		var encoded string
		if json.Unmarshal(raw, &encoded) == nil && strings.EqualFold(strings.TrimSpace(encoded), "null") {
			return reflect.MakeSlice(field.GoType(), 0, 0).Interface(), nil
		}
	}
	if err := json.Unmarshal(raw, target.Interface()); err != nil {
		return nil, exception.WrapCore(err, fmt.Sprintf("解析初始化字段 %s 失败", field.Name()))
	}
	value := target.Elem()
	if field.GoType().Kind() == reflect.Pointer && !value.IsNil() {
		return value.Elem().Interface(), nil
	}

	return value.Interface(), nil
}
