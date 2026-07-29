package entpgx

import (
	"context"
	"errors"

	"entgo.io/ent/dialect"
	"github.com/jackc/pgx/v5"
)

// Tx wraps a pgx.Tx and implements:
//
//   - dialect.Tx, so ent's generated Client.Tx / Client.BeginTx can drive
//     it exactly like they would entgo.io/ent/dialect/sql.Tx.
//   - dialect.Driver, so a *Tx can also be handed directly to
//     ent.NewClient(ent.Driver(tx)) when you start the transaction
//     yourself (see RunInTx in helpers.go) and want an *ent.Client bound
//     to it while keeping a handle on the raw pgx.Tx.
type Tx struct {
	ctx  context.Context // context this tx must be committed/rolled back with
	tx   pgx.Tx
	done bool
}

var (
	_ dialect.Tx                = (*Tx)(nil)
	_ dialect.Driver            = (*Tx)(nil)
	_ interface{ Raw() pgx.Tx } = (*Tx)(nil)
	_ ExecQuerier               = (pgx.Tx)(nil)
)

func newTx(ctx context.Context, pgxTx pgx.Tx) *Tx {
	return &Tx{ctx: ctx, tx: pgxTx}
}

// Raw returns the underlying pgx.Tx for pgx-native operations (CopyFrom,
// LISTEN/NOTIFY, large objects, prepared statements, etc.) that need to run
// inside the exact same transaction ent is using.
func (t *Tx) Raw() pgx.Tx { return t.tx }

// Exec implements dialect.ExecQuerier.
func (t *Tx) Exec(ctx context.Context, query string, args, v any) error {
	return exec(ctx, t.tx, query, args, v)
}

// Query implements dialect.ExecQuerier.
func (t *Tx) Query(ctx context.Context, query string, args, v any) error {
	return doQuery(ctx, t.tx, query, args, v)
}

// Commit implements dialect.Tx (via database/sql/driver.Tx). Ent's
// generated Tx.Commit calls this with no context, so -- exactly like
// entgo.io/ent/dialect/sql.Tx -- we reuse the context the transaction was
// started with.
func (t *Tx) Commit() error {
	if t.done {
		return pgx.ErrTxClosed
	}
	t.done = true
	return t.tx.Commit(t.ctx)
}

// Rollback implements dialect.Tx (via database/sql/driver.Tx).
func (t *Tx) Rollback() error {
	if t.done {
		return nil
	}
	t.done = true
	if err := t.tx.Rollback(t.ctx); err != nil {
		if errors.Is(err, pgx.ErrTxClosed) {
			// Already committed or rolled back. database/sql.Tx.Rollback
			// treats this as a benign no-op after a successful Commit, and
			// callers frequently do `defer tx.Rollback()` right after
			// `defer` a successful commit path -- match that behavior
			// instead of surfacing an error.
			return nil
		}
		return err
	}
	return nil
}

// --- dialect.Driver, so *Tx can be passed straight to ent.NewClient ---

// Tx starts a nested transaction (SAVEPOINT) on top of this one, using
// pgx's native support for nested transactions. This lets ent's own
// (rare) internal use of nested transactions, or your own explicit
// client.Tx(ctx) call from inside a RunInTx callback, work correctly.
func (t *Tx) Tx(ctx context.Context) (dialect.Tx, error) {
	nested, err := t.tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return newTx(ctx, nested), nil
}

// Close is a no-op. A *Tx used as a dialect.Driver represents an
// already-open transaction; commit or roll it back explicitly instead
// (e.g. via RunInTx, which does this for you).
func (t *Tx) Close() error { return nil }

// Dialect implements dialect.Driver.
func (t *Tx) Dialect() string { return dialect.Postgres }
