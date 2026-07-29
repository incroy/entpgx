package entpgx

import (
	stdsql "database/sql"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Export internal functions and constructors for testing in entpgx_test.
var (
	ToPgxTxOptions             = toPgxTxOptions
	ToArgs                     = toArgs
	VarNameRE                  = varNameRE
	DetectMisdirectedEntSQLVar = detectMisdirectedEntSQLVar
	ExecInternal               = exec
	DoQueryInternal            = doQuery
)

func NewResult(tag pgconn.CommandTag) stdsql.Result {
	return &result{tag: tag}
}

func NewRowsAdapter(rows pgx.Rows, cleanup func() error) entsql.ColumnScanner {
	return &rowsAdapter{rows: rows, cleanup: cleanup}
}
