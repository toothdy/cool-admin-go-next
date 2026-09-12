package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/gnrecycle"
	baseservice "github.com/toothdy/cool-admin-go-next/modules/base/service"
	"github.com/toothdy/cool-admin-go-next/modules/recycle/dto"
)

const (
	recycleKeepKey  = "recycleKeep"
	maxRestoreCount = 500
)

type userNameRow struct {
	ID   uint64  `orm:"id"`
	Name *string `orm:"name"`
}

// 回收记录查询、恢复和清理服务
type DataService struct {
	store *gnrecycle.Store
	user  *baseservice.UserService
	conf  *baseservice.ConfService
}

// 创建回收记录业务服务
func NewData(
	store *gnrecycle.Store,
	user *baseservice.UserService,
	conf *baseservice.ConfService,
) (*DataService, error) {
	if store == nil || user == nil || user.Base == nil || conf == nil || conf.Base == nil {
		return nil, exception.Core("回收记录服务依赖无效")
	}

	return &DataService{store: store, user: user, conf: conf}, nil
}

// 分页查询回收记录
func (service *DataService) Page(ctx context.Context, request *dto.DataPageRequest) (dto.DataPageResult, error) {
	if request == nil {
		return dto.DataPageResult{}, exception.Validate("回收记录分页请求不能为空")
	}
	operatorIDs, err := service.operatorIDs(ctx, request.KeyWord)
	if err != nil {
		return dto.DataPageResult{}, err
	}
	page, err := service.store.Page(ctx, gnrecycle.PageInput{
		Page:        request.Page,
		Size:        request.Size,
		Keyword:     request.KeyWord,
		OperatorIDs: operatorIDs,
		Order:       request.Order,
		Sort:        request.Sort,
	})
	if err != nil {
		return dto.DataPageResult{}, err
	}
	userIDs := recordUserIDs(page.List)
	names, err := service.userNames(ctx, userIDs)
	if err != nil {
		return dto.DataPageResult{}, err
	}
	items, err := mapRecords(page.List, names)
	if err != nil {
		return dto.DataPageResult{}, err
	}

	return dto.DataPageResult{List: items, Pagination: page.Pagination}, nil
}

// 返回单条 Node 兼容的回收记录
func (service *DataService) Info(ctx context.Context, request *dto.DataInfoRequest) (*dto.DataItem, error) {
	if request == nil || request.ID == 0 {
		return nil, exception.Validate("回收记录 ID 无效")
	}
	record, err := service.store.Info(ctx, request.ID)
	if err != nil {
		if errors.Is(err, gnrecycle.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if record == nil {
		return nil, nil
	}
	names, err := service.userNames(ctx, recordUserIDs([]gnrecycle.Record{*record}))
	if err != nil {
		return nil, err
	}
	item, err := mapRecord(*record, names)
	if err != nil {
		return nil, err
	}

	return &item, nil
}

// 按请求顺序逐条恢复回收记录
func (service *DataService) Restore(ctx context.Context, request *dto.DataRestoreRequest) error {
	if !service.store.Enabled() {
		return exception.Comm("回收站未启用")
	}
	ids, err := normalizeRestoreIDs(request)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = service.store.Restore(ctx, id); err != nil {
			if errors.Is(err, gnrecycle.ErrRecordNotFound) {
				continue
			}
			return exception.WrapCore(err, fmt.Sprintf("恢复回收记录 %d 失败", id))
		}
	}

	return nil
}

// 按系统配置清理过期回收记录
func (service *DataService) ClearExpired(ctx context.Context) (int64, error) {
	if !service.store.Enabled() {
		return 0, nil
	}
	value, exists, err := service.conf.Value(ctx, recycleKeepKey)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, exception.Core("回收站保留天数配置不存在")
	}
	days, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || days <= 0 {
		return 0, exception.Core("回收站保留天数配置无效")
	}

	return service.store.DeleteExpired(ctx, recycleCutoff(time.Now(), days))
}

// 返回姓名匹配关键字的后台用户 ID
func (service *DataService) operatorIDs(ctx context.Context, keyword string) ([]uint64, error) {
	keyword = strings.TrimSpace(keyword)
	if !service.store.Enabled() || keyword == "" {
		return nil, nil
	}
	model, err := service.user.Model(ctx)
	if err != nil {
		return nil, exception.WrapCore(err, "查询回收记录操作人失败")
	}
	var rows []userNameRow
	if err = model.Fields("id").WhereLike("name", "%"+keyword+"%").Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询回收记录操作人失败")
	}
	result := make([]uint64, len(rows))
	for index, row := range rows {
		result[index] = row.ID
	}

	return result, nil
}

