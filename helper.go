package entpgx

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// RunInTx begins a native pgx transaction and hands you the *entpgx.Tx
// (which is both a dialect.Tx and a dialect.Driver) so you can build an
// ent client bound to it and freely mix ent calls with pgx-native calls
// (CopyFrom, LISTEN/NOTIFY, prepared statements, etc.) in the same
// transaction. If fn returns an error, the transaction is rolled back and
// that error is returned; otherwise the transaction is committed and any
// commit error is returned.
//
// Example:
//
//	err := entpgx.RunInTx(ctx, driver, func(ctx context.Context, tx *entpgx.Tx) error {
//	    client := ent.NewClient(ent.Driver(tx))
//	    u, err := client.User.Create().SetName("a8m").Save(ctx)
//	    if err != nil {
//	        return err
//	    }
//	    // Raw pgx access in the SAME transaction, e.g. NOTIFY:
//	    _, err = tx.Raw().Exec(ctx, "SELECT pg_notify('users_channel', $1)", u.ID.String())
//	    return err
//	})
func RunInTx(ctx context.Context, d *Driver, fn func(ctx context.Context, tx *Tx) error) (err error) {
	pgxTx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("entpgx: begin: %w", err)
	}
	tx := newTx(ctx, pgxTx)

	defer func() {
		if p := recover(); p != nil {
			_ = pgxTx.Rollback(ctx)
			panic(p)
		}
	}()

	if err := fn(ctx, tx); err != nil {
		if rerr := pgxTx.Rollback(ctx); rerr != nil && rerr != pgx.ErrTxClosed {
			return fmt.Errorf("entpgx: rolling back after error %v: %w", err, rerr)
		}
		return err
	}
	if err := pgxTx.Commit(ctx); err != nil {
		return fmt.Errorf("entpgx: commit: %w", err)
	}
	return nil
}

// txBoxKey is the unexported context key used by WithTxCapture / TxBox.
type txBoxKey struct{}

// TxBox is an out-parameter used to retrieve the *entpgx.Tx (and therefore
// the raw pgx.Tx via .Tx.Raw()) that gets created inside an ordinary
// client.Tx(ctx) or client.BeginTx(ctx, opts) call, without requiring any
// changes to ent's generated code.
type TxBox struct {
	// Tx is populated by Driver.Tx/BeginTx once the transaction has been
	// opened. It is nil until then, so only read it after client.Tx(ctx)
	// (or client.BeginTx) has returned successfully.
	Tx *Tx
}

// WithTxCapture returns a context that causes any entpgx.Driver.Tx or
// Driver.BeginTx call made with it (including indirectly, via ent's
// generated Client.Tx / Client.BeginTx) to record the *entpgx.Tx it
// creates into box. Use this when you want the normal client.Tx(ctx)
// ergonomics (an *ent.Tx you call .Commit()/.Rollback() on) but still need
// the raw pgx.Tx handle for pgx-native calls in the same transaction.
//
// Example:
//
//	box := &entpgx.TxBox{}
//	entTx, err := client.Tx(entpgx.WithTxCapture(ctx, box))
//	if err != nil {
//	    return err
//	}
//	defer entTx.Rollback()
//
//	if _, err := entTx.User.Create().SetName("a8m").Save(ctx); err != nil {
//	    return err
//	}
//	// Raw pgx.Tx for the exact same transaction:
//	if _, err := box.Tx.Raw().Exec(ctx, "SELECT pg_notify('users_channel', 'a8m')"); err != nil {
//	    return err
//	}
//	return entTx.Commit()
func WithTxCapture(ctx context.Context, box *TxBox) context.Context {
	return context.WithValue(ctx, txBoxKey{}, box)
}
