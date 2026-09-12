package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"html"
	"strings"
	"sync"

	"github.com/gogf/gf/v2/os/gcache"
	"github.com/microcosm-cc/bluemonday"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/modules/base"
	"github.com/toothdy/cool-admin-go-next/modules/base/entity"
)

const (
	paramCachePrefix  = "param:"
	paramHTMLTemplate = "<html><title>@title</title><body>@content</body></html>"
)

// 参数富文本输出的白名单清洗策略,包级创建一次,创建后并发安全
var paramHTMLPolicy = bluemonday.UGCPolicy()

type paramCacheEntry struct {
	ID       uint64 `orm:"id"`
	KeyName  string `orm:"keyName"`
	Name     string `orm:"name"`
	Data     string `orm:"data"`
	DataType int32  `orm:"dataType"`
}

// 参数查询、HTML 输出和变更缓存协调
type ParamService struct {
	*gnservice.Base[entity.Param, uint64]
	allowKeys map[string]struct{}
	cache     *gcache.Cache
	dirty     map[string]struct{}
	mu        sync.Mutex
}

// 使用私有内存缓存的参数服务
func NewParam(baseService *gnservice.Base[entity.Param, uint64], config base.Config) *ParamService {
	allowKeys := make(map[string]struct{}, len(config.AllowKeys))
	for _, key := range config.AllowKeys {
		allowKeys[key] = struct{}{}
	}

	return &ParamService{
		Base:      baseService,
		allowKeys: allowKeys,
		cache:     gcache.New(),
		dirty:     make(map[string]struct{}),
	}
}

// 按键返回已按 dataType 解析的参数值
func (service *ParamService) DataByKey(ctx context.Context, key string) (any, error) {
	record, err := service.paramByKey(ctx, key)
	if err != nil || record == nil {
		return nil, err
	}

	switch record.DataType {
	case 0:
		var value any
		if json.Unmarshal([]byte(record.Data), &value) == nil {
			return value, nil
		}
		return record.Data, nil
	case 1:
		return record.Data, nil
	case 2:
		return strings.Split(record.Data, ","), nil
	default:
		return nil, nil
	}
}

// 校验 App 公开键后返回参数值
func (service *ParamService) AppDataByKey(ctx context.Context, key string) (any, error) {
	if _, allowed := service.allowKeys[key]; !allowed {
		return nil, exception.Comm("非法操作")
	}

	return service.DataByKey(ctx, key)
}

// 按键返回清洗后的 HTML 响应
func (service *ParamService) HTMLByKey(ctx context.Context, key string) (gnctrl.HTMLResponse, error) {
	record, err := service.paramByKey(ctx, key)
	if err != nil {
		return "", err
	}
	if record == nil {
		return gnctrl.HTMLResponse(strings.Replace(paramHTMLTemplate, "@content", "key notfound", 1)), nil
	}

	// 标题转义、正文白名单清洗,防止存储型 XSS
	return gnctrl.HTMLResponse(strings.NewReplacer(
		"@title", html.EscapeString(record.Name),
		"@content", paramHTMLPolicy.Sanitize(record.Data),
	).Replace(paramHTMLTemplate)), nil
}

// 校验公开键后返回清洗后的 HTML 页面
func (service *ParamService) PublicHTMLByKey(ctx context.Context, key string) (gnctrl.HTMLResponse, error) {
	if _, allowed := service.allowKeys[key]; !allowed {
		return "", exception.Comm("非法操作")
	}

	return service.HTMLByKey(ctx, key)
}

// 新增参数并失效相关缓存
func (service *ParamService) Add(
	ctx context.Context,
	input gnservice.AddInput[entity.Param],
) (gnservice.AddResult[uint64], error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	result, err := service.Base.Add(ctx, input)
	if err != nil {
		return gnservice.AddResult[uint64]{}, err
	}
	rows, err := service.paramsByIDs(ctx, addResultIDs(result))
	if err != nil {
		return gnservice.AddResult[uint64]{}, err
	}
	if err = service.markParamCacheDirty(ctx, rows); err != nil {
		return gnservice.AddResult[uint64]{}, err
	}

	return result, nil
}

