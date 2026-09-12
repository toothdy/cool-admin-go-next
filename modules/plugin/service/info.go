package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"reflect"
	"unicode/utf8"

	"github.com/toothdy/cool-admin-go-next/cool-next/auth"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/cool-next/db"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/bundle"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/host/manager"
	baseservice "github.com/toothdy/cool-admin-go-next/modules/base/service"
	"github.com/toothdy/cool-admin-go-next/modules/plugin/dto"
	"github.com/toothdy/cool-admin-go-next/modules/plugin/entity"
)

// 插件后台变更与本节点热切换协调服务
type InfoService struct {
	*gnservice.Base[entity.Info, uint64]
	store      *Store
	manager    *manager.Manager
	runtime    *db.Runtime
	permission *baseservice.PermissionService
}

// 创建插件信息服务
func NewInfo(
	infoBase *gnservice.Base[entity.Info, uint64],
	store *Store,
	pluginManager *manager.Manager,
	runtime *db.Runtime,
	permission *baseservice.PermissionService,
) (*InfoService, error) {
	if infoBase.Descriptor().Table() != "plugin_info" {
		return nil, exception.Core("插件信息服务依赖无效")
	}
	if store.runtime != runtime {
		return nil, exception.Core("插件信息服务数据库 Runtime 不匹配")
	}

	return &InfoService{
		Base:       infoBase,
		store:      store,
		manager:    pluginManager,
		runtime:    runtime,
		permission: permission,
	}, nil
}

// 安装新插件或升级同 key 插件
func (service *InfoService) Install(ctx context.Context, request *dto.InstallRequest) (dto.InstallResult, error) {
	if err := service.requireAdmin(ctx); err != nil {
		return dto.InstallResult{}, err
	}
	if request == nil || request.File == nil || request.File.FileHeader == nil {
		return dto.InstallResult{}, exception.Validate("插件安装文件不能为空")
	}
	packageData, err := service.readPackage(request.File.Size, request.File.Open)
	if err != nil {
		return dto.InstallResult{}, err
	}
	parsed, err := bundle.ReadWithLimits(packageData, service.store.limits)
	if err != nil {
		return dto.InstallResult{}, exception.WrapValidate(err, "插件安装包无效")
	}
	logo, readme, err := packageAssets(parsed)
	if err != nil {
		return dto.InstallResult{}, err
	}
	current, err := service.store.findInfoByKey(ctx, parsed.Manifest.Key, false)
	if err != nil {
		return dto.InstallResult{}, err
	}
	status, revision, rawConfig, err := nextInstallState(current, parsed.Manifest.Config)
	if err != nil {
		return dto.InstallResult{}, err
	}
	normalizedConfig, err := normalizeConfig(rawConfig, service.store.mode, service.store.baseDir)
	if err != nil {
		return dto.InstallResult{}, exception.WrapValidate(err, "插件配置无效")
	}
	desired := desiredFromPackage(parsed, current, status, revision, normalizedConfig)
	stored := manager.Artifact{
		Version:  parsed.Manifest.Version,
		SHA256:   parsed.SHA256,
		Size:     parsed.Size,
		Manifest: parsed.Manifest,
		Data:     append([]byte(nil), packageData...),
	}
	if current != nil {
		stored.PluginID = current.ID
	}
	candidate, err := service.manager.Prepare(ctx, desired, stored)
	if err != nil {
		return dto.InstallResult{}, err
	}
	defer candidate.Close(context.Background())

	result := dto.InstallResult{
		Key: parsed.Manifest.Key, Version: parsed.Manifest.Version, SHA256: parsed.SHA256, Revision: revision,
	}
	if current != nil {
		result.ID = current.ID
		if current.SHA256 == parsed.SHA256 {
			result.Revision = current.Revision

			return result, nil
		}
		if current.Version == parsed.Manifest.Version && !request.Force {
			result.NeedsConfirmation = true
			result.Revision = current.Revision

			return result, nil
		}
	}

	mutation := installMutation{
		PackageData: packageData,
		Package:     parsed,
		Config:      rawConfig,
		Logo:        logo,
		Readme:      readme,
		Status:      status,
		Revision:    revision,
	}
	var committed committedMutation
	if err = service.runtime.Runner().Within(ctx, func(txCtx context.Context) error {
		var commitErr error
		committed, commitErr = service.store.commitInstall(txCtx, current, mutation)

		return commitErr
	}); err != nil {
		return dto.InstallResult{}, err
	}
	result.ID = committed.PluginID
	if desired.Enabled && len(committed.Disabled) > 0 {
		err = service.manager.PublishReplacing(candidate, committed.Disabled)
	} else {
		err = service.manager.Publish(candidate)
	}

	return result, err
}

