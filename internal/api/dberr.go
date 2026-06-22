package api

import (
	"errors"

	"github.com/go-sql-driver/mysql"
)

// isDuplicateEntry reports whether err is a MySQL ER_DUP_ENTRY (1062), which
// fires on UNIQUE / PRIMARY KEY violations. Replaces fragile string matching
// of the error message text ("Duplicate ..."), which breaks on locale or
// driver-version changes.
func isDuplicateEntry(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
