package gnservice

import (
	"context"
	"fmt"
	"reflect"

	"github.com/gogf/gf/v2/database/gdb"

	"github.com/gogf/gf/v2/util/gconv"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
	"github.com/toothdy/cool-admin-go-next/cool-next/crud"
	"github.com/toothdy/cool-admin-go-next/cool-next/db"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/gnrecycle"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/tx"
)

// Descriptor 驱动的基础 Service
type Base[E any, ID comparable] struct {
	database   gdb.DB
	descriptor gnentity.Descriptor[E, ID]
	group      string
	recycler   gnrecycle.Deleter
}

// 构造基础 Service
func NewBase[E any, ID comparable](
	descriptor gnentity.Descriptor[E, ID],
	runtime *db.Runtime,
	recycler gnrecycle.Deleter,
) (*Base[E, ID], error) {
	if err := validateDescriptor[E, ID](descriptor); err != nil {
		return nil, err
	}
	if runtime.DB() == nil || runtime.Group() == "" {
		return nil, exception.Core("框架数据库 Runtime 无效")
	}
	if runtime.DB().GetGroup() != runtime.Group() {
		return nil, exception.Core(fmt.Sprintf("框架数据库组不匹配: 期望 %s，实际 %s",
			runtime.Group(),
			runtime.DB().GetGroup()),
		)
	}
	if isNil(recycler) {
		return nil, exception.Core("删除归档 Store 无效")
	}

	return &Base[E, ID]{
		database:   runtime.DB(),
		descriptor: descriptor,
		group:      runtime.Group(),
		recycler:   recycler,
	}, nil
}

// 返回实体 Descriptor
func (base *Base[E, ID]) Descriptor() gnentity.Descriptor[E, ID] {
	if base == nil {
		return nil
	}

	return base.descriptor
}

// 返回当前实体使用的 ORM 管理器
func (base *Base[E, ID]) GetOrmManager() gdb.DB {
	if base == nil {
		return nil
	}

	return base.database
}

// 返回当前框架事务
func (base *Base[E, ID]) Tx(ctx context.Context) (gdb.TX, error) {
	if err := base.validate(ctx); err != nil {
		return nil, err
	}
	transaction, group, exists := tx.Current(ctx)
	if !exists {
		return nil, exception.Core("当前上下文不存在框架事务")
	}
	if group != base.group {
		return nil, exception.Core(fmt.Sprintf("事务数据库组不匹配: 当前 %s，请求 %s", group, base.group))
	}
	if transaction == nil {
		return nil, exception.Core("当前框架事务无效")
	}

	return transaction, nil
}

// 返回绑定当前事务或框架数据库组的实体 Model
func (base *Base[E, ID]) Model(ctx context.Context) (*gdb.Model, error) {
	if err := base.validate(ctx); err != nil {
		return nil, err
	}
	transaction, group, exists := tx.Current(ctx)
	if !exists {
		return base.database.Model(base.descriptor.Table()).Ctx(ctx), nil
	}
	if group != base.group {
		return nil, exception.Core(fmt.Sprintf("事务数据库组不匹配: 当前 %s，请求 %s", group, base.group))
	}
	if transaction == nil {
		return nil, exception.Core("当前框架事务无效")
	}

	return transaction.Model(base.descriptor.Table()).Ctx(ctx), nil
}

// 新增单个或多个实体
func (base *Base[E, ID]) Add(ctx context.Context, input AddInput[E]) (AddResult[ID], error) {
	if err := validateAddInput(input); err != nil {
		return AddResult[ID]{}, err
	}
	model, policy, err := base.writeModel(ctx, crud.ActionAdd)
	if err != nil {
		return AddResult[ID]{}, err
	}
	values := input.many
	if !input.isMany {
		values = []*Mutable[E]{input.one}
	}
	ids := make([]ID, len(values))
	for index, value := range values {
		data, _, currentErr := base.mutableData(value, policy)
		if currentErr != nil {
			return AddResult[ID]{}, currentErr
		}
		insertedID, currentErr := model.Data(data).InsertAndGetId()
		if currentErr != nil {
			return AddResult[ID]{}, exception.WrapCore(currentErr, "新增实体失败")
		}
		ids[index], currentErr = base.convertInsertedID(insertedID)
		if currentErr != nil {
			return AddResult[ID]{}, currentErr
		}
	}
	if input.isMany {
		return AddResult[ID]{isMany: true, many: ids}, nil
	}

	return AddResult[ID]{one: ids[0]}, nil
}

