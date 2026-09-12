package service

import (
	"context"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/modules/space/entity"
)

// 文件空间分类业务服务
type TypeService struct {
	*gnservice.Base[entity.Type, uint64]
	info *InfoService
}

// 创建文件空间分类业务服务
func NewType(
	typeBase *gnservice.Base[entity.Type, uint64],
	info *InfoService,
) *TypeService {

	return &TypeService{Base: typeBase, info: info}
}

// 删除分类及该分类下的文件信息
func (service *TypeService) Delete(ctx context.Context, input gnservice.DeleteInput[uint64]) error {
	if err := service.Base.Delete(ctx, input); err != nil {
		return err
	}
	model, err := service.info.Model(ctx)
	if err != nil {
		return err
	}
	var rows []struct {
		ID uint64 `orm:"id"`
	}
	if err = model.Fields("id").WhereIn("classifyId", input.IDs()).Scan(&rows); err != nil {
		return exception.WrapCore(err, "查询分类文件失败")
	}
	if len(rows) == 0 {
		return nil
	}
	ids := make([]uint64, len(rows))
	for index, row := range rows {
		ids[index] = row.ID
	}
	deleteInput, err := gnservice.NewDeleteInput[entity.Info](service.info.Descriptor(), ids)
	if err != nil {
		return err
	}

	return service.info.Delete(ctx, deleteInput)
}
