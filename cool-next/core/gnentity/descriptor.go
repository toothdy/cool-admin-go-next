package gnentity

import "reflect"

// 字段的不可变实现
type fieldDescriptor struct {
	name               string
	jsonName           string
	column             string
	description        string
	logicalType        LogicalType
	goType             reflect.Type
	isNullable         bool
	isPrimary          bool
	isAutoIncrement    bool
	isSystemMaintained bool
	isPersistent       bool
	constraints        Constraints
}

// 字段逻辑名
func (f *fieldDescriptor) Name() string { return f.name }

// JSON 字段名
func (f *fieldDescriptor) JSONName() string { return f.jsonName }

// 数据库列名
func (f *fieldDescriptor) Column() string { return f.column }

// 字段描述
func (f *fieldDescriptor) Description() string { return f.description }

// 跨数据库逻辑类型
func (f *fieldDescriptor) LogicalType() LogicalType { return f.logicalType }

// Go 字段类型
func (f *fieldDescriptor) GoType() reflect.Type { return f.goType }

// 是否可为空
func (f *fieldDescriptor) Nullable() bool { return f.isNullable }

// 是否主键
func (f *fieldDescriptor) Primary() bool { return f.isPrimary }

// 是否自增
func (f *fieldDescriptor) AutoIncrement() bool { return f.isAutoIncrement }

// 是否由系统维护
func (f *fieldDescriptor) SystemMaintained() bool { return f.isSystemMaintained }

// 是否持久化
func (f *fieldDescriptor) Persistent() bool { return f.isPersistent }

// 可移植字段约束
func (f *fieldDescriptor) Constraints() Constraints { return f.constraints }

// Descriptor 的不可变实现
type descriptorValue[E any, ID comparable] struct {
	table            string
	description      string
	entityType       reflect.Type
	idType           reflect.Type
	primary          Field
	fields           []Field
	persistentFields []Field
	byName           map[string]Field
	byJSON           map[string]Field
	byColumn         map[string]Field
	indexes          []Index
	doShape          *doShape
}

// 数据库表名
func (d *descriptorValue[E, ID]) Table() string { return d.table }

// 表描述
func (d *descriptorValue[E, ID]) Description() string { return d.description }

// 主键字段
func (d *descriptorValue[E, ID]) Primary() Field { return d.primary }

// 实体 Go 类型
func (d *descriptorValue[E, ID]) EntityType() reflect.Type { return d.entityType }

// 主键 Go 类型
func (d *descriptorValue[E, ID]) IDType() reflect.Type { return d.idType }

// 创建独立数据库写入值
func (d *descriptorValue[E, ID]) NewDO() DOValue { return d.doShape.newValue() }

// 字段列表副本
func (d *descriptorValue[E, ID]) Fields() []Field {
	return append([]Field(nil), d.fields...)
}

// 持久化字段列表副本
func (d *descriptorValue[E, ID]) PersistentFields() []Field {
	return append([]Field(nil), d.persistentFields...)
}

// 按逻辑名查找字段
func (d *descriptorValue[E, ID]) Field(name string) (Field, bool) {
	field, exists := d.byName[name]
	return field, exists
}

// 按 JSON 名查找字段
func (d *descriptorValue[E, ID]) JSON(name string) (Field, bool) {
	field, exists := d.byJSON[name]
	return field, exists
}

// 按列名查找字段
func (d *descriptorValue[E, ID]) Column(name string) (Field, bool) {
	field, exists := d.byColumn[name]
	return field, exists
}

// 索引列表深拷贝
func (d *descriptorValue[E, ID]) Indexes() []Index {
	return cloneIndexes(d.indexes)
}

// 索引切片深拷贝
func cloneIndexes(indexes []Index) []Index {
	cloned := make([]Index, len(indexes))
	for index, item := range indexes {
		cloned[index] = Index{
			Name:   item.Name,
			Fields: append([]string(nil), item.Fields...),
			Unique: item.Unique,
		}
	}

	return cloned
}
