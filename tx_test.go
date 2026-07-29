package entpgx_test

import (
	"context"
	"testing"

	"entgo.io/ent/dialect"
	"github.com/incroy/entpgx"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/require"
)

func TestTx_Methods(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	drv := entpgx.NewDriver(mock)

	mock.ExpectBegin()
	mock.ExpectCommit()

	tx, err := drv.Tx(context.Background())
	require.NoError(t, err)

	entTx, ok := tx.(*entpgx.Tx)
	require.True(t, ok, "expected tx to be *entpgx.Tx")

	require.Equal(t, dialect.Postgres, entTx.Dialect())
	require.NoError(t, entTx.Close())
	require.NotNil(t, entTx.Raw())

	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTx_RollbackClosed(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	drv := entpgx.NewDriver(mock)

	mock.ExpectBegin()
	mock.ExpectRollback()

	tx, err := drv.Tx(context.Background())
	require.NoError(t, err)

	require.NoError(t, tx.Rollback())
	// Subsequent Rollback on closed tx should return nil without surfacing ErrTxClosed
	require.NoError(t, tx.Rollback())
}

func TestTx_Nested(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	drv := entpgx.NewDriver(mock)

	mock.ExpectBegin()
	mock.ExpectBegin()
	mock.ExpectRollback()
	mock.ExpectCommit()

	tx, err := drv.Tx(context.Background())
	require.NoError(t, err)

	entTx, ok := tx.(*entpgx.Tx)
	require.True(t, ok)

	nestedTx, err := entTx.Tx(context.Background())
	require.NoError(t, err)

	require.NoError(t, nestedTx.Rollback())
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}
