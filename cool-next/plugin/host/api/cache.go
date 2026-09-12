package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/gogf/gf/v2/container/gvar"
	"github.com/gogf/gf/v2/os/gcache"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

const (
	cacheKeyPrefix   = "cool:plugin:cache:"
	maxCacheKeyBytes = 256
)

// Host API 使用的最小 GoFrame 缓存能力
type CacheStore interface {
	Get(context.Context, any) (*gvar.Var, error)
	Set(context.Context, any, any, time.Duration) error
	Remove(context.Context, ...any) (*gvar.Var, error)
}

type cacheGetRequest struct {
	Key string `json:"key"`
}

type cacheGetResponse struct {
	Found bool            `json:"found"`
	Value json.RawMessage `json:"value"`
}

type cacheSetRequest struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
	TTLMS int64           `json:"ttlMs,omitempty"`
}

type cacheDeleteRequest struct {
	Key string `json:"key"`
}

type cacheDeleteResponse struct {
	Deleted bool `json:"deleted"`
}

type cacheHandler struct {
	store CacheStore
}

func cacheOperations(_ Config, dependencies Dependencies) []operation {
	store := dependencies.Cache
	if store == nil {
		store = gcache.New()
	}
	handler := &cacheHandler{store: store}

	return []operation{
		{name: "cache.get", handle: handler.handleGet},
		{name: "cache.set", handle: handler.handleSet},
		{name: "cache.delete", handle: handler.handleDelete},
	}
}

func (handler *cacheHandler) handleGet(
	ctx context.Context,
	_ int64,
	payload json.RawMessage,
) (json.RawMessage, error) {
	key, err := pluginKey(ctx)
	if err != nil {
		return nil, err
	}

	return handler.get(ctx, key, payload)
}

func (handler *cacheHandler) handleSet(
	ctx context.Context,
	_ int64,
	payload json.RawMessage,
) (json.RawMessage, error) {
	key, err := pluginKey(ctx)
	if err != nil {
		return nil, err
	}

	return handler.set(ctx, key, payload)
}

func (handler *cacheHandler) handleDelete(
	ctx context.Context,
	_ int64,
	payload json.RawMessage,
) (json.RawMessage, error) {
	key, err := pluginKey(ctx)
	if err != nil {
		return nil, err
	}

	return handler.delete(ctx, key, payload)
}

func (handler *cacheHandler) get(
	ctx context.Context,
	pluginKeyValue string,
	payload json.RawMessage,
) (json.RawMessage, error) {
	var request cacheGetRequest
	if err := decodeCacheRequest(payload, &request); err != nil {
		return nil, protocol.WrapError(protocol.ErrorInvalidInput, "缓存读取参数无效", err)
	}
	key, err := namespacedCacheKey(pluginKeyValue, request.Key)
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrorInvalidInput, "缓存 key 无效", err)
	}
	value, err := handler.store.Get(cacheContext(ctx), key)
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrorHostCallFailed, "读取插件缓存失败", err)
	}
	response := cacheGetResponse{Value: json.RawMessage("null")}
	if value != nil && !value.IsNil() {
		response.Value = append(json.RawMessage(nil), value.Bytes()...)
		if !json.Valid(response.Value) {
			return nil, protocol.NewError(protocol.ErrorHostCallFailed, "插件缓存值无效")
		}
		response.Found = true
	}

	return marshalCache(response)
}

func (handler *cacheHandler) set(
	ctx context.Context,
	pluginKeyValue string,
	payload json.RawMessage,
) (json.RawMessage, error) {
	var request cacheSetRequest
	if err := decodeCacheRequest(payload, &request); err != nil {
		return nil, protocol.WrapError(protocol.ErrorInvalidInput, "缓存写入参数无效", err)
	}
	key, err := namespacedCacheKey(pluginKeyValue, request.Key)
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrorInvalidInput, "缓存 key 无效", err)
	}
	if len(request.Value) == 0 || !json.Valid(request.Value) {
		return nil, protocol.NewError(protocol.ErrorInvalidInput, "缓存 value 必须是合法 JSON")
	}
	if request.TTLMS < 0 || request.TTLMS > math.MaxInt64/int64(time.Millisecond) {
		return nil, protocol.NewError(protocol.ErrorInvalidInput, "缓存 ttlMs 无效")
	}
	duration := time.Duration(request.TTLMS) * time.Millisecond
	value := append(json.RawMessage(nil), request.Value...)
	if err = handler.store.Set(cacheContext(ctx), key, value, duration); err != nil {
		return nil, protocol.WrapError(protocol.ErrorHostCallFailed, "写入插件缓存失败", err)
	}

	return json.RawMessage(`{}`), nil
}

func (handler *cacheHandler) delete(
	ctx context.Context,
	pluginKeyValue string,
	payload json.RawMessage,
) (json.RawMessage, error) {
	var request cacheDeleteRequest
	if err := decodeCacheRequest(payload, &request); err != nil {
		return nil, protocol.WrapError(protocol.ErrorInvalidInput, "缓存删除参数无效", err)
	}
	key, err := namespacedCacheKey(pluginKeyValue, request.Key)
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrorInvalidInput, "缓存 key 无效", err)
	}
	value, err := handler.store.Remove(cacheContext(ctx), key)
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrorHostCallFailed, "删除插件缓存失败", err)
	}

	return marshalCache(cacheDeleteResponse{Deleted: value != nil && !value.IsNil()})
}

func decodeCacheRequest(payload json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("缓存参数包含多余 JSON")
		}
		return err
	}

	return nil
}

func namespacedCacheKey(pluginKeyValue, key string) (string, error) {
	if strings.TrimSpace(pluginKeyValue) == "" {
		return "", errors.New("插件 key 不能为空")
	}
	if key == "" || len(key) > maxCacheKeyBytes {
		return "", fmt.Errorf("缓存 key 长度必须为 1 至 %d 字节", maxCacheKeyBytes)
	}
	for _, character := range key {
		if unicode.IsControl(character) {
			return "", errors.New("缓存 key 不能包含控制字符")
		}
	}

	return cacheKeyPrefix + pluginKeyValue + ":" + key, nil
}

func marshalCache(value any) (json.RawMessage, error) {
	result, err := json.Marshal(value)
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrorHostCallFailed, "编码插件缓存响应失败", err)
	}

	return result, nil
}

func cacheContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}

	return ctx
}
