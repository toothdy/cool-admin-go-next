package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/gogf/gf/v2/errors/gerror"

	"github.com/toothdy/cool-admin-go-next/cool-next/db/driver"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/schema"
)

type sqlStatements struct {
	kind              driver.Kind
	enqueue           string
	claimCandidates   string
	claimReadySQL     string
	claimExpiredSQL   string
	claim             string
	claimAvailable    string
	claimExpired      string
	readClaimed       string
	renew             string
	markSent          string
	markRetry         string
	markDead          string
	cleanupCandidates string
	cleanupSent       string
	topicStatuses     string
	list              string
	listByTopic       string
	show              string
	lockForReplay     string
	replayDead        string
	insertInbox       string
}

func newSQLStatements(dialect driver.Dialect) (sqlStatements, error) {
	identifiers, err := quoteIdentifiers(dialect)
	if err != nil {
		return sqlStatements{}, err
	}
	now := databaseNow(dialect.Kind())
	addFromNow := addDuration(dialect.Kind(), now)
	renewExpr := renewDeadline(dialect.Kind(), identifiers["leaseExpiresAt"], now)
	recordFields := quotedFields(identifiers, recordColumns())
	metadataFields := quotedFields(identifiers, metadataColumns())
	outboxTable := identifiers[schema.OutboxTableName]
	inboxTable := identifiers[schema.InboxTableName]
	status := identifiers["status"]
	messageID := identifiers["messageId"]
	availableAt := identifiers["availableAt"]
	leaseExpiresAt := identifiers["leaseExpiresAt"]
	createTime := identifiers["createTime"]
	claimToken := identifiers["claimToken"]
	leaseOwner := identifiers["leaseOwner"]
	attempts := identifiers["attempts"]
	updateTime := identifiers["updateTime"]
	lastError := identifiers["lastError"]
	sentAt := identifiers["sentAt"]

	statements := sqlStatements{kind: dialect.Kind()}
	statements.enqueue = fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', 0, %s, %s, %s)",
		outboxTable,
		quotedFields(identifiers, []string{
			"messageId", "topic", "messageType", "messageVersion", "messageKey", "payload", "headers",
			"status", "attempts", "availableAt", "createTime", "updateTime",
		}),
		now,
		now,
		now,
	)
	statements.claimCandidates = fmt.Sprintf(
		"SELECT %s FROM %s WHERE ((%s IN ('pending', 'retry') AND %s <= %s) OR "+
			"(%s = 'leased' AND %s <= %s)) ORDER BY CASE WHEN %s = 'leased' THEN %s ELSE %s END, %s, %s LIMIT ?",
		messageID,
		outboxTable,
		status,
		availableAt,
		now,
		status,
		leaseExpiresAt,
		now,
		status,
		leaseExpiresAt,
		availableAt,
		createTime,
		messageID,
	)
	if dialect.Kind() != driver.SQLite {
		statements.claimCandidates += " FOR UPDATE SKIP LOCKED"
	}
	statements.claimReadySQL = fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s IN ('pending', 'retry') AND %s <= %s "+
			"ORDER BY %s, %s, %s LIMIT ?",
		messageID,
		outboxTable,
		status,
		availableAt,
		now,
		availableAt,
		createTime,
		messageID,
	)
	statements.claimExpiredSQL = fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s = 'leased' AND %s <= %s ORDER BY %s, %s LIMIT ?",
		messageID,
		outboxTable,
		status,
		leaseExpiresAt,
		now,
		leaseExpiresAt,
		messageID,
	)
	if dialect.Kind() != driver.SQLite {
		statements.claimReadySQL += " FOR UPDATE SKIP LOCKED"
		statements.claimExpiredSQL += " FOR UPDATE SKIP LOCKED"
	}
	statements.claim = fmt.Sprintf(
		"UPDATE %s SET %s = CASE WHEN %s IN ('pending', 'retry') THEN %s + 1 ELSE %s END, "+
			"%s = 'leased', %s = ?, %s = ?, %s = %s, %s = %s "+
			"WHERE %s = ? AND ((%s IN ('pending', 'retry') AND %s <= %s) OR (%s = 'leased' AND %s <= %s))",
		outboxTable,
		attempts,
		status,
		attempts,
		attempts,
		status,
		leaseOwner,
		claimToken,
		leaseExpiresAt,
		addFromNow,
		updateTime,
		now,
		messageID,
		status,
		availableAt,
		now,
		status,
		leaseExpiresAt,
		now,
	)
	statements.claimAvailable = fmt.Sprintf(
		"UPDATE %s SET %s = 'leased', %s = ?, %s = ?, %s = %s, %s = %s + 1, %s = %s "+
			"WHERE %s = ? AND %s IN ('pending', 'retry') AND %s <= %s",
		outboxTable,
		status,
		leaseOwner,
		claimToken,
		leaseExpiresAt,
		addFromNow,
		attempts,
		attempts,
		updateTime,
		now,
		messageID,
		status,
		availableAt,
		now,
	)
	statements.claimExpired = fmt.Sprintf(
		"UPDATE %s SET %s = 'leased', %s = ?, %s = ?, %s = %s, %s = %s "+
			"WHERE %s = ? AND %s = 'leased' AND %s <= %s",
		outboxTable,
		status,
		leaseOwner,
		claimToken,
		leaseExpiresAt,
		addFromNow,
		updateTime,
		now,
		messageID,
		status,
		leaseExpiresAt,
		now,
	)
	statements.readClaimed = fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s = ? AND %s = 'leased' AND %s = ?",
		recordFields,
		outboxTable,
		messageID,
		status,
		claimToken,
	)
	statements.renew = fmt.Sprintf(
		"UPDATE %s SET %s = %s, %s = %s WHERE %s = ? AND %s = 'leased' AND %s = ?",
		outboxTable,
		leaseExpiresAt,
		renewExpr,
		updateTime,
		now,
		messageID,
		status,
		claimToken,
	)
	statements.markSent = fmt.Sprintf(
		"UPDATE %s SET %s = 'sent', %s = %s, %s = NULL, %s = NULL, %s = NULL, %s = NULL, %s = %s "+
			"WHERE %s = ? AND %s = 'leased' AND %s = ?",
		outboxTable,
		status,
		sentAt,
		now,
		leaseOwner,
		claimToken,
		leaseExpiresAt,
		lastError,
		updateTime,
		now,
		messageID,
		status,
		claimToken,
	)
	statements.markRetry = fmt.Sprintf(
		"UPDATE %s SET %s = 'retry', %s = %s, %s = ?, %s = NULL, %s = NULL, %s = NULL, %s = NULL, %s = %s "+
			"WHERE %s = ? AND %s = 'leased' AND %s = ?",
		outboxTable,
		status,
		availableAt,
		addFromNow,
		lastError,
		leaseOwner,
		claimToken,
		leaseExpiresAt,
		sentAt,
		updateTime,
		now,
		messageID,
		status,
		claimToken,
	)
	statements.markDead = fmt.Sprintf(
		"UPDATE %s SET %s = 'dead', %s = ?, %s = NULL, %s = NULL, %s = NULL, %s = NULL, %s = %s "+
			"WHERE %s = ? AND %s = 'leased' AND %s = ?",
		outboxTable,
		status,
		lastError,
		leaseOwner,
		claimToken,
		leaseExpiresAt,
		sentAt,
		updateTime,
		now,
		messageID,
		status,
		claimToken,
	)
	statements.cleanupCandidates = fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s = 'sent' AND %s <= %s ORDER BY %s, %s LIMIT ?",
		messageID,
		outboxTable,
		status,
		sentAt,
		addFromNow,
		sentAt,
		messageID,
	)
	statements.cleanupSent = fmt.Sprintf(
		"DELETE FROM %s WHERE %s = ? AND %s = 'sent' AND %s <= %s",
		outboxTable,
		messageID,
		status,
		sentAt,
		addFromNow,
	)
	statements.topicStatuses = fmt.Sprintf(
		"SELECT %s AS %s, %s AS %s, COUNT(*) AS %s, MIN(%s) AS %s, %s AS %s "+
			"FROM %s GROUP BY %s, %s ORDER BY %s, %s",
		identifiers["topic"],
		identifiers["topic"],
		status,
		status,
		identifiers["count"],
		createTime,
		identifiers["oldestTime"],
		now,
		identifiers["databaseTime"],
		outboxTable,
		identifiers["topic"],
		status,
		identifiers["topic"],
		status,
	)
	statements.list = fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s = ? ORDER BY %s, %s LIMIT ?",
		metadataFields,
		outboxTable,
		status,
		createTime,
		messageID,
	)
	statements.listByTopic = fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s = ? AND %s = ? ORDER BY %s, %s LIMIT ?",
		metadataFields,
		outboxTable,
		status,
		identifiers["topic"],
		createTime,
		messageID,
	)
	statements.show = fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s = ?",
		metadataFields,
		outboxTable,
		messageID,
	)
	statements.lockForReplay = statements.show
	if dialect.Kind() != driver.SQLite {
		statements.lockForReplay += " FOR UPDATE"
	}
	statements.replayDead = fmt.Sprintf(
		"UPDATE %s SET %s = 'retry', %s = 0, %s = %s, %s = NULL, %s = NULL, %s = NULL, %s = NULL, %s = NULL, %s = %s "+
			"WHERE %s = ? AND %s = 'dead'",
		outboxTable,
		status,
		attempts,
		availableAt,
		now,
		leaseOwner,
		claimToken,
		leaseExpiresAt,
		sentAt,
		lastError,
		updateTime,
		now,
		messageID,
		status,
	)
	statements.insertInbox = inboxInsertSQL(dialect.Kind(), identifiers, inboxTable)

	return statements, nil
}

