package driver

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/errors/gerror"
)

var errProbeRollback = errors.New("driver probe rollback")

const probeCleanupTimeout = 5 * time.Second // 内部表清理上限

// 验证真实数据库的基线能力
func Probe(
	ctx context.Context,
	database gdb.DB,
	transactionTables ...string,
) (report Report, err error) {
	if database == nil {
		return Report{}, gerror.New("数据库对象不能为 nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	config := database.GetConfig()
	if config == nil {
		return Report{}, gerror.New("数据库配置不能为 nil")
	}
	kind, err := kindFromDriver(config.Type)
	if err != nil {
		return Report{}, err
	}
	dialect, err := New(kind)
	if err != nil {
		return Report{}, err
	}
	for _, table := range transactionTables {
		if _, err = dialect.Quote(table); err != nil {
			return Report{}, gerror.Wrap(err, "校验交易表名")
		}
	}

	version, err := readVersion(ctx, database, kind)
	if err != nil {
		return Report{}, err
	}
	if kind == MySQL {
		if err = checkMySQLDefault(ctx, database); err != nil {
			return Report{}, err
		}
	}

	probeTable := probeTableName()
	cleanupNeeded := true
	defer func() {
		if !cleanupNeeded {
			return
		}
		cleanupCtx, cancelCleanup := cleanupContext(ctx)
		defer cancelCleanup()
		cleanupErr := dropProbeTable(cleanupCtx, database, dialect, probeTable)
		if cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()

	if err = createProbeTable(ctx, database, dialect, probeTable); err != nil {
		return Report{}, err
	}
	if err = probeTransaction(ctx, database, dialect, probeTable); err != nil {
		return Report{}, err
	}
	if err = probeWrite(ctx, database, dialect, probeTable); err != nil {
		return Report{}, err
	}
	if kind != SQLite {
		if err = probeSkipLocked(ctx, database, dialect, probeTable); err != nil {
			return Report{}, err
		}
	}
	if err = dropProbeTable(ctx, database, dialect, probeTable); err != nil {
		return Report{}, err
	}
	cleanupNeeded = false

	if kind == MySQL {
		if err = checkMySQLTables(ctx, database, transactionTables); err != nil {
			return Report{}, err
		}
	}

	return Report{
		Dialect:      dialect,
		Version:      version,
		Capabilities: dialect.capabilities(),
	}, nil
}

func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), probeCleanupTimeout)
}

func kindFromDriver(driverType string) (Kind, error) {
	switch strings.ToLower(strings.TrimSpace(driverType)) {
	case "mysql":
		return MySQL, nil
	case "pgsql":
		return PostgreSQL, nil
	case "sqlite":
		return SQLite, nil
	default:
		return "", gerror.Newf("不支持的数据库驱动: %s", driverType)
	}
}

func readVersion(ctx context.Context, database gdb.DB, kind Kind) (Version, error) {
	query := "SELECT VERSION()"
	switch kind {
	case PostgreSQL:
		query = "SHOW server_version"
	case SQLite:
		query = "SELECT sqlite_version()"
	}

	value, err := database.GetValue(ctx, query)
	if err != nil {
		return Version{}, gerror.Wrapf(err, "读取 %s 版本", kind)
	}
	version, err := ValidateVersion(kind, value.String())
	if err != nil {
		return Version{}, gerror.Wrapf(err, "校验 %s 版本", kind)
	}

	return version, nil
}

func probeTableName() string {
	randomBytes := make([]byte, 8)
	rand.Read(randomBytes)

	return "cool_probe_" + hex.EncodeToString(randomBytes)
}

func createProbeTable(ctx context.Context, database gdb.DB, dialect Dialect, tableName string) error {
	table, id, value, err := probeIdentifiers(dialect, tableName)
	if err != nil {
		return err
	}
	statement := fmt.Sprintf(
		"CREATE TABLE %s (%s INTEGER NOT NULL PRIMARY KEY, %s INTEGER NOT NULL)",
		table,
		id,
		value,
	)
	if dialect.kind == MySQL {
		statement += " ENGINE=InnoDB"
	}
	if _, err = database.Exec(ctx, statement); err != nil {
		return gerror.Wrapf(err, "创建 %s 内部探测表", dialect.kind)
	}

	return nil
}

func dropProbeTable(ctx context.Context, database gdb.DB, dialect Dialect, tableName string) error {
	table, err := dialect.Quote(tableName)
	if err != nil {
		return err
	}
	if _, err = database.Exec(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		return gerror.Wrapf(err, "清理 %s 内部探测表", dialect.kind)
	}

	return nil
}

func probeTransaction(ctx context.Context, database gdb.DB, dialect Dialect, tableName string) error {
	table, id, value, err := probeIdentifiers(dialect, tableName)
	if err != nil {
		return err
	}
	err = database.Transaction(ctx, func(ctx context.Context, transaction gdb.TX) error {
		_, execErr := transaction.Ctx(ctx).Exec(
			fmt.Sprintf("INSERT INTO %s (%s, %s) VALUES (?, ?)", table, id, value),
			1,
			10,
		)
		if execErr != nil {
			return execErr
		}

		return errProbeRollback
	})
	if err = checkRollback(err, dialect.kind); err != nil {
		return err
	}
	count, err := database.GetCount(
		ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = ?", table, id),
		1,
	)
	if err != nil {
		return gerror.Wrapf(err, "读取 %s 事务回滚结果", dialect.kind)
	}
	if count != 0 {
		return gerror.Newf("%s 事务回滚后仍保留 %d 行", dialect.kind, count)
	}

	return nil
}

