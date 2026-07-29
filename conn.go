package entpgx

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"reflect"
	"regexp"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ExecQuerier is the minimal subset of *pgxpool.Pool, pgx.Tx, and
// *pgxpool.Conn that exec/doQuery need. All three types satisfy it with
// identical signatures, which is what lets Driver (pool-backed), Tx
// (transaction-backed), and the pinned-connection path used by session
// variables (see vars.go) share one implementation.
type ExecQuerier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// toArgs converts the `args any` parameter ent passes (always a []any in
// practice) into a concrete []any, or reports a clear error if some other
// dialect.Driver caller passed something unexpected.
func toArgs(args any) ([]any, error) {
	if args == nil {
		return nil, nil
	}
	argv, ok := args.([]any)
	if !ok {
		return nil, fmt.Errorf("entpgx: invalid type %T for args, expected []any", args)
	}
	return argv, nil
}

// exec implements the dialect.ExecQuerier.Exec contract against any
// pgxExecQuerier (pool, transaction, or pinned connection).
func exec(ctx context.Context, c ExecQuerier, query string, args, v any) error {
	argv, err := toArgs(args)
	if err != nil {
		return err
	}
	ex, cleanup, err := withSessionVars(ctx, c)
	if err != nil {
		return err
	}
	tag, execErr := ex.Exec(ctx, query, argv...)
	// Exec is fully synchronous (no rows are streamed back to the caller),
	// so -- unlike doQuery -- cleanup can run immediately here instead of
	// being deferred until something downstream closes a cursor.
	if cleanup != nil {
		if cerr := cleanup(); cerr != nil {
			execErr = errors.Join(execErr, cerr)
		}
	}
	if execErr != nil {
		return execErr
	}
	if v == nil {
		return nil
	}
	res, ok := v.(*stdsql.Result)
	if !ok {
		return fmt.Errorf("entpgx: invalid type %T for result, expected *sql.Result", v)
	}
	*res = &result{tag: tag}
	return nil
}

// doQuery implements the dialect.ExecQuerier.Query contract against any
// pgxExecQuerier (pool, transaction, or pinned connection).
func doQuery(ctx context.Context, c ExecQuerier, query string, args, v any) error {
	argv, err := toArgs(args)
	if err != nil {
		return err
	}
	ex, cleanup, err := withSessionVars(ctx, c)
	if err != nil {
		return err
	}
	rows, err := ex.Query(ctx, query, argv...)
	if err != nil {
		if cleanup != nil {
			err = errors.Join(err, cleanup())
		}
		return err
	}
	vr, ok := v.(*entsql.Rows)
	if !ok {
		rows.Close()
		if cleanup != nil {
			cleanup()
		}
		return fmt.Errorf("entpgx: invalid type %T for rows, expected *sql.Rows", v)
	}
	// IMPORTANT: cleanup (which, on the pool path, RESETs the variables and
	// releases the pinned connection back to the pool) must NOT run yet --
	// the caller hasn't read any rows through *vr at this point. It's
	// handed to rowsAdapter and only runs when the caller closes the rows,
	// which ent always does (directly, or via entsql.ScanSlice/ScanOne).
	// Running it here would return a still-in-use connection to the pool
	// while the caller is still actively scanning it.
	*vr = entsql.Rows{ColumnScanner: &rowsAdapter{rows: rows, cleanup: cleanup}}
	return nil
}

// varNameRE restricts session/GUC variable names to a safe identifier
// shape (optionally dotted, e.g. "app.tenant_id") before they're
// interpolated into a RESET statement -- Postgres has no bind-parameter
// form for RESET's target. Variable *values* are never interpolated; they
// always go through set_config() as real bind parameters.
var varNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// withSessionVars resolves the effective pgxExecQuerier to run a statement
// against, given any session variables attached to ctx via WithVar. If
// none are attached, c is returned unchanged with a nil cleanup. Otherwise:
//
//   - c is *pgxpool.Pool: acquires and pins one physical connection, sets
//     each variable via set_config(..., false) (SET) on it, and returns a
//     cleanup func that RESETs each unique variable and releases the
//     connection back to the pool.
//   - c is pgx.Tx: sets each variable via set_config(..., true) (the
//     SET LOCAL equivalent) on the same transaction. No cleanup is
//     needed -- Postgres reverts SET LOCAL automatically at
//     COMMIT/ROLLBACK.
type poolAcquirer interface {
	ExecQuerier
	Acquire(ctx context.Context) (*pgxpool.Conn, error)
}

func withSessionVars(ctx context.Context, c ExecQuerier) (ExecQuerier, func() error, error) {
	sv, _ := ctx.Value(ctxVarsKey{}).(sessionVars)
	if len(sv.vars) == 0 {
		// The common case: no entpgx.WithVar variables on this context.
		// Before proceeding, do a best-effort check for the classic
		// mistake of calling entgo.io/ent/dialect/sql.WithVar instead of
		// entpgx.WithVar (see the WithVar doc comment) -- that call
		// succeeds silently and just never reaches this driver, since it
		// writes into a context key private to a different package.
		if err := detectMisdirectedEntSQLVar(ctx); err != nil {
			return nil, nil, err
		}
		return c, nil, nil
	}
	switch e := c.(type) {
	case poolAcquirer:
		return withPoolSessionVars(ctx, e, sv)
	case pgx.Tx:
		return withTxSessionVars(ctx, e, sv)
	default:
		// Unknown executor type (e.g. a raw *pgxpool.Conn passed directly,
		// which this package never does itself): session variables aren't
		// supported for it. Fail loudly rather than silently ignoring
		// variables the caller explicitly asked to be set.
		return nil, nil, fmt.Errorf("entpgx: session variables requested via WithVar are not supported for executor type %T", c)
	}
}

