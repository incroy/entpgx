package entpgx

import (
	"context"
	"strconv"
)

// ctxVarsKey is the context key used to attach and read pending session
// variables (see WithVar). It is intentionally unexported and private to
// this package.
type ctxVarsKey struct{}

// sessionVars holds session/transaction variables to be set before the
// next statement(s) executed with a given context.
type sessionVars struct {
	vars []struct{ k, v string }
}

// WithVar returns a new context carrying a Postgres session variable (a
// GUC -- e.g. a custom "app.tenant_id" setting read by a row-level-security
// policy via current_setting()) to be set before the next statement(s)
// executed with it.
//
// IMPORTANT: this is entpgx's own copy of the pattern used by
// entgo.io/ent/dialect/sql.WithVar, and it is NOT interchangeable with it.
// entsql.WithVar attaches its value under a context key private to the
// entgo.io/ent/dialect/sql package, which entpgx has no way to read (and
// vice versa). If you're used to entsql.WithVar/VarFromContext, replace
// those calls with entpgx.WithVar/entpgx.VarFromContext.
//
// Behavior differs depending on whether the context reaches a query with
// an active ent transaction or not:
//
//   - No active transaction (plain client calls, going through the pool):
//     entpgx pins one physical connection for the call, runs
//     "SELECT set_config($1, $2, false)" (the parameterized equivalent of
//     SET) for each variable, executes the statement, then RESETs each
//     variable and returns the connection to the pool.
//   - Active transaction (client.Tx / RunInTx / nested tx via SAVEPOINT):
//     entpgx runs "SELECT set_config($1, $2, true)" (the parameterized
//     equivalent of SET LOCAL) on the transaction. Postgres automatically
//     reverts SET LOCAL values at COMMIT or ROLLBACK, so there is no
//     manual reset step and no risk of a value leaking to whatever request
//     reuses the underlying connection next.
//
// Both cases use set_config(), a real parameterized function call, rather
// than string-interpolating the value into a SET statement -- Postgres has
// no bind-parameter form for SET/RESET's value position, but set_config()
// gives us one.
func WithVar(ctx context.Context, name, value string) context.Context {
	sv, _ := ctx.Value(ctxVarsKey{}).(sessionVars)
	sv.vars = append(sv.vars, struct{ k, v string }{k: name, v: value})
	return context.WithValue(ctx, ctxVarsKey{}, sv)
}

// WithIntVar calls WithVar with the string representation of value.
func WithIntVar(ctx context.Context, name string, value int) context.Context {
	return WithVar(ctx, name, strconv.Itoa(value))
}

// VarFromContext returns the pending session variable value previously
// attached with WithVar, if any.
func VarFromContext(ctx context.Context, name string) (string, bool) {
	sv, _ := ctx.Value(ctxVarsKey{}).(sessionVars)
	for _, s := range sv.vars {
		if s.k == name {
			return s.v, true
		}
	}
	return "", false
}
