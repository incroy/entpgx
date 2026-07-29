package entpgx_test

import (
	"context"
	stdsql "database/sql"
	"errors"
	"os"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/incroy/entpgx"
	"github.com/stretchr/testify/require"
)

func getTestDSN() string {
	if dsn := os.Getenv("ENTPGX_TEST_DSN"); dsn != "" {
		return dsn
	}
	if dsn := os.Getenv("POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		return dsn
	}
	return ""
}

func TestIntegration_Postgres(t *testing.T) {
	dsn := getTestDSN()
	if dsn == "" {
		t.Skip("skipping postgres integration test: ENTPGX_TEST_DSN, POSTGRES_DSN, or DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	drv, err := entpgx.Open(ctx, dsn)
	require.NoError(t, err)
	defer drv.Close()

	require.NotNil(t, drv.Pool())

	// 1. DDL: Create temporary test table
	dropDDL := "DROP TABLE IF EXISTS entpgx_test_users"
	createDDL := `CREATE TABLE entpgx_test_users (
		id INT PRIMARY KEY,
		name TEXT NOT NULL,
		age INT NOT NULL
	)`

	require.NoError(t, drv.Exec(ctx, dropDDL, nil, nil))
	require.NoError(t, drv.Exec(ctx, createDDL, nil, nil))
	defer func() {
		_ = drv.Exec(context.Background(), dropDDL, nil, nil)
	}()

	// 2. DML: Exec INSERT
	insertQuery := "INSERT INTO entpgx_test_users (id, name, age) VALUES ($1, $2, $3)"
	var res stdsql.Result
	err = drv.Exec(ctx, insertQuery, []any{1, "Alice", 30}, &res)
	require.NoError(t, err)

	affected, err := res.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), affected)

	err = drv.Exec(ctx, insertQuery, []any{2, "Bob", 25}, &res)
	require.NoError(t, err)

	// 3. Query: SELECT rows
	selectQuery := "SELECT id, name, age FROM entpgx_test_users ORDER BY id ASC"
	var vr entsql.Rows
	err = drv.Query(ctx, selectQuery, []any{}, &vr)
	require.NoError(t, err)

	cols, err := vr.Columns()
	require.NoError(t, err)
	require.Equal(t, []string{"id", "name", "age"}, cols)

	type user struct {
		id   int
		name string
		age  int
	}
	var users []user
	for vr.Next() {
		var u user
		require.NoError(t, vr.Scan(&u.id, &u.name, &u.age))
		users = append(users, u)
	}
	require.NoError(t, vr.Close())

	expectedUsers := []user{
		{id: 1, name: "Alice", age: 30},
		{id: 2, name: "Bob", age: 25},
	}
	require.Equal(t, expectedUsers, users)

	// 4. Transaction Commit & Rollback
	t.Run("Tx Commit", func(t *testing.T) {
		tx, err := drv.Tx(ctx)
		require.NoError(t, err)

		require.NoError(t, tx.Exec(ctx, insertQuery, []any{3, "Charlie", 35}, nil))
		require.NoError(t, tx.Commit())

		// Verify Charlie exists
		var checkRows entsql.Rows
		require.NoError(t, drv.Query(ctx, "SELECT count(*) FROM entpgx_test_users WHERE id = 3", []any{}, &checkRows))
		defer checkRows.Close()

		require.True(t, checkRows.Next())
		var count int
		require.NoError(t, checkRows.Scan(&count))
		require.Equal(t, 1, count)
	})

	t.Run("Tx Rollback", func(t *testing.T) {
		tx, err := drv.Tx(ctx)
		require.NoError(t, err)

		require.NoError(t, tx.Exec(ctx, insertQuery, []any{4, "David", 40}, nil))
		require.NoError(t, tx.Rollback())

		// Verify David does NOT exist
		var checkRows entsql.Rows
		require.NoError(t, drv.Query(ctx, "SELECT count(*) FROM entpgx_test_users WHERE id = 4", []any{}, &checkRows))
		defer checkRows.Close()

		require.True(t, checkRows.Next())
		var count int
		require.NoError(t, checkRows.Scan(&count))
		require.Equal(t, 0, count)
	})

	// 5. RunInTx Helper
	t.Run("RunInTx Commit", func(t *testing.T) {
		err := entpgx.RunInTx(ctx, drv, func(ctx context.Context, tx *entpgx.Tx) error {
			return tx.Exec(ctx, insertQuery, []any{5, "Eve", 28}, nil)
		})
		require.NoError(t, err)
	})

	t.Run("RunInTx Rollback on error", func(t *testing.T) {
		customErr := errors.New("custom business logic failure")
		err := entpgx.RunInTx(ctx, drv, func(ctx context.Context, tx *entpgx.Tx) error {
			_ = tx.Exec(ctx, insertQuery, []any{6, "Frank", 50}, nil)
			return customErr
		})
		require.ErrorIs(t, err, customErr)
	})

	// 6. WithTxCapture
	t.Run("WithTxCapture", func(t *testing.T) {
		box := &entpgx.TxBox{}
		ctxCapture := entpgx.WithTxCapture(ctx, box)

		tx, err := drv.Tx(ctxCapture)
		require.NoError(t, err)
		defer tx.Rollback()

		require.NotNil(t, box.Tx)
		require.NotNil(t, box.Tx.Raw())

		// Native pgx execution using raw tx handle
		_, err = box.Tx.Raw().Exec(ctx, "SELECT 1")
		require.NoError(t, err)
	})

	// 7. WithVar Session Variables
	t.Run("Session Variables on Pool", func(t *testing.T) {
		ctxVar := entpgx.WithVar(ctx, "app.test_tenant", "tenant-100")
		var vr entsql.Rows
		err := drv.Query(ctxVar, "SELECT current_setting('app.test_tenant')", []any{}, &vr)
		require.NoError(t, err)
		require.True(t, vr.Next())

		var val string
		require.NoError(t, vr.Scan(&val))
		require.Equal(t, "tenant-100", val)
		require.NoError(t, vr.Close())
	})

	t.Run("Session Variables on Tx", func(t *testing.T) {
		tx, err := drv.Tx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		ctxVar := entpgx.WithVar(ctx, "app.test_tenant", "tenant-tx-200")
		var vr entsql.Rows
		err = tx.Query(ctxVar, "SELECT current_setting('app.test_tenant')", []any{}, &vr)
		require.NoError(t, err)
		require.True(t, vr.Next())

		var val string
		require.NoError(t, vr.Scan(&val))
		require.Equal(t, "tenant-tx-200", val)
		_ = vr.Close()
	})
}
