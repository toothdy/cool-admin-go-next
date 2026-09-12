package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"os"
	"reflect"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/util/gmode"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
	"github.com/toothdy/cool-admin-go-next/cool-next/db"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/driver"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/bundle"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/host/manager"
	"github.com/toothdy/cool-admin-go-next/modules/plugin"
	"github.com/toothdy/cool-admin-go-next/modules/plugin/entity"
)

type desiredListRow struct {
	ID               uint64 `orm:"id"`
	KeyName          string `orm:"keyName"`
	Status           int32  `orm:"status"`
	ActiveArtifactID uint64 `orm:"activeArtifactID"`
	Revision         uint64 `orm:"revision"`
}

type desiredRow struct {
	ID               uint64                     `orm:"id"`
	KeyName          string                     `orm:"keyName"`
	Hook             *string                    `orm:"hook"`
	Singleton        bool                       `orm:"singleton"`
	Version          string                     `orm:"version"`
	Status           int32                      `orm:"status"`
	Manifest         map[string]json.RawMessage `orm:"manifest"`
	Config           map[string]json.RawMessage `orm:"config"`
	RuntimeABI       string                     `orm:"runtimeABI"`
	ActiveArtifactID uint64                     `orm:"activeArtifactID"`
	Revision         uint64                     `orm:"revision"`
}

type artifactMetadataRow struct {
	ID       uint64                     `orm:"id"`
	PluginID uint64                     `orm:"pluginId"`
	Version  string                     `orm:"version"`
	SHA256   string                     `orm:"sha256"`
	Manifest map[string]json.RawMessage `orm:"manifest"`
}

type artifactRow struct {
	ID          uint64                     `orm:"id"`
	PluginID    uint64                     `orm:"pluginId"`
	Version     string                     `orm:"version"`
	SHA256      string                     `orm:"sha256"`
	Size        int64                      `orm:"size"`
	Manifest    map[string]json.RawMessage `orm:"manifest"`
	PackageData []byte                     `orm:"packageData"`
}

type artifactOwnerRow struct {
	ID               uint64                     `orm:"id"`
	Manifest         map[string]json.RawMessage `orm:"manifest"`
	ActiveArtifactID uint64                     `orm:"activeArtifactID"`
}

type managedInfoRow struct {
	ID               uint64                     `orm:"id"`
	KeyName          string                     `orm:"keyName"`
	Hook             *string                    `orm:"hook"`
	Version          string                     `orm:"version"`
	Status           int32                      `orm:"status"`
	Config           map[string]json.RawMessage `orm:"config"`
	ActiveArtifactID uint64                     `orm:"activeArtifactID"`
	Revision         uint64                     `orm:"revision"`
	SHA256           string                     `orm:"-"`
}

type hookConflictRow struct {
	ID       uint64 `orm:"id"`
	KeyName  string `orm:"keyName"`
	Revision uint64 `orm:"revision"`
}

type artifactIdentityRow struct {
	ID       uint64 `orm:"id"`
	PluginID uint64 `orm:"pluginId"`
}

type installMutation struct {
	PackageData []byte
	Package     bundle.Package
	Config      map[string]json.RawMessage
	Logo        []byte
	Readme      *string
	Status      int32
	Revision    uint64
}

type committedMutation struct {
	PluginID   uint64
	ArtifactID uint64
	Disabled   []manager.DesiredPlugin
}

type doColumn struct {
	name  string
	value any
}

type semanticJSONNumber struct {
	value string
}

// 插件 Manager 的数据库只读存储
type Store struct {
	runtime            *db.Runtime
	infoDescriptor     gnentity.Descriptor[entity.Info, uint64]
	artifactDescriptor gnentity.Descriptor[entity.Artifact, uint64]
	limits             bundle.Limits
	mode               string
	baseDir            string
}

