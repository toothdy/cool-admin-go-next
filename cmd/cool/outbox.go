package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/drivers/pgsql/v2"
	_ "github.com/gogf/gf/contrib/drivers/sqlite/v2"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"gopkg.in/yaml.v3"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/app"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/config"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/db"
	"github.com/toothdy/cool-admin-go-next/cool-next/outbox/store"
)

const (
	defaultOutboxLimit = 50
	outboxConfigPath   = "manifest/config/config.yaml"
	outboxConfigEnv    = "COOL_CONFIG_FILE"
)

const outboxHelpText = `Outbox 运维命令

	用法:
	  cool outbox list --status <status> [--topic <topic>] [--limit <n>]
	  cool outbox show --message-id <uuidv7>
	  cool outbox replay --message-id <uuidv7> [--operator <name> --reason <text>] [--dry-run]
`

const outboxListHelpText = `用法: cool outbox list --status <status> [--topic <topic>] [--limit <n>]
`

const outboxShowHelpText = `用法: cool outbox show --message-id <uuidv7>
`

const replayHelp = `用法: cool outbox replay --message-id <uuidv7> [--operator <name> --reason <text>] [--dry-run]
`

type outboxCommand func(context.Context, []string, string, io.Writer, io.Writer) int

type outboxOperations interface {
	List(context.Context, store.ListFilter) ([]store.Metadata, error)
	Show(context.Context, string) (store.Metadata, error)
	ReplayDead(context.Context, string) error
}

type outboxAuditLogger interface {
	Info(context.Context, ...any)
}

type outboxDependencies struct {
	open         func(context.Context, string) (outboxOperations, error)
	newOperation func() (string, error)
	audit        outboxAuditLogger
}

type outboxRuntimeConfig struct {
	Cool struct {
		Outbox struct {
			DatabaseGroup string `json:"databaseGroup"`
		} `json:"outbox"`
	} `json:"cool"`
	Database map[string]gdb.ConfigGroup `json:"database"`
}

type outboxOutput struct {
	MessageID      string     `json:"messageId"`
	Topic          string     `json:"topic"`
	MessageType    string     `json:"messageType"`
	MessageVersion uint32     `json:"messageVersion"`
	Status         string     `json:"status"`
	Attempts       uint32     `json:"attempts"`
	AvailableAt    time.Time  `json:"availableAt"`
	LeaseExpiresAt *time.Time `json:"leaseExpiresAt,omitempty"`
	LastError      *string    `json:"lastError,omitempty"`
	CreateTime     time.Time  `json:"createTime"`
	UpdateTime     time.Time  `json:"updateTime"`
	SentAt         *time.Time `json:"sentAt,omitempty"`
}

type outboxReplayOutput struct {
	OperationID string `json:"operationId"`
	MessageID   string `json:"messageId"`
	OldStatus   string `json:"oldStatus"`
	NewStatus   string `json:"newStatus"`
	DryRun      bool   `json:"dryRun"`
}

// 不含消息内容和基础设施信息的重放审计
type outboxReplayAudit struct {
	Event       string `json:"event"`
	OperationID string `json:"operationId"`
	Operator    string `json:"operator,omitempty"`
	Reason      string `json:"reason,omitempty"`
	MessageID   string `json:"messageId"`
	OldStatus   string `json:"oldStatus,omitempty"`
	Result      string `json:"result"`
}

func runOutbox(ctx context.Context, args []string, cwd string, stdout, stderr io.Writer) int {
	return runOutboxWith(ctx, args, cwd, stdout, stderr, outboxDependencies{
		open:         openOutboxStore,
		newOperation: app.NewTraceID,
		audit:        g.Log(),
	})
}

func runOutboxWith(
	ctx context.Context,
	args []string,
	cwd string,
	stdout io.Writer,
	stderr io.Writer,
	dependencies outboxDependencies,
) int {
	if len(args) == 1 && isHelp(args[0]) {
		_, _ = io.WriteString(stdout, outboxHelpText)
		return exitSuccess
	}
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, outboxHelpText)
		return exitUsage
	}

	switch args[0] {
	case "list":
		return runOutboxList(ctx, args[1:], cwd, stdout, stderr, dependencies)
	case "show":
		return runOutboxShow(ctx, args[1:], cwd, stdout, stderr, dependencies)
	case "replay":
		return runOutboxReplay(ctx, args[1:], cwd, stdout, stderr, dependencies)
	default:
		_, _ = fmt.Fprintf(stderr, "未知 Outbox 命令 %q\n\n%s", args[0], outboxHelpText)
		return exitUsage
	}
}