// 适配公共前端的插件安装响应
func (service *InfoService) InstallCompatible(ctx context.Context, request *dto.InstallRequest) (any, error) {
	result, err := service.Install(ctx, request)
	if err != nil {
		return nil, err
	}
	if result.NeedsConfirmation {
		return map[string]any{
			"type":    1,
			"message": "插件已存在，继续安装将覆盖",
		}, nil
	}

	return nil, nil
}

// 更新插件配置或启用状态
func (service *InfoService) Update(ctx context.Context, request *dto.UpdateRequest) (dto.UpdateResult, error) {
	if err := service.requireAdmin(ctx); err != nil {
		return dto.UpdateResult{}, err
	}
	if request == nil || request.ID == 0 || request.Status == nil && request.Config == nil {
		return dto.UpdateResult{}, exception.Validate("插件更新参数无效")
	}
	if request.Status != nil && *request.Status != entity.StatusDisabled && *request.Status != entity.StatusEnabled {
		return dto.UpdateResult{}, exception.Validate("插件状态只允许 0 或 1")
	}
	current, err := service.store.findInfoByID(ctx, request.ID, false)
	if err != nil {
		return dto.UpdateResult{}, err
	}
	if current == nil {
		return dto.UpdateResult{}, exception.Validate("插件不存在")
	}
	if _, err = desiredEnabled(current.Status); err != nil {
		return dto.UpdateResult{}, err
	}
	status := current.Status
	if request.Status != nil {
		status = *request.Status
	}
	rawConfig := cloneRawConfig(current.Config)
	if request.Config != nil {
		rawConfig = cloneRawConfig(*request.Config)
		if rawConfig == nil {
			return dto.UpdateResult{}, exception.Validate("插件配置必须是 JSON object")
		}
	}
	result := dto.UpdateResult{ID: current.ID, Key: current.KeyName, Status: status, Revision: current.Revision}
	if status == current.Status && request.Config == nil {
		return result, nil
	}
	if status == current.Status && reflect.DeepEqual(rawConfig, current.Config) {
		return result, nil
	}
	if current.Revision == math.MaxUint64 {
		return dto.UpdateResult{}, exception.Core("插件 revision 已达到上限")
	}
	normalizedConfig, err := normalizeConfig(rawConfig, service.store.mode, service.store.baseDir)
	if err != nil {
		return dto.UpdateResult{}, exception.WrapValidate(err, "插件配置无效")
	}
	desired, err := service.store.LoadDesired(ctx, current.ID)
	if err != nil {
		return dto.UpdateResult{}, err
	}
	desired.Enabled = status == entity.StatusEnabled
	desired.Config = normalizedConfig
	desired.Revision = current.Revision + 1
	if !desired.Enabled {
		if err = service.runtime.Runner().Within(ctx, func(txCtx context.Context) error {
			_, commitErr := service.store.commitUpdate(txCtx, current, status, rawConfig)

			return commitErr
		}); err != nil {
			return dto.UpdateResult{}, err
		}
		err = service.manager.Disable(desired)
		result.Revision = desired.Revision

		return result, err
	}
	stored, err := service.store.LoadArtifact(ctx, current.ActiveArtifactID)
	if err != nil {
		return dto.UpdateResult{}, err
	}
	candidate, err := service.manager.Prepare(ctx, desired, stored)
	if err != nil {
		return dto.UpdateResult{}, err
	}
	defer candidate.Close(context.Background())

	var committed committedMutation
	if err = service.runtime.Runner().Within(ctx, func(txCtx context.Context) error {
		var commitErr error
		committed, commitErr = service.store.commitUpdate(txCtx, current, status, rawConfig)

		return commitErr
	}); err != nil {
		return dto.UpdateResult{}, err
	}
	if desired.Enabled && len(committed.Disabled) > 0 {
		err = service.manager.PublishReplacing(candidate, committed.Disabled)
	} else {
		err = service.manager.Publish(candidate)
	}
	result.Revision = desired.Revision

	return result, err
}

// 批量卸载插件
func (service *InfoService) Delete(ctx context.Context, request *dto.DeleteRequest) error {
	if err := service.requireAdmin(ctx); err != nil {
		return err
	}
	if request == nil {
		return exception.Validate("插件卸载参数无效")
	}
	ids := auth.NormalizeIDs(request.IDs)
	if len(ids) == 0 {
		return exception.Validate("插件卸载 ID 不能为空")
	}
	current := make([]*managedInfoRow, 0, len(ids))
	for _, id := range ids {
		item, err := service.store.findInfoForDelete(ctx, id, false)
		if err != nil {
			return err
		}
		if item == nil {
			return exception.Validate("插件不存在")
		}
		current = append(current, item)
	}
	if err := service.runtime.Runner().Within(ctx, func(txCtx context.Context) error {
		return service.store.commitDelete(txCtx, current)
	}); err != nil {
		return err
	}
	var removeErr error
	for _, item := range current {
		removeErr = errors.Join(removeErr, service.manager.Remove(item.KeyName))
	}

	return removeErr
}