// 创建插件数据库 Store
func NewStore(
	runtime *db.Runtime,
	infoDescriptor gnentity.Descriptor[entity.Info, uint64],
	artifactDescriptor gnentity.Descriptor[entity.Artifact, uint64],
	config plugin.Config,
) (*Store, error) {
	if infoDescriptor.Table() != "plugin_info" || artifactDescriptor.Table() != "plugin_artifact" {
		return nil, exception.Core("插件 Store Descriptor 无效")
	}
	if config.MaxPackageBytes <= 0 || config.MaxPackageBytes == math.MaxInt64 || config.MaxUnpackedBytes == 0 ||
		config.MaxUnpackedBytes >= math.MaxInt64 || config.MaxEntries <= 0 {
		return nil, exception.Core("插件 Store 制品限制无效")
	}
	baseDir, err := os.Getwd()
	if err != nil {
		return nil, exception.WrapCore(err, "读取插件配置基础目录失败")
	}

	return &Store{
		runtime:            runtime,
		infoDescriptor:     infoDescriptor,
		artifactDescriptor: artifactDescriptor,
		limits: bundle.Limits{
			MaxPackageBytes:  config.MaxPackageBytes,
			MaxUnpackedBytes: config.MaxUnpackedBytes,
			MaxEntries:       config.MaxEntries,
		},
		mode:    gmode.Mode(),
		baseDir: baseDir,
	}, nil
}

// 轻量列出全部插件期望状态
func (store *Store) ListDesired(ctx context.Context) ([]manager.DesiredPlugin, error) {
	model, err := store.model(ctx, store.infoDescriptor.Table())
	if err != nil {
		return nil, err
	}
	var rows []desiredListRow
	if err = model.
		Fields("id", "keyName", "status", "activeArtifactID", "revision").
		OrderAsc("id").
		Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询插件期望状态列表失败")
	}
	result := make([]manager.DesiredPlugin, len(rows))
	for index, row := range rows {
		result[index] = manager.DesiredPlugin{
			ID:         row.ID,
			Key:        row.KeyName,
			Enabled:    row.Status == entity.StatusEnabled,
			ArtifactID: row.ActiveArtifactID,
			Revision:   row.Revision,
		}
	}

	return result, nil
}

// 加载单个插件的期望状态
func (store *Store) LoadDesired(ctx context.Context, pluginID uint64) (manager.DesiredPlugin, error) {
	if pluginID == 0 {
		return manager.DesiredPlugin{}, exception.Validate("插件 ID 无效")
	}
	model, err := store.model(ctx, store.infoDescriptor.Table())
	if err != nil {
		return manager.DesiredPlugin{}, err
	}
	var row desiredRow
	if err = model.
		Fields(
			"id", "keyName", "hook", "singleton", "version", "status", "manifest", "config",
			"runtimeABI", "activeArtifactID", "revision",
		).
		Where("id", pluginID).
		Scan(&row); errors.Is(err, sql.ErrNoRows) {
		return manager.DesiredPlugin{}, exception.Core("插件期望状态不存在")
	} else if err != nil {
		return manager.DesiredPlugin{}, exception.WrapCore(err, "查询插件期望状态失败")
	}
	if row.ID == 0 || row.ActiveArtifactID == 0 {
		return manager.DesiredPlugin{}, exception.Core("插件期望状态或当前制品无效")
	}

	metadata, err := store.loadArtifact(ctx, row.ActiveArtifactID)
	if err != nil {
		return manager.DesiredPlugin{}, err
	}
	if metadata.PluginID != row.ID {
		return manager.DesiredPlugin{}, exception.Core("插件当前制品所属插件不匹配")
	}
	if metadata.Version != row.Version {
		return manager.DesiredPlugin{}, exception.Core("插件当前制品版本不匹配")
	}
	infoManifest, err := parseStoredManifest(row.Manifest)
	if err != nil {
		return manager.DesiredPlugin{}, exception.WrapCore(err, "解析插件期望 Manifest 失败")
	}
	artifactManifest, err := parseStoredManifest(metadata.Manifest)
	if err != nil {
		return manager.DesiredPlugin{}, exception.WrapCore(err, "解析插件制品 Manifest 失败")
	}
	if !sameManifest(infoManifest, artifactManifest) {
		return manager.DesiredPlugin{}, exception.Core("插件期望 Manifest 与当前制品不匹配")
	}
	hook := ""
	if row.Hook != nil {
		hook = *row.Hook
	}
	if row.KeyName != infoManifest.Key || hook != infoManifest.Hook || row.Singleton != infoManifest.Singleton ||
		row.Version != infoManifest.Version || row.RuntimeABI != infoManifest.Runtime.ABI {
		return manager.DesiredPlugin{}, exception.Core("插件期望状态与 Manifest 不匹配")
	}
	config, err := normalizeConfig(row.Config, store.mode, store.baseDir)
	if err != nil {
		return manager.DesiredPlugin{}, exception.WrapCore(err, "规范化插件配置失败")
	}
	enabled, err := desiredEnabled(row.Status)
	if err != nil {
		return manager.DesiredPlugin{}, err
	}
	return manager.DesiredPlugin{
		ID:         row.ID,
		Key:        row.KeyName,
		Hook:       hook,
		Singleton:  row.Singleton,
		Version:    row.Version,
		Enabled:    enabled,
		Manifest:   infoManifest,
		Config:     config,
		RuntimeABI: row.RuntimeABI,
		ArtifactID: row.ActiveArtifactID,
		SHA256:     metadata.SHA256,
		Revision:   row.Revision,
	}, nil
}

