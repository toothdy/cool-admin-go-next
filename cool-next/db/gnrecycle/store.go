package gnrecycle

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
	"github.com/toothdy/cool-admin-go-next/cool-next/crud"
	"github.com/toothdy/cool-admin-go-next/cool-next/db"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/driver"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/schema"
)

// 删除归档与恢复存储
type Store struct {
	runtime          *db.Runtime
	config           crud.Config
	descriptors      map[string]gnentity.RuntimeDescriptor
	recordDescriptor gnentity.Descriptor[Record, uint64]
}

// 创建删除归档与恢复 Store
func New(
	runtime *db.Runtime,
	config crud.Config,
	descriptors ...gnentity.RuntimeDescriptor,
) (*Store, error) {
	if runtime.DB() == nil || runtime.Group() == "" {
		return nil, exception.Core("回收站的框架数据库 Runtime 无效")
	}
	registry, err := compileRegistry(descriptors)
	if err != nil {
		return nil, exception.WrapCore(err, "构建回收站 Descriptor 注册表失败")
	}
	recordDescriptor, err := recordDescriptor()
	if err != nil {
		return nil, exception.WrapCore(err, "构建回收表 Descriptor 失败")
	}

	return &Store{
		runtime:          runtime,
		config:           config,
		descriptors:      registry,
		recordDescriptor: recordDescriptor,
	}, nil
}

// 在应用 Ready 前同步或验证回收表及事务能力
func (store *Store) Prepare(ctx context.Context, mode schema.Mode) error {
	if err := store.validate(); err != nil {
		return err
	}
	if !store.config.SoftDelete {
		return nil
	}
	manager, err := schema.New(store.runtime.DB(), store.runtime.Dialect())
	if err != nil {
		return exception.WrapCore(err, "创建回收表 Schema 管理器失败")
	}
	effectiveMode := mode
	if effectiveMode == schema.Off {
		effectiveMode = schema.Validate
	}
	if _, err = manager.Apply(ctx, effectiveMode, store.recordDescriptor); err != nil {
		return exception.WrapCore(err, "验证回收表失败")
	}
	if _, err = driver.Probe(ctx, store.runtime.DB(), TableName); err != nil {
		return exception.WrapCore(err, "探测回收表事务能力失败")
	}

	return nil
}

// 按配置归档后删除或直接物理删除
func (store *Store) Delete(
	ctx context.Context,
	descriptor gnentity.RuntimeDescriptor,
	ids []any,
) error {
	if err := store.validate(); err != nil {
		return err
	}
	transaction, exists, err := store.runtime.Current(ctx)
	if err != nil {
		return exception.WrapCore(err, "读取回收站事务失败")
	}
	if !exists || transaction == nil {
		return exception.Core("删除归档必须在框架事务中执行")
	}
	ids, err = checkIDs(descriptor, ids)
	if err != nil {
		return err
	}
	if !store.config.SoftDelete {
		return store.deleteRows(ctx, transaction, descriptor, ids, false)
	}
	registered, exists := store.descriptors[descriptor.Table()]
	if !exists || registered.EntityType() != descriptor.EntityType() || registered.IDType() != descriptor.IDType() {
		return exception.Core(fmt.Sprintf("表 %s 不属于当前生成 Descriptor 图", descriptor.Table()))
	}

	return store.archiveDelete(ctx, transaction, descriptor, ids)
}

// 在单个框架事务中恢复一条回收记录
func (store *Store) Restore(ctx context.Context, id uint64) error {
	if err := store.validate(); err != nil {
		return err
	}
	if !store.Enabled() {
		return exception.Core("回收站未启用")
	}
	if id == 0 {
		return exception.Core("回收记录 ID 无效")
	}

	return store.runtime.Runner().Within(ctx, func(scopeCtx context.Context) error {
		transaction, exists, err := store.runtime.Current(scopeCtx)
		if err != nil {
			return exception.WrapCore(err, "读取恢复事务失败")
		}
		if !exists || transaction == nil {
			return exception.Core("恢复事务无效")
		}
		record, err := store.lockRecord(scopeCtx, transaction, id)
		if err != nil {
			return err
		}
		descriptor, exists := store.descriptors[record.TableName]
		if !exists {
			return exception.Core(fmt.Sprintf("回收记录目标表 %s 不属于当前生成 Descriptor 图", record.TableName))
		}
		if record.DatabaseGroup != store.runtime.Group() {
			return exception.Core(fmt.Sprintf("回收记录数据库组不匹配: 当前 %s，记录 %s",
				store.runtime.Group(),
				record.DatabaseGroup),
			)
		}
		entities, err := decodeSnapshot(record.Data, descriptor, record.Count)
		if err != nil {
			return err
		}
		if err = store.insertSnapshot(scopeCtx, transaction, descriptor, entities); err != nil {
			return err
		}
		result, err := transaction.Model(TableName).
			Ctx(scopeCtx).
			Unscoped().
			Where(store.recordDescriptor.Primary().Column(), id).
			Delete()
		if err != nil {
			return exception.WrapCore(err, "删除已恢复回收记录失败")
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return exception.WrapCore(err, "读取回收记录删除行数失败")
		}
		if affected != 1 {
			return exception.Core("回收记录并发恢复冲突")
		}

		return nil
	})
}

