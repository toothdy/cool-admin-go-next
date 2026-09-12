package gnrecycle

import (
	"reflect"

	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

func recordDescriptor() (gnentity.Descriptor[Record, uint64], error) {
	return gnentity.Compile[Record, uint64](gnentity.Schema{Indexes: []gnentity.Index{
		gnentity.IndexOf("idx_cool_recycle_target", "databaseGroup", "tableName"),
	}})
}

func compileRegistry(descriptors []gnentity.RuntimeDescriptor) (map[string]gnentity.RuntimeDescriptor, error) {
	registry := make(map[string]gnentity.RuntimeDescriptor, len(descriptors))
	for _, descriptor := range descriptors {
		if isNilDescriptor(descriptor) {
			return nil, gerror.New("回收站 Descriptor 不能为 nil")
		}
		if descriptor.Table() == TableName {
			return nil, gerror.New("业务 Descriptor 不能使用内部表 cool_recycle")
		}
		if descriptor.EntityType() == nil || descriptor.EntityType().Kind() != reflect.Struct ||
			descriptor.IDType() == nil || descriptor.Primary() == nil || descriptor.NewDO() == nil {
			return nil, gerror.Newf("表 %s 的 Descriptor 无效", descriptor.Table())
		}
		if _, exists := registry[descriptor.Table()]; exists {
			return nil, gerror.Newf("回收站存在重复业务表: %s", descriptor.Table())
		}
		registry[descriptor.Table()] = descriptor
	}

	return registry, nil
}

func isNilDescriptor(descriptor gnentity.RuntimeDescriptor) bool {
	if descriptor == nil {
		return true
	}
	value := reflect.ValueOf(descriptor)

	return value.Kind() == reflect.Pointer && value.IsNil()
}
