package service

import (
	"context"
	"time"

	"github.com/gogf/gf/v2/os/gtime"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/modules/base/entity"
)

type logWrite struct {
	UserID *uint64        `orm:"userId"`
	Action string         `orm:"action"`
	IP     *string        `orm:"ip"`
	Params map[string]any `orm:"params"`
}

// 操作日志写入数据
type LogRecord struct {
	UserID *uint64
	Action string
	IP     string
	Params map[string]any
}

// 带用户名称的操作日志分页项
type LogPageItem struct {
	ID         uint64         `json:"id"`
	CreateTime *gtime.Time    `json:"createTime"`
	UpdateTime *gtime.Time    `json:"updateTime"`
	UserID     *uint64        `json:"userId"`
	Name       *string        `json:"name"`
	Action     string         `json:"action"`
	IP         *string        `json:"ip"`
	Params     map[string]any `json:"params"`
}

// 操作日志分页结果
type LogPageResult struct {
	List       []LogPageItem        `json:"list"`
	Pagination gnservice.Pagination `json:"pagination"`
}

// 操作日志记录、分页和清理
type LogService struct {
	*gnservice.Base[entity.Log, uint64]
	conf *ConfService
	user *gnservice.Base[entity.User, uint64]
	now  func() time.Time
}

// 操作日志服务
func NewLog(
	baseService *gnservice.Base[entity.Log, uint64],
	conf *ConfService,
	user *gnservice.Base[entity.User, uint64],
) *LogService {

	return &LogService{Base: baseService, conf: conf, user: user, now: time.Now}
}

// 写入一条后台业务操作日志
func (service *LogService) Record(ctx context.Context, record LogRecord) error {
	if record.Action == "" {
		return exception.Core("操作日志服务或记录无效")
	}
	model, err := service.Model(ctx)
	if err != nil {
		return err
	}
	ip := record.IP
	if _, err = model.Data(logWrite{
		UserID: record.UserID,
		Action: record.Action,
		IP:     &ip,
		Params: record.Params,
	}).Insert(); err != nil {
		return exception.WrapCore(err, "写入操作日志失败")
	}

	return nil
}

// 带用户名称的操作日志分页
func (service *LogService) Page(ctx context.Context, query gnservice.Query) (LogPageResult, error) {
	page, err := service.Base.Page(ctx, query)
	if err != nil {
		return LogPageResult{}, err
	}
	items := make([]LogPageItem, len(page.List))
	userIDs := make([]uint64, 0, len(page.List))
	for index, record := range page.List {
		if err = record.Scan(&items[index]); err != nil {
			return LogPageResult{}, exception.WrapCore(err, "解析操作日志分页失败")
		}
		if items[index].UserID != nil {
			userIDs = append(userIDs, *items[index].UserID)
		}
	}
	names, err := service.userNames(ctx, userIDs)
	if err != nil {
		return LogPageResult{}, err
	}
	for index := range items {
		if items[index].UserID != nil {
			items[index].Name = names[*items[index].UserID]
		}
	}

	return LogPageResult{List: items, Pagination: page.Pagination}, nil
}

// 清空全部或超过保留期的操作日志
func (service *LogService) Clear(ctx context.Context, all bool) (int64, error) {
	model, err := service.Model(ctx)
	if err != nil {
		return 0, err
	}
	if all {
		// GoFrame 要求删除操作显式携带 WHERE 条件
		model = model.Where("1=1")
	} else {
		days, keepErr := service.conf.LogKeep(ctx)
		if keepErr != nil {
			return 0, keepErr
		}
		cutoff := service.now().Local().AddDate(0, 0, -days)
		model = model.WhereLT("createTime", cutoff)
	}
	result, err := model.Delete()
	if err != nil {
		return 0, exception.WrapCore(err, "清理操作日志失败")
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, exception.WrapCore(err, "读取操作日志清理结果失败")
	}

	return count, nil
}

// 返回操作日志保留天数
func (service *LogService) GetKeep(ctx context.Context) (int, error) {
	return service.conf.LogKeep(ctx)
}

// 更新操作日志保留天数
func (service *LogService) SetKeep(ctx context.Context, days int) error {
	return service.conf.SetLogKeep(ctx, days)
}

func (service *LogService) userNames(ctx context.Context, userIDs []uint64) (map[uint64]*string, error) {
	result := make(map[uint64]*string)
	if len(userIDs) == 0 {
		return result, nil
	}
	model, err := service.user.Model(ctx)
	if err != nil {
		return nil, err
	}
	type userNameRow struct {
		ID   uint64  `orm:"id"`
		Name *string `orm:"name"`
	}
	var rows []userNameRow
	if err = model.Fields("id", "name").WhereIn("id", userIDs).Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询操作日志用户失败")
	}
	for _, row := range rows {
		result[row.ID] = row.Name
	}

	return result, nil
}
