package crud

import (
	"fmt"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

const (
	maxBodyLimit      int64 = 8 * 1024 * 1024
	maxMultipartBytes int64 = 101 * 1024 * 1024
	maxBatchLimit           = 1000
	maxPageLimit            = 10000
	maxListLimit            = 1000
	maxExportLimit          = 10000
)

// CRUD 配置
type Config struct {
	SoftDelete         bool  `json:"softDelete"`
	BodyLimit          int64 `json:"bodyLimit"`
	MultipartBodyLimit int64 `json:"multipartBodyLimit"`
	BatchLimit         int   `json:"batchLimit"`
	PageSize           int   `json:"pageSize"`
	PageLimit          int   `json:"pageLimit"`
	ListLimit          int   `json:"listLimit"`
	ExportLimit        int   `json:"exportLimit"`
}

// CRUD 默认配置
func DefaultConfig() Config {
	return Config{
		SoftDelete:         true,
		BodyLimit:          maxBodyLimit,
		MultipartBodyLimit: maxMultipartBytes,
		BatchLimit:         maxBatchLimit,
		PageSize:           15,
		PageLimit:          maxPageLimit,
		ListLimit:          maxListLimit,
		ExportLimit:        maxExportLimit,
	}
}

// CRUD 限制只能在框架硬上限内收紧
func (config Config) Validate() error {
	if config.BodyLimit <= 0 || config.BodyLimit > maxBodyLimit {
		return exception.Core(fmt.Sprintf("CRUD BodyLimit 必须在 1 到 %d 之间", maxBodyLimit))
	}
	if config.MultipartBodyLimit <= 0 || config.MultipartBodyLimit > maxMultipartBytes {
		return exception.Core(fmt.Sprintf("CRUD MultipartBodyLimit 必须在 1 到 %d 之间", maxMultipartBytes))
	}
	if config.BatchLimit <= 0 || config.BatchLimit > maxBatchLimit {
		return exception.Core(fmt.Sprintf("CRUD BatchLimit 必须在 1 到 %d 之间", maxBatchLimit))
	}
	if config.PageSize <= 0 || config.PageSize > config.PageLimit {
		return exception.Core("CRUD PageSize 必须在 1 到 PageLimit 之间")
	}
	if config.PageLimit <= 0 || config.PageLimit > maxPageLimit {
		return exception.Core(fmt.Sprintf("CRUD PageLimit 必须在 1 到 %d 之间", maxPageLimit))
	}
	if config.ListLimit <= 0 || config.ListLimit > maxListLimit {
		return exception.Core(fmt.Sprintf("CRUD ListLimit 必须在 1 到 %d 之间", maxListLimit))
	}
	if config.ExportLimit <= 0 || config.ExportLimit > maxExportLimit {
		return exception.Core(fmt.Sprintf("CRUD ExportLimit 必须在 1 到 %d 之间", maxExportLimit))
	}

	return nil
}
