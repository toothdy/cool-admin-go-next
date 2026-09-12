package seed

import (
	"context"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
	"github.com/toothdy/cool-admin-go-next/cool-next/db"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/schema"
)

const TableName = "cool_seed_lock" // 种子导入幂等标记表名

type lockRecord struct {
	g.Meta `orm:"table:cool_seed_lock" description:"种子导入幂等标记"`
	gnentity.Base
	SeedKey string `json:"seedKey" orm:"seedKey" description:"模块与种子类型标识" cool:"size=191"`
}

// 种子导入幂等标记的存储与守卫
type Store struct {
	runtime    *db.Runtime
	descriptor gnentity.Descriptor[lockRecord, uint64]
}

// 种子导入幂等守卫
func NewStore(runtime *db.Runtime) (*Store, error) {
	if runtime.DB() == nil || runtime.Runner() == nil {
		return nil, exception.Core("种子导入守卫依赖的框架数据库 Runtime 无效")
	}
	descriptor, err := gnentity.Compile[lockRecord, uint64](gnentity.Schema{
		Indexes: []gnentity.Index{gnentity.UniqueIndexOf("uk_cool_seed_lock_key", "seedKey")},
	})
	if err != nil {
		return nil, exception.WrapCore(err, "构建种子导入标记 Descriptor 失败")
	}

	return &Store{runtime: runtime, descriptor: descriptor}, nil
}

// 同步种子导入幂等标记表
func (store *Store) Prepare(ctx context.Context) error {
	if store == nil || store.runtime == nil {
		return exception.Core("种子导入守卫未初始化")
	}
	manager, err := schema.New(store.runtime.DB(), store.runtime.Dialect())
	if err != nil {
		return exception.WrapCore(err, "创建种子导入标记 Schema 管理器失败")
	}
	if _, err = manager.Apply(ctx, schema.Sync, store.descriptor); err != nil {
		return exception.WrapCore(err, "同步种子导入标记表失败")
	}

	return nil
}

// 指定种子导入标记是否已存在
func (store *Store) Has(ctx context.Context, key string) (bool, error) {
	if store == nil || store.runtime == nil {
		return false, exception.Core("种子导入守卫未初始化")
	}
	if key == "" {
		return false, exception.Core("种子导入幂等键不能为空")
	}
	record, err := store.runtime.DB().Model(TableName).Ctx(ctx).Where("seedKey", key).One()
	if err != nil {
		return false, exception.WrapCore(err, "查询种子导入标记失败")
	}
	return record != nil, nil
}

// 在当前事务内幂等执行种子导入
func (store *Store) Guard(ctx context.Context, key string, fn func(context.Context) error) error {
	if store == nil || store.runtime == nil {
		return exception.Core("种子导入守卫未初始化")
	}
	if key == "" {
		return exception.Core("种子导入幂等键不能为空")
	}
	transaction, exists, err := store.runtime.Current(ctx)
	if err != nil {
		return err
	}
	if !exists || transaction == nil {
		return exception.Core("Guard 必须在框架事务内调用")
	}
	existing, err := transaction.Model(TableName).Ctx(ctx).Where("seedKey", key).One()
	if err != nil {
		return exception.WrapCore(err, "查询种子导入标记失败")
	}
	if existing != nil {
		return nil
	}
	if err = fn(ctx); err != nil {
		return err
	}
	do, err := NewDO(store.descriptor, map[string]any{"seedKey": key}, true)
	if err != nil {
		return exception.WrapCore(err, "设置种子导入标记失败")
	}
	if _, err = transaction.Model(TableName).Ctx(ctx).Data(do.DBData()).Insert(); err != nil {
		return exception.WrapCore(err, "写入种子导入标记失败")
	}

	return nil
}
