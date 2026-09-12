//go:build !wasip1

package sdk

import (
	"context"
	"encoding/json"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

func callHostABI(context.Context, int64, string, json.RawMessage) (json.RawMessage, error) {
	return nil, protocol.NewError(protocol.ErrorHostCallFailed, "宿主调用仅在 WASI 插件中可用")
}
