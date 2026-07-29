package entpgx_test

import (
	"context"
	stdsql "database/sql"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/incroy/entpgx"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/require"
)

func TestDriver_Dialect(t *testing.T) {
	t.Parallel()

	drv := entpgx.NewDriver(nil)
	require.Equal(t, dialect.Postgres, drv.Dialect())
}

func TestDriver_Pool(t *testing.T) {
	t.Parallel()

	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	drv := entpgx.NewDriver(mock)
	require.Equal(t, mock, drv.Pool())
}

func TestOpen_InvalidConnString(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, err := entpgx.Open(ctx, "invalid://host:port/dbname?foo=bar:baz")
	require.Error(t, err)
}

func TestWithVars(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	drv := entpgx.NewDriver(mock)

	// 1. Plain query with single session variable
	mock.ExpectExec("SELECT set_config\\(\\$1, \\$2, false\\)").
		WithArgs("foo", "bar").
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectQuery("SELECT 1").
		WillReturnRows(pgxmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectExec("RESET foo").
		WillReturnResult(pgxmock.NewResult("RESET", 0))

	rows := &entsql.Rows{}
	err = drv.Query(
		entpgx.WithVar(context.Background(), "foo", "bar"),
		"SELECT 1",
		[]any{},
		rows,
	)
	require.NoError(t, err)
	require.NoError(t, rows.Close())
	require.NoError(t, mock.ExpectationsWereMet())

	// 2. Chained session variables
	mock.ExpectExec("SELECT set_config\\(\\$1, \\$2, false\\)").
		WithArgs("foo", "bar").
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectExec("SELECT set_config\\(\\$1, \\$2, false\\)").
		WithArgs("foo", "baz").
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectQuery("SELECT 1").
		WillReturnRows(pgxmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectExec("RESET foo").
		WillReturnResult(pgxmock.NewResult("RESET", 0))

	rows = &entsql.Rows{}
	err = drv.Query(
		entpgx.WithVar(entpgx.WithVar(context.Background(), "foo", "bar"), "foo", "baz"),
		"SELECT 1",
		[]any{},
		rows,
	)
	require.NoError(t, err)
	require.NoError(t, rows.Close())
	require.NoError(t, mock.ExpectationsWereMet())

	// 3. Transaction query with variable
	mock.ExpectBegin()
	mock.ExpectExec("SELECT set_config\\(\\$1, \\$2, true\\)").
		WithArgs("foo", "bar").
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectQuery("SELECT 1").
		WillReturnRows(pgxmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectCommit()

	tx, err := drv.Tx(context.Background())
	require.NoError(t, err)

	rows = &entsql.Rows{}
	err = tx.Query(
		entpgx.WithVar(context.Background(), "foo", "bar"),
		"SELECT 1",
		[]any{},
		rows,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())

	// 4. Exec with single session variable
	mock.ExpectExec("SELECT set_config\\(\\$1, \\$2, false\\)").
		WithArgs("foo", "qux").
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectExec("INSERT INTO users DEFAULT VALUES").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("RESET foo").
		WillReturnResult(pgxmock.NewResult("RESET", 0))

	err = drv.Exec(
		entpgx.WithVar(context.Background(), "foo", "qux"),
		"INSERT INTO users DEFAULT VALUES",
		[]any{},
		nil,
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestToPgxTxOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *stdsql.TxOptions
		expected pgx.TxOptions
	}{
		{
			name:     "nil options",
			input:    nil,
			expected: pgx.TxOptions{},
		},
		{
			name:     "read only",
			input:    &stdsql.TxOptions{ReadOnly: true},
			expected: pgx.TxOptions{AccessMode: pgx.ReadOnly},
		},
		{
			name:     "read uncommitted",
			input:    &stdsql.TxOptions{Isolation: stdsql.LevelReadUncommitted},
			expected: pgx.TxOptions{IsoLevel: pgx.ReadUncommitted},
		},
		{
			name:     "read committed",
			input:    &stdsql.TxOptions{Isolation: stdsql.LevelReadCommitted},
			expected: pgx.TxOptions{IsoLevel: pgx.ReadCommitted},
		},
		{
			name:     "repeatable read",
			input:    &stdsql.TxOptions{Isolation: stdsql.LevelRepeatableRead},
			expected: pgx.TxOptions{IsoLevel: pgx.RepeatableRead},
		},
		{
			name:     "snapshot mapped to repeatable read",
			input:    &stdsql.TxOptions{Isolation: stdsql.LevelSnapshot},
			expected: pgx.TxOptions{IsoLevel: pgx.RepeatableRead},
		},
		{
			name:     "serializable",
			input:    &stdsql.TxOptions{Isolation: stdsql.LevelSerializable},
			expected: pgx.TxOptions{IsoLevel: pgx.Serializable},
		},
		{
			name:     "default level",
			input:    &stdsql.TxOptions{Isolation: stdsql.LevelDefault},
			expected: pgx.TxOptions{},
		},
		{
			name:     "unknown level",
			input:    &stdsql.TxOptions{Isolation: stdsql.IsolationLevel(999)},
			expected: pgx.TxOptions{},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := entpgx.ToPgxTxOptions(tt.input)
			require.Equal(t, tt.expected, got)
		})
	}
}