func runOutboxList(
	ctx context.Context,
	args []string,
	cwd string,
	stdout io.Writer,
	stderr io.Writer,
	dependencies outboxDependencies,
) int {
	flags := flag.NewFlagSet("outbox list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { _, _ = io.WriteString(stderr, outboxListHelpText) }
	statusText := flags.String("status", "", "发布状态")
	topic := flags.String("topic", "", "消息目的地")
	limit := flags.Int("limit", defaultOutboxLimit, "返回数量")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitSuccess
		}
		return exitUsage
	}
	if flags.NArg() != 0 {
		return outboxUsageError(stderr, outboxListHelpText, "list 不接受位置参数")
	}
	status, isValid := parseOutboxStatus(*statusText)
	if !isValid {
		return outboxUsageError(stderr, outboxListHelpText, "--status 必须是 pending、retry、leased、sent 或 dead")
	}
	if *limit <= 0 || *limit > store.MaxListLimit {
		return outboxUsageError(
			stderr,
			outboxListHelpText,
			fmt.Sprintf("--limit 必须在 1 到 %d 之间", store.MaxListLimit),
		)
	}
	trimmedTopic := strings.TrimSpace(*topic)
	if *topic != "" && trimmedTopic == "" {
		return outboxUsageError(stderr, outboxListHelpText, "--topic 不能为空白文本")
	}
	storage, err := dependencies.open(ctx, cwd)
	if err != nil {
		return outboxFailure(stderr, err)
	}
	records, err := storage.List(ctx, store.ListFilter{Status: status, Topic: trimmedTopic, Limit: *limit})
	if err != nil {
		return outboxFailure(stderr, err)
	}
	output := make([]outboxOutput, 0, len(records))
	for _, record := range records {
		output = append(output, safeOutboxOutput(record))
	}
	if err = writeOutboxJSON(stdout, output); err != nil {
		return outboxFailure(stderr, err)
	}

	return exitSuccess
}

func runOutboxShow(
	ctx context.Context,
	args []string,
	cwd string,
	stdout io.Writer,
	stderr io.Writer,
	dependencies outboxDependencies,
) int {
	flags := flag.NewFlagSet("outbox show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { _, _ = io.WriteString(stderr, outboxShowHelpText) }
	messageID := flags.String("message-id", "", "消息 ID")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitSuccess
		}
		return exitUsage
	}
	if flags.NArg() != 0 || !store.IsValidMessageID(*messageID) {
		return outboxUsageError(stderr, outboxShowHelpText, "--message-id 必须是规范的小写 UUIDv7")
	}
	storage, err := dependencies.open(ctx, cwd)
	if err != nil {
		return outboxFailure(stderr, err)
	}
	record, err := storage.Show(ctx, *messageID)
	if err != nil {
		return outboxFailure(stderr, err)
	}
	if err = writeOutboxJSON(stdout, safeOutboxOutput(record)); err != nil {
		return outboxFailure(stderr, err)
	}

	return exitSuccess
}

