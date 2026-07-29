package entpgx

import (
	stdsql "database/sql"
	"errors"

	"entgo.io/ent/dialect/sql"
	"github.com/jackc/pgx/v5"
)

// rowsAdapter adapts a pgx.Rows into entgo.io/ent/dialect/sql.ColumnScanner,
// so ent (query builders, sqlgraph, and dialect/sql/schema introspection)
// can consume results produced natively by pgx without database/sql.Rows
// ever being involved.
type rowsAdapter struct {
	rows pgx.Rows
	// cleanup, if set, is invoked exactly once from Close(), after the
	// underlying pgx.Rows is closed. It's how session variables set via
	// WithVar get RESET and a pool-pinned connection gets released back to
	// the pool -- see withPoolSessionVars in conn.go. It must run here
	// (when the caller is done with the rows) and not any earlier, or a
	// still-in-use connection could be handed back to the pool while this
	// cursor is still being read.
	cleanup func() error
}

var _ sql.ColumnScanner = (*rowsAdapter)(nil)

func (r *rowsAdapter) Next() bool { return r.rows.Next() }

// NextResultSet always reports false: a single pgx Query call always
// produces exactly one result set (unlike, say, SQL Server's MARS). Ent
// itself never issues multi-statement batches that would need this.
func (r *rowsAdapter) NextResultSet() bool { return false }

func (r *rowsAdapter) Scan(dest ...any) error {
	return r.rows.Scan(dest...)
}

func (r *rowsAdapter) Err() error { return r.rows.Err() }

func (r *rowsAdapter) Close() error {
	r.rows.Close()
	if r.cleanup != nil {
		return r.cleanup()
	}
	return nil
}

func (r *rowsAdapter) Columns() ([]string, error) {
	fds := r.rows.FieldDescriptions()
	cols := make([]string, len(fds))
	for i, fd := range fds {
		cols[i] = fd.Name
	}
	return cols, nil
}

// ColumnTypes is intentionally unsupported. *sql.ColumnType has no exported
// constructor outside the database/sql package itself, so a driver that
// never touches database/sql cannot legitimately produce one.
//
// Two call sites in ent actually reach this, with different consequences:
//
//   - entgo.io/ent/dialect/sql/schema/atlas.go's Atlas-migration bridge
//     does rows.ColumnScanner.(*sql.Rows) -- an unchecked type assertion,
//     not a ColumnTypes() call -- and panics regardless of what
//     ColumnTypes() returns. See doc.go's "What is NOT supported: ent's
//     built-in Atlas migrations" section; this is why client.Schema.Create
//     doesn't work here at all.
//
//   - entgo.io/ent/dialect/sql.ScanTypeOf (backing SelectValues, i.e.
//     GroupBy(...).Aggregate(...) and any raw/computed .Select() column
//     not in your static schema) DOES check the error from ColumnTypes()
//     and falls back to a bare *any when it fails, rather than failing the
//     query:
//
//     ct, err := rows.ColumnTypes()
//     if err != nil || len(ct) <= i { return new(any) }
//
//     This path was verified live: GroupBy+Aggregate queries return
//     correct values through entpgx despite ColumnTypes() always erroring
//     here, because pgx.Rows.Scan(*any) does its own OID-based decoding.
//     The one thing to be aware of: the concrete Go type landing in a
//     SelectValues entry may differ from what ent's own database/sql-based
//     driver would produce for the same column (e.g. a bare int64 instead
//     of sql.NullInt64) -- fine if you read the value generically, but
//     worth knowing if code type-asserts a SelectValues entry to a
//     specific concrete type.
//
// Ordinary ent generated CRUD/query code (anything scanning into your
// schema's own typed fields) never calls ColumnTypes() at all, so neither
// of the above applies to normal usage. If you're building your own
// generic "raw query into []map[string]any" utility on top of ent that
// needs real *sql.ColumnType values, run that specific query through a
// *database/sql.DB opened via github.com/jackc/pgx/v5/stdlib instead (see
// README "Schema migrations & tooling that needs database/sql").
func (r *rowsAdapter) ColumnTypes() ([]*stdsql.ColumnType, error) {
	return nil, errors.New("entpgx: ColumnTypes is not supported by the native pgx driver; see rows.go doc comment")
}