// detectMisdirectedEntSQLVar is a best-effort, fail-safe check for the
// common mistake of calling entgo.io/ent/dialect/sql.WithVar instead of
// entpgx.WithVar. It walks the context chain looking for entsql's private
// ctxVarsKey{} context value and, if found with at least one variable
// attached, returns a descriptive error instead of letting the mistake
// pass silently.
//
// This relies on the internal representation of context.WithValue (an
// unexported *context.valueCtx wrapping "key"/"val"/embedded "Context"
// fields) -- stable in practice since Go 1.7, but not a documented
// contract -- and on matching entsql's private ctxVarsKey type by package
// path and name via reflection, without ever calling reflect.Value.Interface()
// on an unexported field (so no unsafe package is needed). If the shape
// ever changes in a future Go or ent release, this simply stops firing --
// it never affects the variables entpgx itself understands, so failure
// here is silent-safe, not silent-broken. Wrapped in recover() as an extra
// guard for the same reason.
func detectMisdirectedEntSQLVar(ctx context.Context) (err error) {
	defer func() {
		if recover() != nil {
			err = nil // best-effort only; never let this check itself panic
		}
	}()
	v := reflect.ValueOf(ctx)
	for i := 0; i < 32 && v.IsValid(); i++ { // 32: generous bound against any cyclic/unexpected shape
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return nil
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return nil
		}
		if keyField := v.FieldByName("key"); keyField.IsValid() &&
			keyField.Kind() == reflect.Interface && !keyField.IsNil() {
			kt := keyField.Elem().Type()
			if kt.PkgPath() == "entgo.io/ent/dialect/sql" && kt.Name() == "ctxVarsKey" {
				return fmt.Errorf("entpgx: found a session variable set via entgo.io/ent/dialect/sql.WithVar on this context, " +
					"but entpgx does not read that package's context key -- use entpgx.WithVar instead " +
					"(see the WithVar doc comment for why they aren't interchangeable)")
			}
		}
		next := v.FieldByName("Context")
		if !next.IsValid() {
			return nil
		}
		if next.Kind() == reflect.Interface {
			if next.IsNil() {
				return nil
			}
			next = next.Elem()
		}
		v = next
	}
	return nil
}

func withPoolSessionVars(ctx context.Context, pool poolAcquirer, sv sessionVars) (ExecQuerier, func() error, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return withDirectSessionVars(ctx, pool, sv)
	}
	reset := make([]string, 0, len(sv.vars))
	seen := make(map[string]struct{}, len(sv.vars))
	for _, s := range sv.vars {
		if _, ok := seen[s.k]; !ok {
			if !varNameRE.MatchString(s.k) {
				conn.Release()
				return nil, nil, fmt.Errorf("entpgx: invalid session variable name %q", s.k)
			}
			reset = append(reset, s.k)
			seen[s.k] = struct{}{}
		}
		if _, err := conn.Exec(ctx, "SELECT set_config($1, $2, false)", s.k, s.v); err != nil {
			conn.Release()
			return nil, nil, fmt.Errorf("entpgx: setting session variable %q: %w", s.k, err)
		}
	}
	cleanup := func() error {
		defer conn.Release()
		for _, name := range reset {
			if _, err := conn.Exec(ctx, fmt.Sprintf("RESET %s", name)); err != nil {
				return fmt.Errorf("entpgx: resetting session variable %q: %w", name, err)
			}
		}
		return nil
	}
	return conn, cleanup, nil
}

func withDirectSessionVars(ctx context.Context, c ExecQuerier, sv sessionVars) (ExecQuerier, func() error, error) {
	reset := make([]string, 0, len(sv.vars))
	seen := make(map[string]struct{}, len(sv.vars))
	for _, s := range sv.vars {
		if _, ok := seen[s.k]; !ok {
			if !varNameRE.MatchString(s.k) {
				return nil, nil, fmt.Errorf("entpgx: invalid session variable name %q", s.k)
			}
			reset = append(reset, s.k)
			seen[s.k] = struct{}{}
		}
		if _, err := c.Exec(ctx, "SELECT set_config($1, $2, false)", s.k, s.v); err != nil {
			return nil, nil, fmt.Errorf("entpgx: setting session variable %q: %w", s.k, err)
		}
	}
	cleanup := func() error {
		for _, name := range reset {
			if _, err := c.Exec(ctx, fmt.Sprintf("RESET %s", name)); err != nil {
				return fmt.Errorf("entpgx: resetting session variable %q: %w", name, err)
			}
		}
		return nil
	}
	return c, cleanup, nil
}

func withTxSessionVars(ctx context.Context, tx pgx.Tx, sv sessionVars) (ExecQuerier, func() error, error) {
	for _, s := range sv.vars {
		if !varNameRE.MatchString(s.k) {
			return nil, nil, fmt.Errorf("entpgx: invalid session variable name %q", s.k)
		}
		if _, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", s.k, s.v); err != nil {
			return nil, nil, fmt.Errorf("entpgx: setting session variable %q: %w", s.k, err)
		}
	}
	// No cleanup: SET LOCAL (which set_config(..., true) is equivalent to)
	// is automatically reverted by Postgres at COMMIT or ROLLBACK.
	return tx, nil, nil
}