func runOutboxReplay(
	ctx context.Context,
	args []string,
	cwd string,
	stdout io.Writer,
	stderr io.Writer,
	dependencies outboxDependencies,
) int {
	flags := flag.NewFlagSet("outbox replay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { _, _ = io.WriteString(stderr, replayHelp) }
	messageID := flags.String("message-id", "", "消息 ID")
	operator := flags.String("operator", "", "操作人")
	reason := flags.String("reason", "", "操作原因")
	isDryRun := flags.Bool("dry-run", false, "只校验不修改")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitSuccess
		}
		return exitUsage
	}
	if flags.NArg() != 0 || !store.IsValidMessageID(*messageID) {
		return outboxUsageError(stderr, replayHelp, "--message-id 必须是规范的小写 UUIDv7")
	}
	trimmedOperator := strings.TrimSpace(*operator)
	trimmedReason := strings.TrimSpace(*reason)
	if !*isDryRun && (trimmedOperator == "" || trimmedReason == "") {
		return outboxUsageError(stderr, replayHelp, "实际重放必须提供非空 --operator 和 --reason")
	}
	operationID, err := dependencies.newOperation()
	if err != nil {
		return outboxFailure(stderr, fmt.Errorf("生成 Operation ID 失败: %w", err))
	}
	operationCtx, err := app.WithTraceID(ctx, operationID)
	if err != nil {
		return outboxFailure(stderr, err)
	}
	audit := outboxReplayAudit{
		Event:       "outbox_replay",
		OperationID: operationID,
		Operator:    safeOutboxText(trimmedOperator),
		Reason:      safeOutboxText(trimmedReason),
		MessageID:   *messageID,
		Result:      "failed",
	}
	storage, err := dependencies.open(operationCtx, cwd)
	if err != nil {
		dependencies.audit.Info(operationCtx, audit)
		return outboxFailure(stderr, err)
	}
	record, err := storage.Show(operationCtx, *messageID)
	if err != nil {
		dependencies.audit.Info(operationCtx, audit)
		return outboxFailure(stderr, err)
	}
	audit.OldStatus = string(record.Status)
	if record.Status != store.Dead {
		dependencies.audit.Info(operationCtx, audit)
		return outboxFailure(stderr, store.ErrReplayRejected)
	}
	if *isDryRun {
		audit.Result = "dry_run"
		dependencies.audit.Info(operationCtx, audit)
		if err = writeOutboxJSON(stdout, outboxReplayOutput{
			OperationID: operationID,
			MessageID:   record.MessageID,
			OldStatus:   string(record.Status),
			NewStatus:   string(store.Retry),
			DryRun:      true,
		}); err != nil {
			return outboxFailure(stderr, err)
		}

		return exitSuccess
	}
	if err = storage.ReplayDead(operationCtx, *messageID); err != nil {
		dependencies.audit.Info(operationCtx, audit)
		return outboxFailure(stderr, err)
	}
	audit.Result = "success"
	dependencies.audit.Info(operationCtx, audit)
	if err = writeOutboxJSON(stdout, outboxReplayOutput{
		OperationID: operationID,
		MessageID:   record.MessageID,
		OldStatus:   string(record.Status),
		NewStatus:   string(store.Retry),
	}); err != nil {
		return outboxFailure(stderr, err)
	}

	return exitSuccess
}

func openOutboxStore(ctx context.Context, cwd string) (outboxOperations, error) {
	path := os.Getenv(outboxConfigEnv)
	if path == "" {
		path = filepath.Join(cwd, outboxConfigPath)
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取应用配置失败: %w", err)
	}
	selected, err := selectOutboxConfig(content)
	if err != nil {
		return nil, err
	}
	defaults := outboxRuntimeConfig{}
	defaults.Cool.Outbox.DatabaseGroup = "default"
	result, err := config.Load(ctx, defaults, config.Source{Main: selected, LookupEnv: os.LookupEnv})
	if err != nil {
		return nil, fmt.Errorf("Outbox 配置无效: %w", err)
	}
	config := result.Value()
	group := strings.TrimSpace(config.Cool.Outbox.DatabaseGroup)
	nodes, exists := config.Database[group]
	if group == "" || !exists || len(nodes) == 0 {
		return nil, fmt.Errorf("Outbox 数据库组 %q 未配置", group)
	}
	runtime, err := db.New(ctx, db.Config{Group: group, Nodes: nodes})
	if err != nil {
		return nil, err
	}
	storage, err := store.New(runtime)
	if err != nil {
		return nil, err
	}
	if err = storage.Probe(ctx); err != nil {
		return nil, err
	}

	return storage, nil
}

