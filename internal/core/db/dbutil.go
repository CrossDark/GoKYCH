package db

import (
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// IsDuplicateEntry reports whether err is a MySQL ER_DUP_ENTRY (1062), which
// fires on UNIQUE / PRIMARY KEY violations. Replaces fragile string matching
// of the error message text ("Duplicate ..."), which breaks on locale or
// driver-version changes.
func IsDuplicateEntry(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

// Placeholders returns a comma-separated "?,?,?" string with n question marks.
// Useful for building IN-clauses: `... WHERE id IN (` + Placeholders(n) + `)`.
func Placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, 0, n*2-1)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '?')
	}
	return string(b)
}

// PlaceholdersJoin returns a placeholder string for n args joined by sep, which
// is handy when each IN element is a two-column (composite) row, e.g.:
//
//	`... WHERE (a,b) IN ((?,?),(?,?))` for n rows uses sep="),(" and a wrapper.
func PlaceholdersJoin(n int, sep string) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?", n)[:n] + strings.Repeat(sep+"?", n-1)
}