func checkRollback(err error, kind Kind) error {
	if errors.Is(err, errProbeRollback) {
		return nil
	}
	if err == nil {
		return gerror.Newf("%s 事务探测未执行预期回滚", kind)
	}

	return gerror.Wrapf(err, "%s 事务回滚能力探测失败", kind)
}

func probeWrite(
	ctx context.Context,
	database gdb.DB,
	dialect Dialect,
	tableName string,
) error {
	table, id, value, err := probeIdentifiers(dialect, tableName)
	if err != nil {
		return err
	}
	if _, err = database.Exec(
		ctx,
		fmt.Sprintf("INSERT INTO %s (%s, %s) VALUES (?, ?)", table, id, value),
		2,
		10,
	); err != nil {
		return gerror.Wrapf(err, "准备 %s 条件写入探测行", dialect.kind)
	}
	if dialect.kind != MySQL {
		insertStatement, err := insertSQL(dialect, tableName)
		if err != nil {
			return err
		}
		insertResult, err := database.Exec(ctx, insertStatement, 2, 99)
		if err != nil {
			return gerror.Wrapf(err, "%s 条件插入能力探测失败", dialect.kind)
		}
		if err = checkRows(insertResult, 0, dialect.kind); err != nil {
			return err
		}
	}
	statement := fmt.Sprintf(
		"UPDATE %s SET %s = ? WHERE %s = ? AND %s = ?",
		table,
		value,
		id,
		value,
	)
	result, err := database.Exec(ctx, statement, 20, 2, 10)
	if err != nil {
		return gerror.Wrapf(err, "%s 条件写入能力探测失败", dialect.kind)
	}
	if err = checkRows(result, 1, dialect.kind); err != nil {
		return err
	}
	result, err = database.Exec(ctx, statement, 30, 2, 10)
	if err != nil {
		return gerror.Wrapf(err, "%s 过期条件写入探测失败", dialect.kind)
	}

	return checkRows(result, 0, dialect.kind)
}

func insertSQL(dialect Dialect, tableName string) (string, error) {
	if dialect.kind == MySQL {
		return "", gerror.New("MySQL 条件插入由具体 Store 处理")
	}
	table, id, value, err := probeIdentifiers(dialect, tableName)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"INSERT INTO %s (%s, %s) VALUES (?, ?) ON CONFLICT (%s) DO NOTHING",
		table,
		id,
		value,
		id,
	), nil
}

func checkRows(result sql.Result, expected int64, kind Kind) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return gerror.Wrapf(err, "读取 %s 条件写入行数", kind)
	}
	if affected != expected {
		return gerror.Newf("%s 条件写入行数错误: 期望 %d, 实际 %d", kind, expected, affected)
	}

	return nil
}

func probeSkipLocked(ctx context.Context, database gdb.DB, dialect Dialect, tableName string) error {
	table, id, _, err := probeIdentifiers(dialect, tableName)
	if err != nil {
		return err
	}
	return database.Transaction(ctx, func(ctx context.Context, transaction gdb.TX) error {
		record, err := transaction.Ctx(ctx).GetOne(
			fmt.Sprintf("SELECT %s FROM %s WHERE %s = ? FOR UPDATE SKIP LOCKED", id, table, id),
			2,
		)
		if err != nil {
			return gerror.Wrapf(err, "%s Skip Locked 能力探测失败", dialect.kind)
		}
		if record.IsEmpty() {
			return gerror.Newf("%s Skip Locked 探测未读取到候选行", dialect.kind)
		}

		return nil
	})
}

func probeIdentifiers(dialect Dialect, tableName string) (table string, id string, value string, err error) {
	table, err = dialect.Quote(tableName)
	if err != nil {
		return "", "", "", err
	}
	id, err = dialect.Quote("id")
	if err != nil {
		return "", "", "", err
	}
	value, err = dialect.Quote("value")
	if err != nil {
		return "", "", "", err
	}

	return table, id, value, nil
}

func checkMySQLDefault(ctx context.Context, database gdb.DB) error {
	value, err := database.GetValue(ctx, "SELECT @@default_storage_engine")
	if err != nil {
		return gerror.Wrap(err, "读取 MySQL 默认存储引擎")
	}
	if !strings.EqualFold(value.String(), "InnoDB") {
		return gerror.Newf("MySQL 默认存储引擎必须为 InnoDB，实际为 %s", value.String())
	}

	return nil
}

func checkMySQLTables(ctx context.Context, database gdb.DB, tables []string) error {
	for _, table := range tables {
		value, err := database.GetValue(
			ctx,
			"SELECT ENGINE FROM information_schema.TABLES "+
				"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?",
			table,
		)
		if err != nil {
			return gerror.Wrapf(err, "读取 MySQL 表 %s 存储引擎", table)
		}
		if value.IsNil() || value.String() == "" {
			return gerror.Newf("MySQL 交易表 %s 不存在", table)
		}
		if !strings.EqualFold(value.String(), "InnoDB") {
			return gerror.Newf("MySQL 交易表 %s 必须使用 InnoDB，实际为 %s", table, value.String())
		}
	}

	return nil
}

func (d Dialect) capabilities() Capabilities {
	capabilities := Capabilities{
		Transactions:     true,
		ConditionalWrite: true,
	}
	if d.kind != SQLite {
		capabilities.RowLock = true
		capabilities.SkipLocked = true
		capabilities.NativeComments = true
	}

	return capabilities
}