// 更新参数并失效旧键与新键
func (service *ParamService) Update(
	ctx context.Context,
	input gnservice.UpdateInput[entity.Param, uint64],
) error {
	service.mu.Lock()
	defer service.mu.Unlock()

	ids := updateInputIDs(input)
	oldRows, err := service.paramsByIDs(ctx, ids)
	if err != nil {
		return err
	}
	if err = service.Base.Update(ctx, input); err != nil {
		return err
	}
	newRows, err := service.paramsByIDs(ctx, ids)
	if err != nil {
		return err
	}

	affectedRows := append(append(make([]paramCacheEntry, 0, len(oldRows)+len(newRows)), oldRows...), newRows...)

	return service.markParamCacheDirty(ctx, affectedRows)
}

// 删除参数并失效旧键缓存
func (service *ParamService) Delete(ctx context.Context, input gnservice.DeleteInput[uint64]) error {
	service.mu.Lock()
	defer service.mu.Unlock()

	oldRows, err := service.paramsByIDs(ctx, input.IDs())
	if err != nil {
		return err
	}
	if err = service.Base.Delete(ctx, input); err != nil {
		return err
	}

	return service.markParamCacheDirty(ctx, oldRows)
}

func (service *ParamService) paramByKey(ctx context.Context, key string) (*paramCacheEntry, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	cacheKey := paramCacheKey(key)
	_, isDirty := service.dirty[cacheKey]
	if !isDirty {
		value, err := service.cache.Get(ctx, cacheKey)
		if err != nil {
			return nil, exception.WrapCore(err, "读取参数缓存")
		}
		if value != nil {
			record := &paramCacheEntry{}
			if err = value.Scan(record); err != nil {
				return nil, exception.WrapCore(err, "解析参数缓存")
			}

			return record, nil
		}
	}

	record, err := service.queryParamByKey(ctx, key)
	if err != nil || record == nil {
		return record, err
	}
	if !isDirty {
		if err = service.cache.Set(ctx, cacheKey, *record, 0); err != nil {
			return nil, exception.WrapCore(err, "回填参数缓存")
		}
	}

	return record, nil
}

func (service *ParamService) queryParamByKey(ctx context.Context, key string) (*paramCacheEntry, error) {
	model, err := service.Base.Model(ctx)
	if err != nil {
		return nil, err
	}
	var record *paramCacheEntry
	err = model.
		Fields("id", "keyName", "name", "data", "dataType").
		Where("keyName", key).
		Scan(&record)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, exception.WrapCore(err, "查询参数失败")
	}

	return record, nil
}

func (service *ParamService) paramsByIDs(ctx context.Context, ids []uint64) ([]paramCacheEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	model, err := service.Base.Model(ctx)
	if err != nil {
		return nil, err
	}
	var rows []paramCacheEntry
	err = model.
		Fields("id", "keyName", "name", "data", "dataType").
		WhereIn("id", ids).
		OrderAsc("id").
		Scan(&rows)
	if err != nil {
		return nil, exception.WrapCore(err, "查询参数失败")
	}

	return rows, nil
}

func (service *ParamService) markParamCacheDirty(ctx context.Context, rows []paramCacheEntry) error {
	keys := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		key := paramCacheKey(row.KeyName)
		keys[key] = struct{}{}
		service.dirty[key] = struct{}{}
	}
	if len(keys) > 0 {
		cacheKeys := make([]any, 0, len(keys))
		for key := range keys {
			cacheKeys = append(cacheKeys, key)
		}
		if _, err := service.cache.Remove(ctx, cacheKeys...); err != nil {
			return exception.WrapCore(err, "失效参数缓存")
		}
	}

	return nil
}

func addResultIDs(result gnservice.AddResult[uint64]) []uint64 {
	if result.IsMany() {
		return result.Many()
	}

	return []uint64{result.One()}
}

func updateInputIDs(input gnservice.UpdateInput[entity.Param, uint64]) []uint64 {
	if !input.IsMany() {
		return []uint64{input.One().ID()}
	}
	items := input.Many()
	ids := make([]uint64, len(items))
	for index, item := range items {
		ids[index] = item.ID()
	}

	return ids
}

func paramCacheKey(key string) string {
	return paramCachePrefix + key
}