func selectOutboxConfig(content []byte) ([]byte, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	document := &yaml.Node{}
	if err := decoder.Decode(document); err != nil {
		return nil, fmt.Errorf("解析应用配置失败: %w", err)
	}
	extra := &yaml.Node{}
	if err := decoder.Decode(extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("应用配置只能包含一个 YAML 文档")
		}
		return nil, fmt.Errorf("解析应用配置失败: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("应用配置根节点必须是对象")
	}
	root := document.Content[0]
	cool, err := getOutboxConfigNode(root, "cool")
	if err != nil {
		return nil, err
	}
	database, err := getOutboxConfigNode(root, "database")
	if err != nil {
		return nil, err
	}
	selected := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if cool != nil {
		outbox, outboxErr := getOutboxConfigNode(cool, "outbox")
		if outboxErr != nil {
			return nil, outboxErr
		}
		if outbox != nil {
			databaseGroup, groupErr := outboxValue(outbox, "databaseGroup")
			if groupErr != nil {
				return nil, groupErr
			}
			if databaseGroup != nil {
				selected.Content = append(selected.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "cool"},
					&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
						{Kind: yaml.ScalarNode, Tag: "!!str", Value: "outbox"},
						{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
							{Kind: yaml.ScalarNode, Tag: "!!str", Value: "databaseGroup"},
							databaseGroup,
						}},
					}},
				)
			}
		}
	}
	if database != nil {
		selected.Content = append(selected.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "database"},
			database,
		)
	}
	output := bytes.Buffer{}
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err = encoder.Encode(selected); err != nil {
		return nil, fmt.Errorf("编码 Outbox 配置失败: %w", err)
	}
	if err = encoder.Close(); err != nil {
		return nil, fmt.Errorf("编码 Outbox 配置失败: %w", err)
	}

	return output.Bytes(), nil
}

func getOutboxConfigNode(mapping *yaml.Node, name string) (*yaml.Node, error) {
	result, err := outboxValue(mapping, name)
	if err != nil {
		return nil, err
	}
	if result != nil && result.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("配置 %s 必须是对象", name)
	}

	return result, nil
}

func outboxValue(mapping *yaml.Node, name string) (*yaml.Node, error) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("配置 %s 必须是对象", name)
	}
	var result *yaml.Node
	seen := make(map[string]bool, len(mapping.Content)/2)
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		key := mapping.Content[index]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, fmt.Errorf("应用配置键必须是字符串")
		}
		if seen[key.Value] {
			return nil, fmt.Errorf("应用配置字段 %s 重复", key.Value)
		}
		seen[key.Value] = true
		if key.Value == name {
			result = mapping.Content[index+1]
		}
	}
	return result, nil
}

func parseOutboxStatus(value string) (store.Status, bool) {
	status := store.Status(strings.TrimSpace(value))
	switch status {
	case store.Pending, store.Retry, store.Leased, store.Sent, store.Dead:
		return status, true
	default:
		return "", false
	}
}

func safeOutboxOutput(record store.Metadata) outboxOutput {
	var lastError *string
	if record.LastError != nil {
		value := safeOutboxText(*record.LastError)
		lastError = &value
	}

	return outboxOutput{
		MessageID:      record.MessageID,
		Topic:          record.Topic,
		MessageType:    record.MessageType,
		MessageVersion: record.MessageVersion,
		Status:         string(record.Status),
		Attempts:       record.Attempts,
		AvailableAt:    record.AvailableAt,
		LeaseExpiresAt: record.LeaseExpiresAt,
		LastError:      lastError,
		CreateTime:     record.CreateTime,
		UpdateTime:     record.UpdateTime,
		SentAt:         record.SentAt,
	}
}

func safeOutboxText(value string) string {
	if value == "" {
		return ""
	}

	return exception.Resolve(exception.Validate(value)).Message
}

func writeOutboxJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("输出 Outbox 结果失败: %w", err)
	}

	return nil
}

func outboxUsageError(stderr io.Writer, help, message string) int {
	_, _ = fmt.Fprintf(stderr, "%s\n\n%s", message, help)
	return exitUsage
}

func outboxFailure(stderr io.Writer, err error) int {
	message := "Outbox 命令失败"
	switch {
	case errors.Is(err, store.ErrNotFound):
		message = store.ErrNotFound.Error()
	case errors.Is(err, store.ErrReplayRejected):
		message = store.ErrReplayRejected.Error()
	case errors.Is(err, store.ErrReplayConflict):
		message = store.ErrReplayConflict.Error()
	case errors.Is(err, context.Canceled):
		message = context.Canceled.Error()
	case errors.Is(err, context.DeadlineExceeded):
		message = context.DeadlineExceeded.Error()
	}
	_, _ = fmt.Fprintln(stderr, message)

	return exitFailure
}
