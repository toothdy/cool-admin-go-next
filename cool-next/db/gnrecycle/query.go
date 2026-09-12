package gnrecycle

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/crud"
)

var pageOrderColumns = map[string]string{
	"id":         "id",
	"createTime": "createTime",
	"updateTime": "updateTime",
	"count":      "count",
}

// 回收站是否启用
func (store *Store) Enabled() bool {
	return store != nil && store.config.SoftDelete
}

// 按 ID 查询回收记录
func (store *Store) Info(ctx context.Context, id uint64) (*Record, error) {
	if err := store.validate(); err != nil {
		return nil, err
	}
	if !store.Enabled() {
		return nil, nil
	}
	if id == 0 {
		return nil, exception.Validate("回收记录 ID 无效")
	}
	var record Record
	if err := store.runtime.DB().Model(TableName).
		Ctx(ctx).
		Unscoped().
		Where(store.recordDescriptor.Primary().Column(), id).
		Scan(&record); errors.Is(err, sql.ErrNoRows) {
		return nil, recordNotFoundError(id)
	} else if err != nil {
		return nil, exception.WrapCore(err, "查询回收记录失败")
	}
	if record.ID == 0 {
		return nil, recordNotFoundError(id)
	}

	return &record, nil
}

func recordNotFoundError(id uint64) error {
	return exception.WrapCore(ErrRecordNotFound, "回收记录不存在: "+strconv.FormatUint(id, 10))
}

// 分页查询回收记录
func (store *Store) Page(ctx context.Context, input PageInput) (PageResult, error) {
	if err := store.validate(); err != nil {
		return PageResult{}, err
	}
	page, size, order, sortDirection, err := store.normalizePageInput(input)
	if err != nil {
		return PageResult{}, err
	}
	result := PageResult{
		List: make([]Record, 0),
		Pagination: Pagination{
			Page: page,
			Size: size,
		},
	}
	if !store.Enabled() {
		return result, nil
	}
	model := store.runtime.DB().Model(TableName).Ctx(ctx).Unscoped()
	model = applyPageFilter(model, input.Keyword, input.OperatorIDs)
	if sortDirection == "asc" {
		model = model.OrderAsc(order)
	} else {
		model = model.OrderDesc(order)
	}
	model = model.Limit((page-1)*size, size)
	var total int
	if err = model.ScanAndCount(&result.List, &total, false); err != nil {
		return PageResult{}, exception.WrapCore(err, "分页查询回收记录失败")
	}
	result.Pagination.Total = int64(total)

	return result, nil
}

// 物理删除截止时间前的回收记录
func (store *Store) DeleteExpired(ctx context.Context, cutoff time.Time) (int64, error) {
	if err := store.validate(); err != nil {
		return 0, err
	}
	if !store.Enabled() {
		return 0, nil
	}
	if cutoff.IsZero() {
		return 0, exception.Validate("回收记录清理截止时间无效")
	}
	result, err := store.runtime.DB().Model(TableName).
		Ctx(ctx).
		Unscoped().
		WhereLT("createTime", cutoff).
		Delete()
	if err != nil {
		return 0, exception.WrapCore(err, "清理过期回收记录失败")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, exception.WrapCore(err, "读取过期回收记录清理行数失败")
	}

	return affected, nil
}

func (store *Store) normalizePageInput(input PageInput) (int, int, string, string, error) {
	page := input.Page
	if page == 0 {
		page = 1
	}
	if page < 1 {
		return 0, 0, "", "", exception.Validate("page 必须是正整数")
	}
	pageSize := store.config.PageSize
	pageLimit := store.config.PageLimit
	if pageSize <= 0 || pageLimit <= 0 {
		if store.Enabled() {
			return 0, 0, "", "", exception.Core("回收站 CRUD 分页配置无效")
		}
		defaults := crud.DefaultConfig()
		pageSize = defaults.PageSize
		pageLimit = defaults.PageLimit
	}
	size := input.Size
	if size == 0 {
		size = pageSize
	}
	if size < 1 {
		return 0, 0, "", "", exception.Validate("size 必须是正整数")
	}
	if size > pageLimit {
		return 0, 0, "", "", exception.Validate("size 超过 " + strconv.Itoa(pageLimit) + " 上限")
	}
	if page-1 > math.MaxInt/size {
		return 0, 0, "", "", exception.Validate("page 超出整数范围")
	}
	requestedOrder := strings.TrimSpace(input.Order)
	requestedSort := strings.ToLower(strings.TrimSpace(input.Sort))
	if requestedOrder == "" && requestedSort == "" {
		return page, size, "id", "desc", nil
	}
	if requestedOrder == "" || requestedSort == "" {
		return 0, 0, "", "", exception.Validate("order 与 sort 必须同时提供")
	}
	order, exists := pageOrderColumns[requestedOrder]
	if !exists {
		return 0, 0, "", "", exception.Validate("order 不支持")
	}
	if requestedSort != "asc" && requestedSort != "desc" {
		return 0, 0, "", "", exception.Validate("sort 只支持 asc 或 desc")
	}

	return page, size, order, requestedSort, nil
}

func applyPageFilter(model *gdb.Model, keyword string, adminOperatorIDs []uint64) *gdb.Model {
	operatorIDs := uniqueOperatorIDs(adminOperatorIDs)
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		if len(operatorIDs) == 0 {
			return model
		}

		return model.Where("operatorType", "admin").WhereIn("operatorId", operatorIDs)
	}
	filter := model.Builder().WhereLike("source", "%"+keyword+"%")
	if len(operatorIDs) > 0 {
		operatorFilter := model.Builder().
			Where("operatorType", "admin").
			WhereIn("operatorId", operatorIDs)
		filter = filter.WhereOr(operatorFilter)
	}

	return model.Where(filter)
}

func uniqueOperatorIDs(ids []uint64) []string {
	result := make([]string, 0, len(ids))
	seen := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, strconv.FormatUint(id, 10))
	}

	return result
}
