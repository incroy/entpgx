package entpgx_test

import (
	"context"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/incroy/entpgx"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/require"
)

func TestToArgs(t *testing.T) {
	t.Run("nil args", func(t *testing.T) {
		res, err := entpgx.ToArgs(nil)
		require.NoError(t, err)
		require.Nil(t, res)
	})

	t.Run("valid slice of any", func(t *testing.T) {
		input := []any{1, "test", true}
		res, err := entpgx.ToArgs(input)
		require.NoError(t, err)
		require.Equal(t, input, res)
	})

	t.Run("invalid args type", func(t *testing.T) {
		invalidInputs := []any{
			"string",
			123,
			[]string{"a", "b"},
			map[string]any{"a": 1},
		}
		for _, input := range invalidInputs {
			_, err := entpgx.ToArgs(input)
			require.Error(t, err)
			require.Contains(t, err.Error(), "entpgx: invalid type")
		}
	})
}

func TestVarNameRE(t *testing.T) {
	validNames := []string{
		"tenant_id",
		"app.tenant_id",
		"_var_1",
		"a.b.c",
		"APP_SETTING",
		"schema1.table2.col3",
	}
	for _, name := range validNames {
		require.True(t, entpgx.VarNameRE.MatchString(name), "expected match for %q", name)
	}

	invalidNames := []string{
		"123var",
		"app;drop",
		"app-id",
		"foo bar",
		"app.123bad",
		"app.'tenant'",
		"SELECT * FROM users",
		"",
	}
	for _, name := range invalidNames {
		require.False(t, entpgx.VarNameRE.MatchString(name), "expected reject for %q", name)
	}
}

func TestDetectMisdirectedEntSQLVar(t *testing.T) {
	t.Run("plain context", func(t *testing.T) {
		ctx := context.Background()
		require.NoError(t, entpgx.DetectMisdirectedEntSQLVar(ctx))
	})

	t.Run("entpgx.WithVar context", func(t *testing.T) {
		ctx := entpgx.WithVar(context.Background(), "app.tenant", "123")
		require.NoError(t, entpgx.DetectMisdirectedEntSQLVar(ctx))
	})

	t.Run("entsql.WithVar context", func(t *testing.T) {
		ctx := entsql.WithVar(context.Background(), "app.tenant", "123")
		err := entpgx.DetectMisdirectedEntSQLVar(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "entgo.io/ent/dialect/sql.WithVar")
	})
}

func TestExec_InvalidArgs(t *testing.T) {
	ctx := context.Background()
	err := entpgx.ExecInternal(ctx, nil, "SELECT 1", "invalid_args_type", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid type")
}

func TestExec_InvalidResultType(t *testing.T) {
	ctx := context.Background()
	dummyExec := &dummyExecQuerier{}
	invalidResTarget := "not_a_sql_result_pointer"
	err := entpgx.ExecInternal(ctx, dummyExec, "UPDATE tbl SET col = 1", []any{}, &invalidResTarget)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected *sql.Result")
}

func TestDoQuery_InvalidArgs(t *testing.T) {
	ctx := context.Background()
	err := entpgx.DoQueryInternal(ctx, nil, "SELECT 1", 12345, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid type")
}

func TestDoQuery_InvalidRowsType(t *testing.T) {
	ctx := context.Background()
	dummyExec := &dummyExecQuerier{}
	invalidRowsTarget := "not_an_entsql_rows_pointer"
	err := entpgx.DoQueryInternal(ctx, dummyExec, "SELECT 1", []any{}, &invalidRowsTarget)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected *sql.Rows")
}

func TestWithSessionVars_UnsupportedExecutor(t *testing.T) {
	ctx := entpgx.WithVar(context.Background(), "app.tenant", "123")
	dummyExec := &dummyExecQuerier{}
	err := entpgx.ExecInternal(ctx, dummyExec, "SELECT 1", []any{}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session variables requested via WithVar are not supported for executor type")
}

func TestWithSessionVars_InvalidVarName(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	drv := entpgx.NewDriver(mock)
	ctx := entpgx.WithVar(context.Background(), "123invalid_var", "val")
	err = drv.Exec(ctx, "SELECT 1", []any{}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid session variable name")
}