func (statements sqlStatements) claimArguments(
	owner string,
	token ClaimToken,
	duration time.Duration,
	messageID string,
) []any {
	return []any{owner, token, statements.durationArgument(duration), messageID}
}

func (statements sqlStatements) renewArguments(
	duration time.Duration,
	messageID string,
	token ClaimToken,
) []any {
	value := statements.durationArgument(duration)
	if statements.kind == driver.MySQL {
		return []any{value, value, messageID, token}
	}

	return []any{value, messageID, token}
}

func (statements sqlStatements) retryArguments(
	duration time.Duration,
	summary string,
	messageID string,
	token ClaimToken,
) []any {
	return []any{statements.durationArgument(duration), summary, messageID, token}
}

func (statements sqlStatements) durationArgument(duration time.Duration) any {
	microseconds := duration.Microseconds()
	if duration > 0 && microseconds == 0 {
		microseconds = 1
	}
	if statements.kind == driver.SQLite {
		if duration > 0 && microseconds < int64(time.Millisecond/time.Microsecond) {
			microseconds = int64(time.Millisecond / time.Microsecond)
		}
		return fmt.Sprintf("%+.6f seconds", float64(microseconds)/float64(time.Second/time.Microsecond))
	}

	return microseconds
}

func quoteIdentifiers(dialect driver.Dialect) (map[string]string, error) {
	names := append(recordColumns(),
		"consumer",
		"processedAt",
		"count",
		"oldestTime",
		"databaseTime",
		schema.OutboxTableName,
		schema.InboxTableName,
	)
	identifiers := make(map[string]string, len(names))
	for _, name := range names {
		quoted, err := dialect.Quote(name)
		if err != nil {
			return nil, gerror.Wrapf(err, "outbox store: 引用标识符 %s", name)
		}
		identifiers[name] = quoted
	}

	return identifiers, nil
}

