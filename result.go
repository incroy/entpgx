package entpgx

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// result implements database/sql.Result over a pgconn.CommandTag, the
// value pgx returns from Exec.
type result struct {
	tag pgconn.CommandTag
}

var _ (sql.Result) = (*result)(nil)

// LastInsertId is not supported: PostgreSQL has no equivalent of MySQL/
// SQLite's auto-increment "last insert id" concept, and ent's postgres
// dialect never calls this -- ID retrieval after INSERT is done via
// `RETURNING id` plus Query/Scan, not Exec. Implemented for interface
// completeness, in case third-party code calls it directly.
func (r *result) LastInsertId() (int64, error) {
	return 0, errors.New("entpgx: LastInsertId is not supported by postgres; ent uses RETURNING for this")
}

// RowsAffected returns the number of rows affected by the statement, as
// reported by PostgreSQL's command tag.
func (r *result) RowsAffected() (int64, error) {
	return r.tag.RowsAffected(), nil
}
