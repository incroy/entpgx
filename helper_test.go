package entpgx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/incroy/entpgx"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/require"
)

func TestWithTxCapture(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	drv := entpgx.NewDriver(mock)
	box := &entpgx.TxBox{}
	ctx := entpgx.WithTxCapture(context.Background(), box)

	mock.ExpectBegin()
	mock.ExpectCommit()

	tx, err := drv.Tx(ctx)
	require.NoError(t, err)

	require.Equal(t, tx, box.Tx)
	require.NotNil(t, box.Tx.Raw())

	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRunInTx_Commit(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	drv := entpgx.NewDriver(mock)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users \\(name\\) VALUES \\(\\$1\\)").
		WithArgs("a8m").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	err = entpgx.RunInTx(context.Background(), drv, func(ctx context.Context, tx *entpgx.Tx) error {
		return tx.Exec(ctx, "INSERT INTO users (name) VALUES ($1)", []any{"a8m"}, nil)
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRunInTx_RollbackOnError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	drv := entpgx.NewDriver(mock)
	fnErr := errors.New("fn failed")

	mock.ExpectBegin()
	mock.ExpectRollback()

	err = entpgx.RunInTx(context.Background(), drv, func(ctx context.Context, tx *entpgx.Tx) error {
		return fnErr
	})
	require.ErrorIs(t, err, fnErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRunInTx_RollbackOnPanic(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	drv := entpgx.NewDriver(mock)

	mock.ExpectBegin()
	mock.ExpectRollback()

	defer func() {
		r := recover()
		require.Equal(t, "test panic", r)
		require.NoError(t, mock.ExpectationsWereMet())
	}()

	_ = entpgx.RunInTx(context.Background(), drv, func(ctx context.Context, tx *entpgx.Tx) error {
		panic("test panic")
	})
}
