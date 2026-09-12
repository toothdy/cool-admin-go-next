package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/errors/gerror"

	"github.com/toothdy/cool-admin-go-next/cool-next/db/driver"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/schema"
)

const probeConsumer = "__cool_probe_consumer__"

var errProbeRollback = errors.New("outbox store probe rollback")

type metadataColumn struct {
	Name string `orm:"name"`
	PK   int    `orm:"pk"`
}

type metadataIndexColumn struct {
	IndexName  string `orm:"indexName"`
	ColumnName string `orm:"columnName"`
}

type tableContract struct {
	columns    []string
	primaryKey []string
	indexes    map[string][]string
}

// 自检 Store 与 Schema 契约
func (store *DatabaseStore) Probe(ctx context.Context) error {
	if store == nil || store.runtime.DB() == nil {
		return gerror.New("outbox store: Store 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := store.checkCaps(); err != nil {
		return err
	}
	if err := store.checkTable(ctx, schema.OutboxDefinition()); err != nil {
		return err
	}
	if err := store.checkTable(ctx, schema.InboxDefinition()); err != nil {
		return err
	}
	if store.runtime.Dialect().Kind() == driver.MySQL {
		if err := store.checkMySQL(ctx); err != nil {
			return err
		}
	}

	return store.probeDML(ctx)
}

func (store *DatabaseStore) checkCaps() error {
	capabilities := store.runtime.Diagnostic().Capabilities
	if !capabilities.Transactions || !capabilities.ConditionalWrite {
		return gerror.New("outbox store: 数据库缺少事务或条件写入能力")
	}
	if store.runtime.Dialect().Kind() != driver.SQLite && (!capabilities.RowLock || !capabilities.SkipLocked) {
		return gerror.New("outbox store: 数据库缺少行锁或 SKIP LOCKED 能力")
	}

	return nil
}

func (store *DatabaseStore) checkTable(ctx context.Context, definition schema.Definition) error {
	actual, err := store.inspectTable(ctx, definition.Name)
	if err != nil {
		return err
	}
	expectedColumns := make([]string, len(definition.Columns))
	for index, column := range definition.Columns {
		expectedColumns[index] = column.Name
	}
	if !slices.Equal(actual.columns, expectedColumns) {
		return gerror.Newf(
			"outbox store: 表 %s 列序不匹配，期望 %v，实际 %v",
			definition.Name,
			expectedColumns,
			actual.columns,
		)
	}
	if !slices.Equal(actual.primaryKey, definition.PrimaryKey) {
		return gerror.Newf(
			"outbox store: 表 %s 主键不匹配，期望 %v，实际 %v",
			definition.Name,
			definition.PrimaryKey,
			actual.primaryKey,
		)
	}
	for _, index := range definition.Indexes {
		fields, exists := actual.indexes[strings.ToLower(index.Name)]
		if !exists || !slices.Equal(fields, index.Fields) {
			return gerror.Newf(
				"outbox store: 表 %s 索引 %s 不匹配，期望 %v，实际 %v",
				definition.Name,
				index.Name,
				index.Fields,
				fields,
			)
		}
	}

	return nil
}

func (store *DatabaseStore) inspectTable(ctx context.Context, tableName string) (tableContract, error) {
	switch store.runtime.Dialect().Kind() {
	case driver.MySQL:
		return store.inspectMySQL(ctx, tableName)
	case driver.PostgreSQL:
		return store.inspectPostgres(ctx, tableName)
	case driver.SQLite:
		return store.inspectSQLite(ctx, tableName)
	default:
		return tableContract{}, gerror.Newf("outbox store: 不支持的数据库类型 %s", store.runtime.Dialect().Kind())
	}
}

func (store *DatabaseStore) inspectMySQL(ctx context.Context, tableName string) (tableContract, error) {
	var columns []metadataColumn
	if err := store.runtime.DB().GetScan(
		ctx,
		&columns,
		"SELECT COLUMN_NAME AS name FROM information_schema.COLUMNS "+
			"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION",
		tableName,
	); err != nil {
		return tableContract{}, gerror.Wrapf(err, "outbox store: 读取 MySQL 表 %s 列", tableName)
	}
	var primary []metadataColumn
	if err := store.runtime.DB().GetScan(
		ctx,
		&primary,
		"SELECT COLUMN_NAME AS name FROM information_schema.KEY_COLUMN_USAGE "+
			"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND CONSTRAINT_NAME = 'PRIMARY' "+
			"ORDER BY ORDINAL_POSITION",
		tableName,
	); err != nil {
		return tableContract{}, gerror.Wrapf(err, "outbox store: 读取 MySQL 表 %s 主键", tableName)
	}
	var indexColumns []metadataIndexColumn
	if err := store.runtime.DB().GetScan(
		ctx,
		&indexColumns,
		"SELECT INDEX_NAME AS indexName, COLUMN_NAME AS columnName FROM information_schema.STATISTICS "+
			"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME <> 'PRIMARY' "+
			"ORDER BY INDEX_NAME, SEQ_IN_INDEX",
		tableName,
	); err != nil {
		return tableContract{}, gerror.Wrapf(err, "outbox store: 读取 MySQL 表 %s 索引", tableName)
	}

	return collectContract(columns, primary, indexColumns), nil
}

func (store *DatabaseStore) inspectPostgres(ctx context.Context, tableName string) (tableContract, error) {
	var columns []metadataColumn
	if err := store.runtime.DB().GetScan(
		ctx,
		&columns,
		"SELECT column_name AS name FROM information_schema.columns "+
			"WHERE table_schema = current_schema() AND table_name = ? ORDER BY ordinal_position",
		tableName,
	); err != nil {
		return tableContract{}, gerror.Wrapf(err, "outbox store: 读取 PostgreSQL 表 %s 列", tableName)
	}
	var primary []metadataColumn
	if err := store.runtime.DB().GetScan(
		ctx,
		&primary,
		"SELECT attribute.attname AS name FROM pg_index idx "+
			"JOIN pg_class relation ON relation.oid = idx.indrelid "+
			"JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace "+
			"JOIN unnest(idx.indkey) WITH ORDINALITY AS keys(attributeNumber, sequence) ON TRUE "+
			"JOIN pg_attribute attribute ON attribute.attrelid = relation.oid AND attribute.attnum = keys.attributeNumber "+
			"WHERE namespace.nspname = current_schema() AND relation.relname = ? AND idx.indisprimary ORDER BY keys.sequence",
		tableName,
	); err != nil {
		return tableContract{}, gerror.Wrapf(err, "outbox store: 读取 PostgreSQL 表 %s 主键", tableName)
	}
	var indexColumns []metadataIndexColumn
	if err := store.runtime.DB().GetScan(
		ctx,
		&indexColumns,
		"SELECT indexrel.relname AS indexName, attribute.attname AS columnName FROM pg_index idx "+
			"JOIN pg_class relation ON relation.oid = idx.indrelid "+
			"JOIN pg_class indexrel ON indexrel.oid = idx.indexrelid "+
			"JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace "+
			"JOIN unnest(idx.indkey) WITH ORDINALITY AS keys(attributeNumber, sequence) ON TRUE "+
			"JOIN pg_attribute attribute ON attribute.attrelid = relation.oid AND attribute.attnum = keys.attributeNumber "+
			"WHERE namespace.nspname = current_schema() AND relation.relname = ? AND NOT idx.indisprimary "+
			"ORDER BY indexrel.relname, keys.sequence",
		tableName,
	); err != nil {
		return tableContract{}, gerror.Wrapf(err, "outbox store: 读取 PostgreSQL 表 %s 索引", tableName)
	}

	return collectContract(columns, primary, indexColumns), nil
}

func (store *DatabaseStore) inspectSQLite(ctx context.Context, tableName string) (tableContract, error) {
	quotedTable, err := store.runtime.Dialect().Quote(tableName)
	if err != nil {
		return tableContract{}, err
	}
	var columns []metadataColumn
	if err = store.runtime.DB().GetScan(ctx, &columns, "PRAGMA table_info("+quotedTable+")"); err != nil {
		return tableContract{}, gerror.Wrapf(err, "outbox store: 读取 SQLite 表 %s 列", tableName)
	}
	primary := append([]metadataColumn(nil), columns...)
	sort.Slice(primary, func(first int, second int) bool {
		if primary[first].PK == 0 {
			return false
		}
		if primary[second].PK == 0 {
			return true
		}
		return primary[first].PK < primary[second].PK
	})
	primary = slices.DeleteFunc(primary, func(column metadataColumn) bool { return column.PK == 0 })

	indexRows, err := store.runtime.DB().GetAll(ctx, "PRAGMA index_list("+quotedTable+")")
	if err != nil {
		return tableContract{}, gerror.Wrapf(err, "outbox store: 读取 SQLite 表 %s 索引", tableName)
	}
	indexColumns := make([]metadataIndexColumn, 0)
	for _, row := range indexRows {
		name := recordValue(row, "name")
		if name == "" || strings.HasPrefix(name, "sqlite_autoindex_") {
			continue
		}
		quotedIndex, quoteErr := store.runtime.Dialect().Quote(name)
		if quoteErr != nil {
			return tableContract{}, quoteErr
		}
		columns, queryErr := store.runtime.DB().GetAll(ctx, "PRAGMA index_info("+quotedIndex+")")
		if queryErr != nil {
			return tableContract{}, gerror.Wrapf(queryErr, "outbox store: 读取 SQLite 索引 %s", name)
		}
		for _, column := range columns {
			indexColumns = append(indexColumns, metadataIndexColumn{
				IndexName:  name,
				ColumnName: recordValue(column, "name"),
			})
		}
	}

	return collectContract(columns, primary, indexColumns), nil
}

func collectContract(
	columns []metadataColumn,
	primary []metadataColumn,
	indexColumns []metadataIndexColumn,
) tableContract {
	contract := tableContract{
		columns:    make([]string, len(columns)),
		primaryKey: make([]string, len(primary)),
		indexes:    make(map[string][]string),
	}
	for index, column := range columns {
		contract.columns[index] = column.Name
	}
	for index, column := range primary {
		contract.primaryKey[index] = column.Name
	}
	for _, column := range indexColumns {
		name := strings.ToLower(column.IndexName)
		contract.indexes[name] = append(contract.indexes[name], column.ColumnName)
	}

	return contract
}

func (store *DatabaseStore) checkMySQL(ctx context.Context) error {
	for _, tableName := range []string{schema.OutboxTableName, schema.InboxTableName} {
		value, err := store.runtime.DB().GetValue(
			ctx,
			"SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?",
			tableName,
		)
		if err != nil {
			return gerror.Wrapf(err, "outbox store: 读取 MySQL 表 %s 引擎", tableName)
		}
		if !strings.EqualFold(value.String(), "InnoDB") {
			return gerror.Newf("outbox store: MySQL 表 %s 必须使用 InnoDB，实际为 %s", tableName, value.String())
		}
	}

	return nil
}

func (store *DatabaseStore) probeDML(ctx context.Context) error {
	messageID, err := probeMessageID()
	if err != nil {
		return err
	}
	record, err := NewRecord(
		messageID,
		"__cool_probe_topic__",
		"cool.internal.probe",
		1,
		nil,
		[]byte(`{}`),
		[]byte(`{}`),
	)
	if err != nil {
		return err
	}
	probe := func(transactionCtx context.Context, transaction gdb.TX) error {
		if enqueueErr := store.Enqueue(transactionCtx, transaction, record); enqueueErr != nil {
			return enqueueErr
		}
		inserted, insertErr := store.InsertIfAbsent(transactionCtx, transaction, probeConsumer, messageID)
		if insertErr != nil {
			return gerror.Wrap(insertErr, "outbox store: Inbox 首次写入探测")
		}
		if !inserted {
			return gerror.New("outbox store: Inbox 首次写入探测未插入记录")
		}
		inserted, insertErr = store.InsertIfAbsent(transactionCtx, transaction, probeConsumer, messageID)
		if insertErr != nil {
			return gerror.Wrap(insertErr, "outbox store: Inbox 重复写入探测")
		}
		if inserted {
			return gerror.New("outbox store: Inbox 重复写入探测再次插入记录")
		}
		token := newClaimToken()
		arguments := store.statements.claimArguments("__cool_probe_worker__", token, time.Minute, messageID)
		result, claimErr := transaction.Ctx(transactionCtx).Exec(store.statements.claim, arguments...)
		if claimErr != nil {
			return gerror.Wrap(claimErr, "outbox store: 条件领取探测")
		}
		affected, claimErr := result.RowsAffected()
		if claimErr != nil {
			return gerror.Wrap(claimErr, "outbox store: 读取条件领取探测行数")
		}
		if affected != 1 {
			return invariantError("条件领取探测", affected)
		}
		if _, claimErr = store.readClaimed(transactionCtx, transaction, messageID, token); claimErr != nil {
			return claimErr
		}

		return errProbeRollback
	}
	if store.runtime.Dialect().Kind() == driver.SQLite {
		err = store.runtime.DB().Transaction(ctx, probe)
	} else {
		options := gdb.DefaultTxOptions()
		options.Isolation = sql.LevelReadCommitted
		err = store.runtime.DB().TransactionWithOptions(
			ctx,
			options,
			probe,
		)
	}
	if !errors.Is(err, errProbeRollback) {
		return gerror.Wrap(err, "outbox store: 回滚能力探测事务")
	}

	return store.checkProbeRows(ctx, messageID)
}

func (store *DatabaseStore) checkProbeRows(ctx context.Context, messageID string) error {
	for _, check := range []struct {
		table     string
		condition string
		arguments []any
	}{
		{table: schema.OutboxTableName, condition: "messageId", arguments: []any{messageID}},
		{table: schema.InboxTableName, condition: "messageId", arguments: []any{messageID}},
	} {
		table, err := store.runtime.Dialect().Quote(check.table)
		if err != nil {
			return err
		}
		column, err := store.runtime.Dialect().Quote(check.condition)
		if err != nil {
			return err
		}
		count, err := store.runtime.DB().GetCount(
			ctx,
			fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = ?", table, column),
			check.arguments...,
		)
		if err != nil {
			return gerror.Wrapf(err, "outbox store: 确认表 %s 探测回滚", check.table)
		}
		if count != 0 {
			return gerror.Newf("outbox store: 表 %s 遗留 %d 条探测记录", check.table, count)
		}
	}

	return nil
}

func probeMessageID() (string, error) {
	value := make([]byte, 16)
	rand.Read(value)
	milliseconds := uint64(time.Now().UnixMilli())
	for index := 5; index >= 0; index-- {
		value[index] = byte(milliseconds)
		milliseconds >>= 8
	}
	value[6] = value[6]&0x0f | 0x70
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value)

	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func recordValue(record gdb.Record, field string) string {
	for name, value := range record {
		if strings.EqualFold(name, field) {
			return value.String()
		}
	}

	return ""
}