func recordColumns() []string {
	return []string{
		"messageId",
		"topic",
		"messageType",
		"messageVersion",
		"messageKey",
		"payload",
		"headers",
		"status",
		"attempts",
		"availableAt",
		"leaseOwner",
		"claimToken",
		"leaseExpiresAt",
		"lastError",
		"createTime",
		"updateTime",
		"sentAt",
	}
}

func metadataColumns() []string {
	return []string{
		"messageId",
		"topic",
		"messageType",
		"messageVersion",
		"status",
		"attempts",
		"availableAt",
		"leaseExpiresAt",
		"lastError",
		"createTime",
		"updateTime",
		"sentAt",
	}
}

func quotedFields(identifiers map[string]string, fields []string) string {
	quoted := make([]string, len(fields))
	for index, field := range fields {
		quoted[index] = identifiers[field]
	}

	return strings.Join(quoted, ", ")
}

func databaseNow(kind driver.Kind) string {
	switch kind {
	case driver.MySQL:
		return "CURRENT_TIMESTAMP(6)"
	case driver.PostgreSQL:
		return "CURRENT_TIMESTAMP"
	case driver.SQLite:
		return "STRFTIME('%Y-%m-%d %H:%M:%f', 'now')"
	default:
		return ""
	}
}

func addDuration(kind driver.Kind, source string) string {
	switch kind {
	case driver.MySQL:
		return "DATE_ADD(" + source + ", INTERVAL ? MICROSECOND)"
	case driver.PostgreSQL:
		return source + " + (? * INTERVAL '1 microsecond')"
	case driver.SQLite:
		return "STRFTIME('%Y-%m-%d %H:%M:%f', " + source + ", ?)"
	default:
		return ""
	}
}

func renewDeadline(kind driver.Kind, deadline string, now string) string {
	switch kind {
	case driver.MySQL:
		return "GREATEST(DATE_ADD(" + deadline + ", INTERVAL ? MICROSECOND), DATE_ADD(" + now + ", INTERVAL ? MICROSECOND))"
	case driver.PostgreSQL:
		return "GREATEST(" + deadline + ", " + now + ") + (? * INTERVAL '1 microsecond')"
	case driver.SQLite:
		return "STRFTIME('%Y-%m-%d %H:%M:%f', CASE WHEN JULIANDAY(" + deadline + ") > JULIANDAY('now') THEN " + deadline + " ELSE 'now' END, ?)"
	default:
		return ""
	}
}

func inboxInsertSQL(kind driver.Kind, identifiers map[string]string, table string) string {
	consumer := identifiers["consumer"]
	messageID := identifiers["messageId"]
	processedAt := identifiers["processedAt"]
	now := databaseNow(kind)
	statement := fmt.Sprintf(
		"INSERT INTO %s (%s, %s, %s) VALUES (?, ?, %s)",
		table,
		consumer,
		messageID,
		processedAt,
		now,
	)
	switch kind {
	case driver.PostgreSQL:
		return statement + fmt.Sprintf(" ON CONFLICT (%s, %s) DO NOTHING RETURNING %s", consumer, messageID, messageID)
	case driver.SQLite:
		return statement + " ON CONFLICT DO NOTHING"
	default:
		return statement
	}
}