// 返回插件详情
func (service *InfoService) Detail(ctx context.Context, request *dto.InfoRequest) (dto.InfoResult, error) {
	if request == nil || request.ID == 0 {
		return dto.InfoResult{}, exception.Validate("插件 ID 无效")
	}
	identity, err := auth.Admin(ctx)
	if err != nil {
		return dto.InfoResult{}, err
	}
	isAdmin, err := service.permission.IsAdmin(ctx, identity.RoleIDs())
	if err != nil {
		return dto.InfoResult{}, err
	}
	fields := []any{
		"id", "createTime", "updateTime", "name", "description", "keyName", "hook", "singleton",
		"readme", "version", "logo", "author", "status", "runtimeABI", "revision",
	}
	if isAdmin {
		fields = append(fields, "config")
	}
	model, err := service.store.model(ctx, service.store.infoDescriptor.Table())
	if err != nil {
		return dto.InfoResult{}, err
	}
	var result dto.InfoResult
	if err = model.Fields(fields...).Where("id", request.ID).Scan(&result); errors.Is(err, sql.ErrNoRows) {
		return dto.InfoResult{}, nil
	} else if err != nil {
		return dto.InfoResult{}, exception.WrapCore(err, "查询插件详情失败")
	}

	return result, nil
}

func (service *InfoService) requireAdmin(ctx context.Context) error {
	identity, err := auth.Admin(ctx)
	if err != nil {
		return err
	}
	isAdmin, err := service.permission.IsAdmin(ctx, identity.RoleIDs())
	if err != nil {
		return err
	}
	if !isAdmin {
		return exception.Comm("仅平台管理员可以管理插件", http.StatusForbidden)
	}

	return nil
}

func (service *InfoService) readPackage(
	declaredSize int64,
	open func() (multipart.File, error),
) ([]byte, error) {
	maximum := service.store.limits.MaxPackageBytes
	if declaredSize < 0 || declaredSize > maximum {
		return nil, exception.Validate("插件安装包超过大小限制")
	}
	reader, err := open()
	if err != nil {
		return nil, exception.WrapValidate(err, "打开插件安装包失败")
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, exception.WrapValidate(err, "读取插件安装包失败")
	}
	if int64(len(content)) > maximum {
		return nil, exception.Validate("插件安装包超过大小限制")
	}

	return content, nil
}

func nextInstallState(
	current *managedInfoRow,
	defaults map[string]json.RawMessage,
) (int32, uint64, map[string]json.RawMessage, error) {
	if current == nil {
		return entity.StatusEnabled, 1, cloneRawConfig(defaults), nil
	}
	if _, err := desiredEnabled(current.Status); err != nil {
		return 0, 0, nil, err
	}
	if current.Revision == math.MaxUint64 {
		return 0, 0, nil, exception.Core("插件 revision 已达到上限")
	}

	return current.Status, current.Revision + 1, mergeConfig(defaults, current.Config), nil
}

func desiredFromPackage(
	parsed bundle.Package,
	current *managedInfoRow,
	status int32,
	revision uint64,
	config map[string]json.RawMessage,
) manager.DesiredPlugin {
	desired := manager.DesiredPlugin{
		Key:        parsed.Manifest.Key,
		Hook:       parsed.Manifest.Hook,
		Singleton:  parsed.Manifest.Singleton,
		Version:    parsed.Manifest.Version,
		Enabled:    status == entity.StatusEnabled,
		Manifest:   parsed.Manifest,
		Config:     config,
		RuntimeABI: parsed.Manifest.Runtime.ABI,
		SHA256:     parsed.SHA256,
		Revision:   revision,
	}
	if current != nil {
		desired.ID = current.ID
	}

	return desired
}

func packageAssets(parsed bundle.Package) ([]byte, *string, error) {
	logo := append([]byte(nil), parsed.Files[parsed.Manifest.Logo]...)
	if parsed.Manifest.Readme == "" {
		return logo, nil, nil
	}
	content := parsed.Files[parsed.Manifest.Readme]
	if !utf8.Valid(content) {
		return nil, nil, exception.Validate("插件 README 必须是合法 UTF-8")
	}
	readme := string(content)

	return logo, &readme, nil
}