// 按 ID 读取并重新校验当前插件制品
func (store *Store) LoadArtifact(ctx context.Context, artifactID uint64) (manager.Artifact, error) {
	if artifactID == 0 {
		return manager.Artifact{}, exception.Validate("插件制品 ID 无效")
	}
	model, err := store.model(ctx, store.artifactDescriptor.Table())
	if err != nil {
		return manager.Artifact{}, err
	}
	var row artifactRow
	if err = model.
		Fields("id", "pluginId", "version", "sha256", "size", "manifest", "packageData").
		Where("id", artifactID).
		Scan(&row); errors.Is(err, sql.ErrNoRows) {
		return manager.Artifact{}, exception.Core("插件制品不存在")
	} else if err != nil {
		return manager.Artifact{}, exception.WrapCore(err, "查询插件制品失败")
	}
	if row.ID == 0 || row.PluginID == 0 {
		return manager.Artifact{}, exception.Core("插件制品元数据无效")
	}
	owner, err := store.loadArtifactOwner(ctx, row.PluginID)
	if err != nil {
		return manager.Artifact{}, err
	}
	if owner.ActiveArtifactID != row.ID {
		return manager.Artifact{}, exception.Core("插件制品不是所属插件的当前制品")
	}

	parsed, err := bundle.ReadWithLimits(row.PackageData, store.limits)
	if err != nil {
		return manager.Artifact{}, exception.WrapCore(err, "重新读取插件制品失败")
	}
	if parsed.SHA256 != row.SHA256 {
		return manager.Artifact{}, exception.Core("插件制品 SHA-256 校验失败")
	}
	if parsed.Size != row.Size {
		return manager.Artifact{}, exception.Core("插件制品大小校验失败")
	}
	if parsed.Manifest.Version != row.Version {
		return manager.Artifact{}, exception.Core("插件制品版本校验失败")
	}
	storedManifest, err := parseStoredManifest(row.Manifest)
	if err != nil {
		return manager.Artifact{}, exception.WrapCore(err, "解析插件制品 Manifest 失败")
	}
	ownerManifest, err := parseStoredManifest(owner.Manifest)
	if err != nil {
		return manager.Artifact{}, exception.WrapCore(err, "解析插件期望 Manifest 失败")
	}
	if !sameManifest(parsed.Manifest, storedManifest) || !sameManifest(parsed.Manifest, ownerManifest) {
		return manager.Artifact{}, exception.Core("插件制品 Manifest 校验失败")
	}

	return manager.Artifact{
		ID:       row.ID,
		PluginID: row.PluginID,
		Version:  row.Version,
		SHA256:   row.SHA256,
		Size:     row.Size,
		Manifest: parsed.Manifest,
		Data:     append([]byte(nil), row.PackageData...),
	}, nil
}