func (store *Store) archiveDelete(
	ctx context.Context,
	transaction gdb.TX,
	descriptor gnentity.RuntimeDescriptor,
	ids []any,
) error {
	model := transaction.Model(descriptor.Table()).
		Ctx(ctx).
		Unscoped().
		Fields(columns(descriptor)).
		WhereIn(descriptor.Primary().Column(), ids).
		OrderAsc(descriptor.Primary().Column())
	if store.runtime.Dialect().Kind() != driver.SQLite {
		model = model.LockUpdate()
	}
	entities := reflect.New(reflect.SliceOf(descriptor.EntityType()))
	if err := model.Scan(entities.Interface()); err != nil {
		return exception.WrapCore(err, "锁定并读取删除目标失败")
	}
	rows := entities.Elem()
	if rows.Len() == 0 {
		return nil
	}
	items := make([]map[string]any, rows.Len())
	actualIDs := make([]any, rows.Len())
	for index := 0; index < rows.Len(); index++ {
		items[index] = make(map[string]any, len(descriptor.PersistentFields()))
		for _, field := range descriptor.PersistentFields() {
			value, isNull, currentErr := entityFieldValue(rows.Index(index), field)
			if currentErr != nil {
				return currentErr
			}
			if isNull {
				value = nil
			}
			items[index][field.JSONName()] = value
		}
		actualIDs[index] = items[index][descriptor.Primary().JSONName()]
		if actualIDs[index] == nil {
			return exception.Core("删除快照主键不能为 null")
		}
	}
	snapshot, err := json.Marshal(items)
	if err != nil {
		return exception.WrapCore(err, "编码删除快照失败")
	}
	if err = store.insertRecord(ctx, transaction, descriptor.Table(), snapshot, uint64(rows.Len())); err != nil {
		return err
	}

	return store.deleteRows(ctx, transaction, descriptor, actualIDs, true)
}

func (store *Store) insertRecord(
	ctx context.Context,
	transaction gdb.TX,
	table string,
	snapshot []byte,
	count uint64,
) error {
	data := store.recordDescriptor.NewDO()
	if err := data.SetColumn("databaseGroup", store.runtime.Group()); err != nil {
		return exception.WrapCore(err, "构建回收记录数据库组失败")
	}
	if err := data.SetColumn("tableName", table); err != nil {
		return exception.WrapCore(err, "构建回收记录表名失败")
	}
	if err := data.SetColumn("data", snapshot); err != nil {
		return exception.WrapCore(err, "构建回收记录快照失败")
	}
	if err := data.SetColumn("count", count); err != nil {
		return exception.WrapCore(err, "构建回收记录数量失败")
	}
	audit := currentAudit(ctx)
	for _, optional := range []struct {
		field string
		value string
	}{
		{field: "source", value: audit.source},
		{field: "params", value: audit.params},
		{field: "operatorType", value: audit.operatorType},
		{field: "operatorId", value: audit.operatorID},
	} {
		if optional.value != "" {
			if err := data.SetColumn(optional.field, optional.value); err != nil {
				return exception.WrapCore(err, "构建回收记录来源失败")
			}
		}
	}
	result, err := transaction.Model(TableName).Ctx(ctx).Data(data.DBData()).Insert()
	if err != nil {
		return exception.WrapCore(err, "写入回收记录失败")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return exception.WrapCore(err, "读取回收记录写入行数失败")
	}
	if affected != 1 {
		return exception.Core(fmt.Sprintf("回收记录写入行数冲突: %d", affected))
	}

	return nil
}

func (store *Store) deleteRows(
	ctx context.Context,
	transaction gdb.TX,
	descriptor gnentity.RuntimeDescriptor,
	ids []any,
	checkCount bool,
) error {
	result, err := transaction.Model(descriptor.Table()).
		Ctx(ctx).
		Unscoped().
		WhereIn(descriptor.Primary().Column(), ids).
		Delete()
	if err != nil {
		return exception.WrapCore(err, "物理删除实体失败")
	}
	if !checkCount {
		return nil
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return exception.WrapCore(err, "读取物理删除行数失败")
	}
	if affected != int64(len(ids)) {
		return exception.Core(fmt.Sprintf("归档与物理删除行数冲突: 归档 %d，删除 %d", len(ids), affected))
	}

	return nil
}

func (store *Store) lockRecord(ctx context.Context, transaction gdb.TX, id uint64) (Record, error) {
	model := transaction.Model(TableName).
		Ctx(ctx).
		Unscoped().
		Where(store.recordDescriptor.Primary().Column(), id)
	if store.runtime.Dialect().Kind() != driver.SQLite {
		model = model.LockUpdate()
	}
	var record Record
	if err := model.Scan(&record); errors.Is(err, sql.ErrNoRows) {
		return Record{}, recordNotFoundError(id)
	} else if err != nil {
		return Record{}, exception.WrapCore(err, "锁定回收记录失败")
	}
	if record.ID == 0 {
		return Record{}, recordNotFoundError(id)
	}

	return record, nil
}