// 按全局配置归档后删除或直接物理删除一个或多个实体
func (base *Base[E, ID]) Delete(ctx context.Context, input DeleteInput[ID]) error {
	if len(input.ids) == 0 {
		return exception.Validate("删除 ID 不能为空")
	}
	for _, id := range input.ids {
		if err := validateID[E](base.Descriptor(), id); err != nil {
			return err
		}
	}
	if _, _, err := base.writeModel(ctx, crud.ActionDelete); err != nil {
		return err
	}
	ids := make([]any, len(input.ids))
	for index, id := range input.ids {
		ids[index] = id
	}
	if err := base.recycler.Delete(ctx, base.descriptor, ids); err != nil {
		return exception.WrapCore(err, "删除实体失败")
	}

	return nil
}

// 更新单个或多个实体明确提交的字段
func (base *Base[E, ID]) Update(ctx context.Context, input UpdateInput[E, ID]) error {
	if err := validateUpdateInput(input); err != nil {
		return err
	}
	model, policy, err := base.writeModel(ctx, crud.ActionUpdate)
	if err != nil {
		return err
	}
	items := input.many
	if !input.isMany {
		items = []UpdateItem[E, ID]{input.one}
	}
	for _, item := range items {
		if err = validateUpdateItem(base.descriptor, item); err != nil {
			return err
		}
		data, count, currentErr := base.mutableData(item.mutable, policy)
		if currentErr != nil {
			return currentErr
		}
		if count == 0 {
			return exception.Validate("更新字段不能为空")
		}
		if _, currentErr = model.
			Where(base.descriptor.Primary().Column(), item.id).
			Data(data).
			Update(); currentErr != nil {
			return exception.WrapCore(currentErr, "更新实体失败")
		}
	}

	return nil
}

// 按主键查询单条记录
func (base *Base[E, ID]) Info(ctx context.Context, id ID) (Record, error) {
	if err := validateID[E](base.Descriptor(), id); err != nil {
		return Record{}, err
	}
	model, err := base.queryModel(ctx, crud.ActionInfo)
	if err != nil {
		return Record{}, err
	}
	primary := base.descriptor.Primary()
	qualified := model.QuoteWord("a") + "." + model.QuoteWord(primary.Column())
	result, err := model.Where(qualified+" = ?", id).One()
	if err != nil {
		return Record{}, exception.WrapCore(err, "查询实体详情失败")
	}
	if result == nil {
		return Record{}, nil
	}

	return base.record(result), nil
}

// 查询记录列表
func (base *Base[E, ID]) List(ctx context.Context, query Query) ([]Record, error) {
	if err := validateReadQuery(query); err != nil {
		return nil, err
	}
	model, err := base.queryModel(ctx, crud.ActionList)
	if err != nil {
		return nil, err
	}
	if query.listLimit > 0 {
		model = model.Limit(query.listLimit)
	}
	result, err := model.All()
	if err != nil {
		return nil, exception.WrapCore(err, "查询实体列表失败")
	}

	return base.records(result), nil
}

// 分页查询记录
func (base *Base[E, ID]) Page(ctx context.Context, query Query) (PageResult, error) {
	model, err := base.queryModel(ctx, crud.ActionPage)
	if err != nil {
		return PageResult{}, err
	}
	var result gdb.Result
	pagination, err := renderPage(model, query, &result)
	if err != nil {
		return PageResult{}, err
	}

	return PageResult{
		List:       base.records(result),
		Pagination: pagination,
	}, nil
}

// 操作 Model 获得分页数据
func (base *Base[E, ID]) EntityRenderPage(
	ctx context.Context,
	model *gdb.Model,
	query Query,
	destination any,
) (Pagination, error) {
	if err := base.validate(ctx); err != nil {
		return Pagination{}, err
	}
	if scope, exists := crud.CurrentOperation(ctx); exists {
		if scope.Plan().Action() != crud.ActionPage {
			return Pagination{}, exception.Core("当前 CRUD 动作不是分页查询")
		}
		var err error
		model, err = scope.Plan().ApplyQuery(ctx, model)
		if err != nil {
			return Pagination{}, exception.WrapCore(err, "应用分页查询计划失败")
		}
	}

	return renderPage(model, query, destination)
}

func (base *Base[E, ID]) queryModel(ctx context.Context, action crud.Action) (*gdb.Model, error) {
	model, err := base.Model(ctx)
	if err != nil {
		return nil, err
	}
	scope, exists := crud.CurrentOperation(ctx)
	if !exists || scope.Plan() == nil {
		return nil, exception.Core("当前上下文不存在 CRUD 动作计划")
	}
	if scope.Plan().Action() != action {
		return nil, exception.Core(fmt.Sprintf("CRUD 动作计划不匹配: 当前 %s，请求 %s", scope.Plan().Action(), action))
	}
	applied, err := scope.Plan().ApplyQuery(ctx, model)
	if err != nil {
		return nil, exception.WrapCore(err, "应用 CRUD 查询计划失败")
	}

	return applied, nil
}

