// Package entpgx is a production-ready entgo.io/ent dialect.Driver
// implementation backed natively by jackc/pgx/v5. It never uses
// database/sql: connection pooling, query execution, and transactions all
// go through pgxpool.Pool / pgx.Tx directly.
//
// # Design
//
// dialect.Driver and dialect.Tx (entgo.io/ent/dialect) are plain Go
// interfaces. Ent's generated Client.Tx / Client.BeginTx call through those
// interfaces only, so a from-scratch driver requires no changes to ent's
// code generator for transactions, hooks, or the query/mutation builders to
// work.
//
// entgo.io/ent/dialect/sql.Rows is a struct with a single exported,
// embedded field of interface type ColumnScanner (Close, ColumnTypes,
// Columns, Err, Next, NextResultSet, Scan). Every builder- and
// sqlgraph-driven query in ent constructs a *sql.Rows{} and passes it into
// Driver.Query as the `v any` parameter, then only ever touches it through
// ColumnScanner. That's the seam this driver plugs into: rows.go supplies a
// ColumnScanner backed directly by pgx.Rows, so database/sql is never
// involved for ordinary CRUD, queries, hooks, or transactions -- including
// nested transactions via pgx's native SAVEPOINT support (see tx.go).
//
// # What is NOT supported: ent's built-in Atlas migrations
//
// client.Schema.Create(ctx, ...) -- ent's Atlas-based auto-migration and
// schema diffing -- does not work with this driver, or with any driver
// whose ColumnScanner isn't a literal *database/sql.Rows. This was
// confirmed empirically (not just inferred): ent's own bridge into Atlas,
// entgo.io/ent/dialect/sql/schema/atlas.go's (*db).QueryContext, does:
//
//	rows := &entsql.Rows{}
//	d.ExecQuerier.Query(ctx, query, args, rows)
//	return rows.ColumnScanner.(*sql.Rows), nil // unchecked, hardcoded
//
// That unchecked type assertion panics the moment client.Schema.Create
// issues its first query (even a trivial "SELECT version()"). It is a
// limitation in ent's Atlas integration, not something a dialect.Driver
// implementation can work around -- there is no interception point.
//
// Plain DDL execution is unaffected: drv.Exec(ctx, "CREATE TABLE ...", nil,
// nil) works fine, since Exec doesn't go through the ColumnScanner bridge.
// Only Atlas's introspection/diffing path is broken.
//
// Recommended alternatives, in order of how "production" they are:
//
//  1. Run migrations with the Atlas CLI, golang-migrate, or goose against
//     your Postgres DSN directly, independent of your app process. This is
//     standard practice for production ent deployments regardless of
//     driver -- auto-migration on every boot is normally discouraged past
//     early development.
//  2. If you want to keep calling client.Schema.Create() specifically,
//     open a second, migration-only *ent.Client backed by
//     entgo.io/ent/dialect/sql.OpenDB over a *database/sql.DB opened via
//     github.com/jackc/pgx/v5/stdlib, run the migration once at
//     startup/deploy, then discard it. Your request-serving client stays
//     entirely on entpgx.
//
// # What is NOT supported: ColumnScanner.ColumnTypes()
//
// *sql.ColumnType has no exported constructor outside the database/sql
// package itself, so a driver that never touches database/sql cannot
// legitimately produce one (see rows.go). This does not affect ent's own
// runtime CRUD/query path, which never calls ColumnTypes() -- only a
// hand-rolled "raw query into []map[string]any" utility built on top of
// ent would need it, and would need to run that specific query through a
// database/sql-backed connection instead.
//
// # Error handling
//
// Verified against ent's actual runtime, not assumed:
//
//   - ent.IsNotFound / ent.IsNotSingular work normally: they're derived
//     from row counts, never from driver-specific error text.
//   - ent.IsConstraintError works normally for unique and foreign-key
//     violations: pgconn.PgError's Error() text ("violates unique
//     constraint", "violates foreign key constraint", ...) matches the
//     substrings ent's classifier looks for, the same as it would with
//     lib/pq or pgx's database/sql-compatible stdlib driver.
//   - NOT NULL violations surface as plain Go errors, not as
//     ent.ConstraintError. This is true of ent's own postgres/database-sql
//     driver too -- ent's classifier doesn't pattern-match "violates
//     not-null constraint" into ConstraintError regardless of the
//     underlying driver -- so it is not an entpgx-specific gap.
//   - context.Canceled and context.DeadlineExceeded propagate correctly
//     through errors.Is, since pgx wraps context errors compatibly with
//     the standard library's conventions.
//
// # Third-party ent extensions
//
// Anything built purely against dialect.Driver / dialect.Tx (e.g.
// ariga/entcache, dialect.Debug for query logging, OpenTelemetry wrappers)
// composes normally, since Driver and Tx here satisfy those plain
// interfaces. entc-time tooling that never opens a runtime DB connection
// (entc codegen itself, the hedwigz/entviz schema visualizer) is
// unaffected either way. ariga/entviz is also unaffected: it opens its own
// throwaway connection via a -dev-url flag rather than using your app's
// driver.
// # Session variables (SET / SET LOCAL, e.g. for row-level security)
//
// WithVar / WithIntVar / VarFromContext (vars.go) let you attach a
// Postgres session variable to a context, to be set before the next
// statement(s) executed with it -- the common use case is a custom GUC
// like "app.tenant_id" that a row-level-security policy reads via
// current_setting("app.tenant_id").
//
// This is entpgx's own copy of the pattern entgo.io/ent/dialect/sql
// exposes as WithVar/VarFromContext, and is NOT interchangeable with it --
// see the doc comment on WithVar for why, and what to change if migrating
// from entsql.WithVar.
//
// Values are always set via set_config($1, $2, is_local), a real
// parameterized function call, never by interpolating the value into a SET
// statement. On the pool path (no active transaction), the variable is set
// on a connection pinned for the call and RESET before the connection
// returns to the pool. On the transaction path, it's set with the
// SET LOCAL equivalent, which Postgres reverts automatically at
// COMMIT/ROLLBACK -- deliberately more robust here than upstream ent's own
// implementation, whose *sql.Tx branch never runs its reset step, which
// under the standard database/sql driver can let a plain SET value outlive
// COMMIT and leak into whatever unrelated request reuses that pooled
// connection next.
package entpgx