func (store *Store) loadArtifact(ctx context.Context, artifactID uint64) (artifactMetadataRow, error) {
	model, err := store.model(ctx, store.artifactDescriptor.Table())
	if err != nil {
		return artifactMetadataRow{}, err
	}
	var row artifactMetadataRow
	if err = model.
		Fields("id", "pluginId", "version", "sha256", "manifest").
		Where("id", artifactID).
		Scan(&row); errors.Is(err, sql.ErrNoRows) {
		return artifactMetadataRow{}, exception.Core("插件当前制品不存在")
	} else if err != nil {
		return artifactMetadataRow{}, exception.WrapCore(err, "查询插件制品元数据失败")
	}
	if row.ID == 0 {
		return artifactMetadataRow{}, exception.Core("插件当前制品不存在")
	}

	return row, nil
}

func (store *Store) loadArtifactOwner(ctx context.Context, pluginID uint64) (artifactOwnerRow, error) {
	model, err := store.model(ctx, store.infoDescriptor.Table())
	if err != nil {
		return artifactOwnerRow{}, err
	}
	var row artifactOwnerRow
	if err = model.
		Fields("id", "manifest", "activeArtifactID").
		Where("id", pluginID).
		Scan(&row); errors.Is(err, sql.ErrNoRows) {
		return artifactOwnerRow{}, exception.Core("插件制品所属插件不存在")
	} else if err != nil {
		return artifactOwnerRow{}, exception.WrapCore(err, "查询插件制品所属插件失败")
	}
	if row.ID == 0 {
		return artifactOwnerRow{}, exception.Core("插件制品所属插件不存在")
	}

	return row, nil
}

func (store *Store) findInfoByKey(ctx context.Context, key string, lock bool) (*managedInfoRow, error) {
	model, err := store.model(ctx, store.infoDescriptor.Table())
	if err != nil {
		return nil, err
	}
	if lock {
		model = store.lockUpdate(model)
	}
	var row managedInfoRow
	if err = model.
		Fields("id", "keyName", "hook", "version", "status", "config", "activeArtifactID", "revision").
		Where("keyName", key).
		Scan(&row); errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, exception.WrapCore(err, "查询插件管理状态失败")
	}
	if row.ID == 0 {
		return nil, nil
	}
	metadata, err := store.loadArtifact(ctx, row.ActiveArtifactID)
	if err != nil {
		return nil, err
	}
	if metadata.PluginID != row.ID {
		return nil, exception.Core("插件当前制品所属插件不匹配")
	}
	row.SHA256 = metadata.SHA256

	return cloneManagedInfo(&row), nil
}

func (store *Store) findInfoByID(ctx context.Context, id uint64, lock bool) (*managedInfoRow, error) {
	model, err := store.model(ctx, store.infoDescriptor.Table())
	if err != nil {
		return nil, err
	}
	if lock {
		model = store.lockUpdate(model)
	}
	var row managedInfoRow
	if err = model.
		Fields("id", "keyName", "hook", "version", "status", "config", "activeArtifactID", "revision").
		Where("id", id).
		Scan(&row); errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, exception.WrapCore(err, "查询插件管理状态失败")
	}
	if row.ID == 0 {
		return nil, nil
	}
	metadata, err := store.loadArtifact(ctx, row.ActiveArtifactID)
	if err != nil {
		return nil, err
	}
	if metadata.PluginID != row.ID {
		return nil, exception.Core("插件当前制品所属插件不匹配")
	}
	row.SHA256 = metadata.SHA256

	return cloneManagedInfo(&row), nil
}

func (store *Store) findInfoForDelete(ctx context.Context, id uint64, lock bool) (*managedInfoRow, error) {
	model, err := store.model(ctx, store.infoDescriptor.Table())
	if err != nil {
		return nil, err
	}
	if lock {
		model = store.lockUpdate(model)
	}
	var row managedInfoRow
	if err = model.
		Fields("id", "keyName", "hook", "version", "status", "config", "activeArtifactID", "revision").
		Where("id", id).
		Scan(&row); errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, exception.WrapCore(err, "查询待卸载插件失败")
	}
	if row.ID == 0 {
		return nil, nil
	}

	return cloneManagedInfo(&row), nil
}

