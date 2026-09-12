package service

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/modules/base/entity"
)

const (
	logKeepKey         = "logKeep"
	defaultLogKeepDays = 31
)

type confValueUpdate struct {
	CValue string `orm:"cValue"`
}

// 内部配置读写
type ConfService struct {
	*gnservice.Base[entity.Conf, uint64]
}

// 内部配置服务
func NewConf(baseService *gnservice.Base[entity.Conf, uint64]) (*ConfService, error) {
	if baseService == nil {
		return nil, exception.Core("配置基础 Service 无效")
	}

	return &ConfService{Base: baseService}, nil
}

// 按键读取内部配置
func (service *ConfService) Value(ctx context.Context, key string) (string, bool, error) {
	if strings.TrimSpace(key) == "" {
		return "", false, exception.Core("配置服务或配置键无效")
	}
	model, err := service.Model(ctx)
	if err != nil {
		return "", false, err
	}
	value, err := model.Fields("cValue").Where("cKey", key).Value()
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, exception.WrapCore(err, "查询系统配置失败")
	}
	if value.IsNil() {
		return "", false, nil
	}

	return value.String(), true, nil
}

// 更新已存在的内部配置
func (service *ConfService) SetValue(ctx context.Context, key, value string) error {
	if strings.TrimSpace(key) == "" {
		return exception.Core("配置服务或配置键无效")
	}
	model, err := service.Model(ctx)
	if err != nil {
		return err
	}
	result, err := model.Data(confValueUpdate{CValue: value}).Where("cKey", key).Update()
	if err != nil {
		return exception.WrapCore(err, "更新系统配置失败")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return exception.WrapCore(err, "读取系统配置更新结果失败")
	}
	if affected == 0 {
		return exception.Comm("系统配置不存在")
	}

	return nil
}

// 返回操作日志保留天数
func (service *ConfService) LogKeep(ctx context.Context) (int, error) {
	value, exists, err := service.Value(ctx, logKeepKey)
	if err != nil {
		return 0, err
	}
	if !exists {
		return defaultLogKeepDays, nil
	}
	days, err := strconv.Atoi(value)
	if err != nil || days <= 0 {
		return 0, exception.Core("日志保留天数配置无效")
	}

	return days, nil
}

// 更新操作日志保留天数
func (service *ConfService) SetLogKeep(ctx context.Context, days int) error {
	if days <= 0 {
		return exception.Validate("日志保留天数必须大于 0")
	}

	return service.SetValue(ctx, logKeepKey, strconv.Itoa(days))
}
