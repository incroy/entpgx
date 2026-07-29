// Package entpgx: see doc.go for the full package documentation.
package entpgx

import (
	"context"
	stdsql "database/sql"
	"fmt"

	"entgo.io/ent/dialect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool represents the subset of *pgxpool.Pool (and pgxmock.PgxPoolIface)
// needed by Driver.
type Pool interface {
	ExecQuerier
	Begin(ctx context.Context) (pgx.Tx, error)
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
	Close()
}

// Driver is a dialect.Driver implementation that executes every operation
// through a Pool using pgx's native protocol.
type Driver struct {
	pool Pool
}

// Compile-time interface checks.
var (
	_ dialect.Driver = (*Driver)(nil)
	_ interface {
		BeginTx(context.Context, *stdsql.TxOptions) (dialect.Tx, error)
	} = (*Driver)(nil)
	// Verifies *pgxpool.Pool satisfies the minimal Pool interface.
	_ Pool = (*pgxpool.Pool)(nil)
	// Verifies *pgxpool.Conn (used to pin a connection for session
	// variables -- see vars.go / withPoolSessionVars in conn.go) also
	// satisfies it.
	_ ExecQuerier = (*pgxpool.Conn)(nil)
)

// Open parses connString (a DSN or "postgres://" URL -- anything
// pgxpool.ParseConfig accepts), builds a *pgxpool.Pool with it, pings the
// database, and returns a ready-to-use Driver.
//
// If you need to customize the pool before ent ever sees it (TLS config,
// tracers/logging via pgxpool.Config.ConnConfig.Tracer, MinConns/MaxConns,
// a custom AfterConnect for registering pgtype codecs, etc.), call
// pgxpool.ParseConfig / pgxpool.NewWithConfig yourself and use NewDriver.
func Open(ctx context.Context, connString string) (*Driver, error) {
	cfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("entpgx: parsing config: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("entpgx: creating pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("entpgx: ping: %w", err)
	}
	return NewDriver(pool), nil
}

// NewDriver wraps an already-configured Pool (such as a *pgxpool.Pool or
// a pgxmock pool) as a dialect.Driver.
func NewDriver(pool Pool) *Driver {
	return &Driver{pool: pool}
}

// Pool returns the underlying Pool.
func (d *Driver) Pool() Pool { return d.pool }

// Dialect implements dialect.Driver.
func (d *Driver) Dialect() string { return dialect.Postgres }

// Close implements dialect.Driver. It closes the pool; blocks until all
// connections are returned/closed.
func (d *Driver) Close() error {
	d.pool.Close()
	return nil
}

// Exec implements dialect.Driver / dialect.ExecQuerier.
func (d *Driver) Exec(ctx context.Context, query string, args, v any) error {
	return exec(ctx, d.pool, query, args, v)
}

// Query implements dialect.Driver / dialect.ExecQuerier.
func (d *Driver) Query(ctx context.Context, query string, args, v any) error {
	return doQuery(ctx, d.pool, query, args, v)
}

// Tx starts a native pgx transaction (default isolation, read-write) and
// returns a dialect.Tx backed by it. Every statement ent issues inside the
// transaction goes through pgx.Tx.Exec/Query -- never through database/sql.
//
// If you need the raw pgx.Tx alongside the transaction ent is using (e.g.
// to interleave client.User.Create(ctx) calls with tx.CopyFrom(...) or
// LISTEN/NOTIFY), don't use this method indirectly via client.Tx(ctx) --
// use RunInTx or WithTxCapture from helpers.go instead.
func (d *Driver) Tx(ctx context.Context) (dialect.Tx, error) {
	pgxTx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("entpgx: begin: %w", err)
	}
	tx := newTx(ctx, pgxTx)
	if box, ok := ctx.Value(txBoxKey{}).(*TxBox); ok {
		box.Tx = tx
	}
	return tx, nil
}

// BeginTx implements the structural interface ent's generated
// Client.BeginTx(ctx, opts) looks for, so client.BeginTx works exactly like
// it would with entgo.io/ent/dialect/sql.Driver.
func (d *Driver) BeginTx(ctx context.Context, opts *stdsql.TxOptions) (dialect.Tx, error) {
	pgxTx, err := d.pool.BeginTx(ctx, toPgxTxOptions(opts))
	if err != nil {
		return nil, fmt.Errorf("entpgx: begin tx: %w", err)
	}
	tx := newTx(ctx, pgxTx)
	if box, ok := ctx.Value(txBoxKey{}).(*TxBox); ok {
		box.Tx = tx
	}
	return tx, nil
}

func toPgxTxOptions(opts *stdsql.TxOptions) pgx.TxOptions {
	if opts == nil {
		return pgx.TxOptions{}
	}
	o := pgx.TxOptions{}
	if opts.ReadOnly {
		o.AccessMode = pgx.ReadOnly
	}
	switch opts.Isolation {
	case stdsql.LevelReadUncommitted:
		o.IsoLevel = pgx.ReadUncommitted
	case stdsql.LevelReadCommitted:
		o.IsoLevel = pgx.ReadCommitted
	case stdsql.LevelRepeatableRead, stdsql.LevelSnapshot:
		o.IsoLevel = pgx.RepeatableRead
	case stdsql.LevelSerializable:
		o.IsoLevel = pgx.Serializable
	default:
		// LevelDefault and anything unmapped: let Postgres use its default
		// (READ COMMITTED).
	}
	return o
}