func (store *Store) commitInstall(
	ctx context.Context,
	expected *managedInfoRow,
	mutation installMutation,
) (committedMutation, error) {
	current, err := store.findInfoByKey(ctx, mutation.Package.Manifest.Key, true)
	if err != nil {
		return committedMutation{}, err
	}
	if !sameManagedInfo(current, expected) {
		return committedMutation{}, exception.Comm("插件状态已变化，请重试")
	}
	targetID := uint64(0)
	if current != nil {
		targetID = current.ID
	}
	var disabled []hookConflictRow
	if mutation.Status == entity.StatusEnabled {
		disabled, err = store.lockHookConflicts(
			ctx,
			mutation.Package.Manifest.Key,
			mutation.Package.Manifest.Hook,
			targetID,
		)
		if err != nil {
			return committedMutation{}, err
		}
	}
	if current == nil {
		targetID, err = store.insertInfo(ctx, mutation)
		if err != nil {
			return committedMutation{}, err
		}
	}
	artifactID, err := store.saveArtifact(ctx, targetID, mutation)
	if err != nil {
		return committedMutation{}, err
	}
	if err = store.updateInstalledInfo(ctx, targetID, current, artifactID, mutation); err != nil {
		return committedMutation{}, err
	}
	disabledDesired, err := store.disableConflicts(ctx, disabled)
	if err != nil {
		return committedMutation{}, err
	}

	return committedMutation{PluginID: targetID, ArtifactID: artifactID, Disabled: disabledDesired}, nil
}

func (store *Store) commitUpdate(
	ctx context.Context,
	expected *managedInfoRow,
	status int32,
	config map[string]json.RawMessage,
) (committedMutation, error) {
	if expected == nil {
		return committedMutation{}, exception.Validate("插件更新目标无效")
	}
	current, err := store.findInfoByID(ctx, expected.ID, true)
	if err != nil {
		return committedMutation{}, err
	}
	if !sameManagedInfo(current, expected) {
		return committedMutation{}, exception.Comm("插件状态已变化，请重试")
	}
	if current.Revision == math.MaxUint64 {
		return committedMutation{}, exception.Core("插件 revision 已达到上限")
	}
	var conflicts []hookConflictRow
	if status == entity.StatusEnabled {
		conflicts, err = store.lockHookConflicts(ctx, current.KeyName, hookValue(current.Hook), current.ID)
		if err != nil {
			return committedMutation{}, err
		}
	}
	write := store.infoDescriptor.NewDO()
	if err = setDOColumns(write, []doColumn{
		{name: "status", value: status},
		{name: "config", value: cloneRawConfig(config)},
		{name: "revision", value: current.Revision + 1},
	}); err != nil {
		return committedMutation{}, err
	}
	model, err := store.model(ctx, store.infoDescriptor.Table())
	if err != nil {
		return committedMutation{}, err
	}
	result, err := model.Where("id", current.ID).Where("revision", current.Revision).Data(write.DBData()).Update()
	if err != nil {
		return committedMutation{}, exception.WrapCore(err, "更新插件期望状态失败")
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
		if affectedErr != nil {
			return committedMutation{}, exception.WrapCore(affectedErr, "确认插件期望状态更新失败")
		}
		return committedMutation{}, exception.Comm("插件状态已变化，请重试")
	}
	disabled, err := store.disableConflicts(ctx, conflicts)
	if err != nil {
		return committedMutation{}, err
	}

	return committedMutation{PluginID: current.ID, ArtifactID: current.ActiveArtifactID, Disabled: disabled}, nil
}