func (store *Store) insertSnapshot(
	ctx context.Context,
	transaction gdb.TX,
	descriptor gnentity.RuntimeDescriptor,
	entities reflect.Value,
) error {
	for index := 0; index < entities.Len(); index++ {
		data := descriptor.NewDO()
		for _, field := range descriptor.PersistentFields() {
			value, isNull, err := entityFieldValue(entities.Index(index), field)
			if err != nil {
				return err
			}
			if isNull {
				value = nil
			}
			if err = data.SetColumn(field.Name(), value); err != nil {
				return exception.WrapCore(err, fmt.Sprintf("将快照字段 %s 解码为强类型 DO 失败", field.JSONName()))
			}
		}
		if _, err := transaction.Model(descriptor.Table()).
			Ctx(ctx).
			Unscoped().
			Data(data.DBData()).
			Insert(); err != nil {
			return exception.WrapCore(err, "恢复业务记录失败")
		}
	}

	return nil
}

func decodeSnapshot(
	data []byte,
	descriptor gnentity.RuntimeDescriptor,
	count uint64,
) (reflect.Value, error) {
	if count == 0 || len(data) == 0 {
		return reflect.Value{}, exception.Core("回收记录快照为空")
	}
	var shape []map[string]json.RawMessage
	if err := json.Unmarshal(data, &shape); err != nil {
		return reflect.Value{}, exception.WrapCore(err, "解析回收记录快照形状失败")
	}
	if uint64(len(shape)) != count {
		return reflect.Value{}, exception.Core(fmt.Sprintf("回收记录快照数量不匹配: 记录 %d，快照 %d", count, len(shape)))
	}
	expected := make(map[string]struct{}, len(descriptor.PersistentFields()))
	for _, field := range descriptor.PersistentFields() {
		expected[field.JSONName()] = struct{}{}
	}
	for _, item := range shape {
		if len(item) != len(expected) {
			return reflect.Value{}, exception.Core("回收记录快照字段数量不匹配")
		}
		for field := range expected {
			if _, exists := item[field]; !exists {
				return reflect.Value{}, exception.Core(fmt.Sprintf("回收记录快照缺少字段 %s", field))
			}
		}
	}
	entities := reflect.New(reflect.SliceOf(descriptor.EntityType()))
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(entities.Interface()); err != nil {
		return reflect.Value{}, exception.WrapCore(err, "强类型解码回收记录快照失败")
	}
	if err := requireJSONEnd(decoder); err != nil {
		return reflect.Value{}, err
	}

	return entities.Elem(), nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return exception.Core("回收记录快照包含多余内容")
		}
		return exception.WrapCore(err, "读取回收记录快照结尾失败")
	}

	return nil
}

func checkIDs(descriptor gnentity.RuntimeDescriptor, ids []any) ([]any, error) {
	if isNilDescriptor(descriptor) || descriptor.IDType() == nil || !descriptor.IDType().Comparable() {
		return nil, exception.Core("删除目标 Descriptor 无效")
	}
	if len(ids) == 0 {
		return nil, exception.Core("删除 ID 不能为空")
	}
	unique := make([]any, 0, len(ids))
	seen := make(map[any]struct{}, len(ids))
	for _, id := range ids {
		if id == nil || reflect.TypeOf(id) != descriptor.IDType() {
			return nil, exception.Core("删除 ID 类型与 Descriptor 不匹配")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}

	return unique, nil
}

func columns(descriptor gnentity.RuntimeDescriptor) []string {
	columns := make([]string, 0, len(descriptor.PersistentFields()))
	for _, field := range descriptor.PersistentFields() {
		columns = append(columns, field.Column())
	}

	return columns
}

func entityFieldValue(value reflect.Value, field gnentity.Field) (data any, isNull bool, err error) {
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	for _, candidate := range reflect.VisibleFields(value.Type()) {
		name := strings.Split(candidate.Tag.Get("json"), ",")[0]
		if name != field.JSONName() {
			continue
		}
		current := value.FieldByIndex(candidate.Index)
		if current.Kind() == reflect.Pointer {
			if current.IsNil() {
				return nil, true, nil
			}
			current = current.Elem()
		}
		if current.Kind() == reflect.Slice && current.IsNil() && !field.Nullable() {
			current = reflect.MakeSlice(current.Type(), 0, 0)
		}

		return current.Interface(), false, nil
	}

	return nil, false, exception.Core(fmt.Sprintf("实体 %s 缺少快照字段 %s", value.Type(), field.JSONName()))
}

func (store *Store) validate() error {
	if store == nil || store.runtime.DB() == nil || store.recordDescriptor == nil {
		return exception.Core("回收站 Store 未初始化")
	}
	if store.runtime.DB().GetGroup() != store.runtime.Group() {
		return exception.Core("回收站数据库组不匹配")
	}

	return nil
}
