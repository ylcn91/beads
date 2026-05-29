package dberrors

import (
	"errors"
	"regexp"
	"strings"

	mysql "github.com/go-sql-driver/mysql"
)

var (
	quotedTableMissingPattern   = regexp.MustCompile(`(?i)\btable\s+'[^']+'\s+(doesn't exist|does not exist)\b`)
	unquotedTableMissingPattern = regexp.MustCompile("(?i)^table\\s+`?[^\\s'`]+`?\\s+(doesn't exist|does not exist)\\b")
)

// IsTableNotExist reports whether err is specifically a MySQL/Dolt
// table-not-found error. It intentionally does not classify missing columns,
// schemas, or other objects as optional-table absence.
func IsTableNotExist(err error) bool {
	if err == nil {
		return false
	}

	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1146
	}

	s := strings.ToLower(err.Error())
	return strings.Contains(s, "error 1146") ||
		quotedTableMissingPattern.MatchString(s) ||
		unquotedTableMissingPattern.MatchString(s)
}

// IsSerializationConflict reports whether err is a MySQL/Dolt serialization
// failure that guarantees the transaction was rolled back, so a read-modify-
// write operation is safe to retry from scratch:
//   - 1213 (ER_LOCK_DEADLOCK): concurrent transactions conflict at commit time
//   - 1205 (ER_LOCK_WAIT_TIMEOUT): lock wait exceeded, transaction rolled back
func IsSerializationConflict(err error) bool {
	if err == nil {
		return false
	}
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	return mysqlErr.Number == 1213 || mysqlErr.Number == 1205
}