// 批量查询后台用户姓名
func (service *DataService) userNames(ctx context.Context, userIDs []uint64) (map[uint64]*string, error) {
	if len(userIDs) == 0 {
		return map[uint64]*string{}, nil
	}
	model, err := service.user.Model(ctx)
	if err != nil {
		return nil, exception.WrapCore(err, "查询回收记录操作人失败")
	}
	var rows []userNameRow
	if err = model.Fields("id", "name").WhereIn("id", userIDs).Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询回收记录操作人失败")
	}
	result := make(map[uint64]*string, len(rows))
	for _, row := range rows {
		result[row.ID] = row.Name
	}

	return result, nil
}

// 转换多条核心回收记录
func mapRecords(records []gnrecycle.Record, names map[uint64]*string) ([]dto.DataItem, error) {
	result := make([]dto.DataItem, len(records))
	for index, record := range records {
		item, err := mapRecord(record, names)
		if err != nil {
			return nil, err
		}
		result[index] = item
	}

	return result, nil
}

// 转换单条核心回收记录
func mapRecord(record gnrecycle.Record, names map[uint64]*string) (dto.DataItem, error) {
	if !json.Valid(record.Data) {
		return dto.DataItem{}, exception.Core(fmt.Sprintf("回收记录 %d 快照 JSON 无效", record.ID))
	}
	userID := recordUserID(record)
	var userName *string
	if userID != nil {
		userName = names[*userID]
	}

	return dto.DataItem{
		ID:         record.ID,
		CreateTime: record.CreateTime,
		UpdateTime: record.UpdateTime,
		Count:      record.Count,
		Data:       append(json.RawMessage(nil), record.Data...),
		URL:        stringValue(record.Source),
		Params:     paramsJSON(record.Params),
		UserID:     userID,
		UserName:   userName,
		EntityInfo: dto.EntityInfo{
			DataSourceName: record.DatabaseGroup,
			Entity:         record.TableName,
		},
	}, nil
}

// 提取去重后的后台用户 ID
func recordUserIDs(records []gnrecycle.Record) []uint64 {
	result := make([]uint64, 0, len(records))
	seen := make(map[uint64]struct{}, len(records))
	for _, record := range records {
		id := recordUserID(record)
		if id == nil {
			continue
		}
		if _, exists := seen[*id]; exists {
			continue
		}
		seen[*id] = struct{}{}
		result = append(result, *id)
	}

	return result
}

// 解析后台操作人 ID
func recordUserID(record gnrecycle.Record) *uint64 {
	if stringValue(record.OperatorType) != "admin" || record.OperatorID == nil {
		return nil
	}
	id, err := strconv.ParseUint(strings.TrimSpace(*record.OperatorID), 10, 64)
	if err != nil || id == 0 {
		return nil
	}

	return &id
}

// 校验并按输入顺序去重恢复 ID
func normalizeRestoreIDs(request *dto.DataRestoreRequest) ([]uint64, error) {
	if request == nil || len(request.IDs) == 0 {
		return nil, exception.Validate("ID不能为空")
	}
	if len(request.IDs) > maxRestoreCount {
		return nil, exception.Validate("不能超过 500")
	}
	result := make([]uint64, 0, len(request.IDs))
	seen := make(map[uint64]struct{}, len(request.IDs))
	for _, id := range request.IDs {
		if id == 0 {
			return nil, exception.Validate("ID无效")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}

	return result, nil
}

// 返回合法对象参数或空对象
func paramsJSON(value *string) json.RawMessage {
	if value == nil {
		return json.RawMessage("{}")
	}
	trimmed := strings.TrimSpace(*value)
	if len(trimmed) < 2 || trimmed[0] != '{' || !json.Valid([]byte(trimmed)) {
		return json.RawMessage("{}")
	}

	return append(json.RawMessage(nil), trimmed...)
}

// 解引用可空字符串
func stringValue(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

// 计算本地当天零点前的保留边界
func recycleCutoff(now time.Time, days int) time.Time {
	year, month, day := now.Date()

	return time.Date(year, month, day, 0, 0, 0, 0, now.Location()).AddDate(0, 0, -days)
}
