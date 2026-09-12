package service

import (
	"context"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/modules/dict/entity"
)

// 字典类别及关联信息删除
type TypeService struct {
	*gnservice.Base[entity.Type, uint64]
	infoBase *gnservice.Base[entity.Info, uint64]
}

// 字典类别业务服务
func NewType(
	typeBase *gnservice.Base[entity.Type, uint64],
	infoBase *gnservice.Base[entity.Info, uint64],
) *TypeService {

	return &TypeService{Base: typeBase, infoBase: infoBase}
}

// 删除类别及其全部字典信息
func (service *TypeService) Delete(ctx context.Context, input gnservice.DeleteInput[uint64]) error {
	if err := service.Base.Delete(ctx, input); err != nil {
		return err
	}
	model, err := service.infoBase.Model(ctx)
	if err != nil {
		return err
	}
	var rows []struct {
		ID uint64 `orm:"id"`
	}
	if err = model.Fields("id").WhereIn("typeId", input.IDs()).Scan(&rows); err != nil {
		return exception.WrapCore(err, "查询类别字典信息失败")
	}
	if len(rows) == 0 {
		return nil
	}
	ids := make([]uint64, len(rows))
	for index, row := range rows {
		ids[index] = row.ID
	}
	deleteInput, err := gnservice.NewDeleteInput[entity.Info](service.infoBase.Descriptor(), ids)
	if err != nil {
		return err
	}

	return service.infoBase.Delete(ctx, deleteInput)
}