func (store *Store) commitDelete(ctx context.Context, expected []*managedInfoRow) error {
	for _, item := range expected {
		current, err := store.findInfoForDelete(ctx, item.ID, true)
		if err != nil {
			return err
		}
		if !sameManagedInfo(current, item) {
			return exception.Comm("插件状态已变化，请重试")
		}
	}
	artifactModel, err := store.model(ctx, store.artifactDescriptor.Table())
	if err != nil {
		return err
	}
	infoModel, err := store.model(ctx, store.infoDescriptor.Table())
	if err != nil {
		return err
	}
	for _, item := range expected {
		if _, err = artifactModel.Clone().Where("pluginId", item.ID).Delete(); err != nil {
			return exception.WrapCore(err, "删除插件制品失败")
		}
		if _, err = infoModel.Clone().Where("id", item.ID).Delete(); err != nil {
			return exception.WrapCore(err, "删除插件信息失败")
		}
	}

	return nil
}

func (store *Store) lockHookConflicts(
	ctx context.Context,
	key string,
	hook string,
	targetID uint64,
) ([]hookConflictRow, error) {
	model, err := store.model(ctx, store.infoDescriptor.Table())
	if err != nil {
		return nil, err
	}
	var collision hookConflictRow
	if err = store.lockUpdate(model.Clone()).
		Fields("id", "keyName", "revision").
		Where("status", entity.StatusEnabled).
		WhereNot("id", targetID).
		Where("hook", key).
		Scan(&collision); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, exception.WrapCore(err, "检查插件 key 与 hook 冲突失败")
	}
	if collision.ID != 0 {
		return nil, exception.Validate("插件 key 与已启用插件 hook 冲突")
	}
	if hook == "" {
		return nil, nil
	}
	collision = hookConflictRow{}
	if err = store.lockUpdate(model.Clone()).
		Fields("id", "keyName", "revision").
		Where("status", entity.StatusEnabled).
		WhereNot("id", targetID).
		Where("keyName", hook).
		Scan(&collision); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, exception.WrapCore(err, "检查插件 hook 与 key 冲突失败")
	}
	if collision.ID != 0 {
		return nil, exception.Validate("插件 hook 与已启用插件 key 冲突")
	}
	var conflicts []hookConflictRow
	if err = store.lockUpdate(model.Clone()).
		Fields("id", "keyName", "revision").
		Where("status", entity.StatusEnabled).
		WhereNot("id", targetID).
		Where("hook", hook).
		OrderAsc("id").
		Scan(&conflicts); err != nil {
		return nil, exception.WrapCore(err, "查询插件 hook 冲突失败")
	}

	return conflicts, nil
}

func (store *Store) disableConflicts(
	ctx context.Context,
	conflicts []hookConflictRow,
) ([]manager.DesiredPlugin, error) {
	model, err := store.model(ctx, store.infoDescriptor.Table())
	if err != nil {
		return nil, err
	}
	disabled := make([]manager.DesiredPlugin, 0, len(conflicts))
	for _, conflict := range conflicts {
		if conflict.Revision == math.MaxUint64 {
			return nil, exception.Core("插件 revision 已达到上限")
		}
		write := store.infoDescriptor.NewDO()
		if err = setDOColumns(write, []doColumn{
			{name: "status", value: entity.StatusDisabled},
			{name: "revision", value: conflict.Revision + 1},
		}); err != nil {
			return nil, err
		}
		result, updateErr := model.Clone().Where("id", conflict.ID).Where("revision", conflict.Revision).
			Data(write.DBData()).Update()
		if updateErr != nil {
			return nil, exception.WrapCore(updateErr, "禁用同 hook 插件失败")
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return nil, exception.WrapCore(affectedErr, "确认同 hook 插件禁用失败")
		}
		if affected != 1 {
			return nil, exception.Comm("插件状态已变化，请重试")
		}
		disabled = append(disabled, manager.DesiredPlugin{
			ID: conflict.ID, Key: conflict.KeyName, Enabled: false, Revision: conflict.Revision + 1,
		})
	}

	return disabled, nil
}