func (base *Base[E, ID]) writeModel(ctx context.Context, action crud.Action) (*gdb.Model, *crud.FieldPolicy, error) {
	transaction, err := base.Tx(ctx)
	if err != nil {
		return nil, nil, err
	}
	scope, exists := crud.CurrentOperation(ctx)
	if !exists || scope.Plan() == nil {
		return nil, nil, exception.Core("当前上下文不存在 CRUD 动作计划")
	}
	if scope.Plan().Action() != action {
		return nil, nil, exception.Core(fmt.Sprintf("CRUD 动作计划不匹配: 当前 %s，请求 %s", scope.Plan().Action(), action))
	}
	return transaction.Model(base.descriptor.Table()).Ctx(ctx), scope.Plan().Fields(), nil
}

func (base *Base[E, ID]) mutableData(
	value *Mutable[E],
	policy *crud.FieldPolicy,
) (any, int, error) {
	if err := validateMutable[E, ID](base.descriptor, value); err != nil {
		return nil, 0, err
	}
	do := base.descriptor.NewDO()
	if do == nil {
		return nil, 0, exception.Core("实体 Descriptor 返回了无效 DOValue")
	}
	count := 0
	for _, field := range base.descriptor.PersistentFields() {
		item, exists := value.values[field.Name()]
		if !exists {
			continue
		}
		if policy.IsHidden(field.Name()) && item.source == fieldSourceClient {
			return nil, 0, exception.Validate(fmt.Sprintf("隐藏字段 %s 不允许写入", field.JSONName()))
		}
		if field.Primary() || field.SystemMaintained() {
			if item.source == fieldSourceClient {
				continue
			}

			return nil, 0, exception.Validate(fmt.Sprintf("系统字段 %s 不允许写入", field.JSONName()))
		}
		if policy.IsReadonly(field.Name()) {
			// 前端整行提交通常会原样回传只读字段，Add 与 Update 统一忽略
			// 业务代码通过 Set 改写后按服务端字段写入
			if item.source == fieldSourceClient {
				continue
			}
		}
		data := item.data
		if item.isNull {
			data = nil
		}
		if err := do.SetColumn(field.Name(), data); err != nil {
			return nil, 0, exception.WrapCore(err, "构造实体 DOValue 失败")
		}
		count++
	}

	return do.DBData(), count, nil
}

func (base *Base[E, ID]) convertInsertedID(insertedID int64) (ID, error) {
	var zero ID
	if insertedID < 0 {
		return zero, exception.Core("数据库返回了无效新增主键")
	}
	id, matches := any(uint64(insertedID)).(ID)
	if !matches || reflect.TypeOf(id) != base.descriptor.IDType() {
		return zero, exception.Core("数据库新增主键类型与 Descriptor 不匹配")
	}

	return id, nil
}

func (base *Base[E, ID]) validate(ctx context.Context) error {
	if base == nil || base.database == nil || base.descriptor == nil || base.group == "" {
		return exception.Core("基础 Service 未初始化")
	}
	if ctx == nil {
		return exception.Core("Service 上下文不能为空")
	}

	return nil
}

func renderPage(model *gdb.Model, query Query, destination any) (Pagination, error) {
	if err := validateReadQuery(query); err != nil {
		return Pagination{}, err
	}
	if model == nil {
		return Pagination{}, exception.Validate("分页 ORM Model 不能为空")
	}
	if isNil(destination) || reflect.TypeOf(destination).Kind() != reflect.Pointer {
		return Pagination{}, exception.Validate("分页查询目标必须是非 nil 指针")
	}
	total := 0
	if err := model.Page(query.page, query.size).ScanAndCount(destination, &total, false); err != nil {
		return Pagination{}, exception.WrapCore(err, "分页查询实体失败")
	}

	return Pagination{Page: query.page, Size: query.size, Total: int64(total)}, nil
}

func validateReadQuery(query Query) error {
	if query.page <= 0 || query.size <= 0 {
		return exception.Validate("分页参数必须为正数")
	}

	return nil
}

func (base *Base[E, ID]) records(result gdb.Result) []Record {
	records := make([]Record, len(result))
	for index, current := range result {
		records[index] = base.record(current)
	}

	return records
}

func (base *Base[E, ID]) record(record gdb.Record) Record {
	values := make(map[string]any, len(record))
	for field, value := range record {
		raw := value.Val()
		if descriptor, exists := base.descriptor.JSON(field); exists {
			raw = dbValue(raw, descriptor.GoType())
		}
		values[field] = cloneData(raw)
	}

	return Record{values: values}
}

func dbValue(value any, target reflect.Type) any {
	if value == nil {
		return nil
	}
	if target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	converted := reflect.New(target)
	if err := gconv.Scan(value, converted.Interface()); err != nil {
		return value
	}

	return converted.Elem().Interface()
}