func (store *Store) insertInfo(ctx context.Context, mutation installMutation) (uint64, error) {
	manifest := mutation.Package.Manifest
	write := store.infoDescriptor.NewDO()
	hook := any(nil)
	if manifest.Hook != "" {
		hook = manifest.Hook
	}
	readme := any(nil)
	if mutation.Readme != nil {
		readme = *mutation.Readme
	}
	if err := setDOColumns(write, []doColumn{
		{name: "name", value: manifest.Name},
		{name: "description", value: manifest.Description},
		{name: "keyName", value: manifest.Key},
		{name: "hook", value: hook},
		{name: "singleton", value: manifest.Singleton},
		{name: "readme", value: readme},
		{name: "version", value: manifest.Version},
		{name: "logo", value: append([]byte{}, mutation.Logo...)},
		{name: "author", value: manifest.Author},
		{name: "status", value: mutation.Status},
		{name: "manifest", value: encodeManifestMap(manifest)},
		{name: "config", value: cloneRawConfig(mutation.Config)},
		{name: "runtimeABI", value: manifest.Runtime.ABI},
		{name: "activeArtifactID", value: uint64(0)},
		{name: "revision", value: mutation.Revision},
	}); err != nil {
		return 0, err
	}
	model, err := store.model(ctx, store.infoDescriptor.Table())
	if err != nil {
		return 0, err
	}
	id, err := model.Data(write.DBData()).InsertAndGetId()
	if err != nil {
		return 0, exception.WrapCore(err, "新增插件信息失败")
	}
	if id <= 0 {
		return 0, exception.Core("新增插件信息未返回有效 ID")
	}

	return uint64(id), nil
}

func (store *Store) saveArtifact(
	ctx context.Context,
	pluginID uint64,
	mutation installMutation,
) (uint64, error) {
	model, err := store.model(ctx, store.artifactDescriptor.Table())
	if err != nil {
		return 0, err
	}
	var existing artifactIdentityRow
	if err = store.lockUpdate(model.Clone()).Fields("id", "pluginId").Where("sha256", mutation.Package.SHA256).
		Scan(&existing); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, exception.WrapCore(err, "查询相同插件制品失败")
	}
	if existing.ID != 0 {
		if existing.PluginID != pluginID {
			return 0, exception.Core("相同插件制品所属插件不匹配")
		}

		return existing.ID, nil
	}
	write := store.artifactDescriptor.NewDO()
	if err = setDOColumns(write, []doColumn{
		{name: "pluginId", value: pluginID},
		{name: "version", value: mutation.Package.Manifest.Version},
		{name: "sha256", value: mutation.Package.SHA256},
		{name: "size", value: mutation.Package.Size},
		{name: "manifest", value: encodeManifestMap(mutation.Package.Manifest)},
		{name: "packageData", value: append([]byte(nil), mutation.PackageData...)},
	}); err != nil {
		return 0, err
	}
	id, err := model.Data(write.DBData()).InsertAndGetId()
	if err != nil {
		return 0, exception.WrapCore(err, "新增插件制品失败")
	}
	if id <= 0 {
		return 0, exception.Core("新增插件制品未返回有效 ID")
	}

	return uint64(id), nil
}

func (store *Store) updateInstalledInfo(
	ctx context.Context,
	pluginID uint64,
	current *managedInfoRow,
	artifactID uint64,
	mutation installMutation,
) error {
	manifest := mutation.Package.Manifest
	write := store.infoDescriptor.NewDO()
	hook := any(nil)
	if manifest.Hook != "" {
		hook = manifest.Hook
	}
	readme := any(nil)
	if mutation.Readme != nil {
		readme = *mutation.Readme
	}
	if err := setDOColumns(write, []doColumn{
		{name: "name", value: manifest.Name},
		{name: "description", value: manifest.Description},
		{name: "hook", value: hook},
		{name: "singleton", value: manifest.Singleton},
		{name: "readme", value: readme},
		{name: "version", value: manifest.Version},
		{name: "logo", value: append([]byte{}, mutation.Logo...)},
		{name: "author", value: manifest.Author},
		{name: "status", value: mutation.Status},
		{name: "manifest", value: encodeManifestMap(manifest)},
		{name: "config", value: cloneRawConfig(mutation.Config)},
		{name: "runtimeABI", value: manifest.Runtime.ABI},
		{name: "activeArtifactID", value: artifactID},
		{name: "revision", value: mutation.Revision},
	}); err != nil {
		return err
	}
	model, err := store.model(ctx, store.infoDescriptor.Table())
	if err != nil {
		return err
	}
	model = model.Where("id", pluginID)
	if current != nil {
		model = model.Where("revision", current.Revision)
	}
	result, err := model.Data(write.DBData()).Update()
	if err != nil {
		return exception.WrapCore(err, "更新插件当前制品失败")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return exception.WrapCore(err, "确认插件当前制品更新失败")
	}
	if affected != 1 {
		return exception.Comm("插件状态已变化，请重试")
	}

	return nil
}

func setDOColumns(value gnentity.DOValue, columns []doColumn) error {
	for _, column := range columns {
		if err := value.SetColumn(column.name, column.value); err != nil {
			return err
		}
	}

	return nil
}

func cloneManagedInfo(source *managedInfoRow) *managedInfoRow {
	if source == nil {
		return nil
	}
	cloned := *source
	if source.Hook != nil {
		hook := *source.Hook
		cloned.Hook = &hook
	}
	cloned.Config = cloneRawConfig(source.Config)

	return &cloned
}

func sameManagedInfo(left, right *managedInfoRow) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	return left.ID == right.ID && left.KeyName == right.KeyName && left.ActiveArtifactID == right.ActiveArtifactID &&
		left.Revision == right.Revision && left.SHA256 == right.SHA256
}

func cloneRawConfig(source map[string]json.RawMessage) map[string]json.RawMessage {
	if source == nil {
		return nil
	}
	result := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		result[key] = append(json.RawMessage(nil), value...)
	}

	return result
}

func encodeManifestMap(manifest bundle.Manifest) map[string]json.RawMessage {
	data, err := json.Marshal(manifest)
	if err != nil {
		panic(err)
	}
	result := make(map[string]json.RawMessage)
	if err = json.Unmarshal(data, &result); err != nil {
		panic(err)
	}

	return result
}

func hookValue(hook *string) string {
	if hook == nil {
		return ""
	}

	return *hook
}

func (store *Store) lockUpdate(model *gdb.Model) *gdb.Model {
	if store.runtime.Dialect().Kind() == driver.SQLite {
		return model
	}

	return model.LockUpdate()
}

func (store *Store) model(ctx context.Context, table string) (*gdb.Model, error) {
	transaction, exists, err := store.runtime.Current(ctx)
	if err != nil {
		return nil, exception.WrapCore(err, "读取插件 Store 当前事务失败")
	}
	if exists {
		return transaction.Model(table).Ctx(ctx), nil
	}

	return store.runtime.DB().Model(table).Ctx(ctx), nil
}

func parseStoredManifest(source map[string]json.RawMessage) (bundle.Manifest, error) {
	data, err := json.Marshal(source)
	if err != nil {
		return bundle.Manifest{}, err
	}

	return bundle.ParseManifest(data)
}

func sameManifest(left, right bundle.Manifest) bool {
	leftData, leftErr := json.Marshal(left)
	rightData, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftValue, leftErr := decodeJSONValue(leftData)
	rightValue, rightErr := decodeJSONValue(rightData)

	return leftErr == nil && rightErr == nil && reflect.DeepEqual(leftValue, rightValue)
}

func decodeJSONValue(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return nil, errors.New("JSON 包含多个值")
		}
		return nil, err
	}

	return normNumbers(value), nil
}

func normNumbers(value any) any {
	switch current := value.(type) {
	case json.Number:
		number, ok := new(big.Rat).SetString(current.String())
		if !ok {
			return current
		}
		return semanticJSONNumber{value: number.RatString()}
	case []any:
		for index := range current {
			current[index] = normNumbers(current[index])
		}
	case map[string]any:
		for key := range current {
			current[key] = normNumbers(current[key])
		}
	}

	return value
}

func desiredEnabled(status int32) (bool, error) {
	switch status {
	case entity.StatusDisabled:
		return false, nil
	case entity.StatusEnabled:
		return true, nil
	default:
		return false, exception.Core("插件期望状态值无效")
	}
}

var _ manager.Store = (*Store)(nil)
