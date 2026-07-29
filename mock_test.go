package entpgx_test

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/incroy/entpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// dummyExecQuerier implements ExecQuerier for testing purposes.
type dummyExecQuerier struct {
	execFn  func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	queryFn func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (m *dummyExecQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if m.execFn != nil {
		return m.execFn(ctx, sql, args...)
	}
	return pgconn.NewCommandTag("SELECT 0"), nil
}

func (m *dummyExecQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if m.queryFn != nil {
		return m.queryFn(ctx, sql, args...)
	}
	return &dummyRows{}, nil
}

// dummyRows implements pgx.Rows for testing purposes.
type dummyRows struct {
	cols   []string
	vals   [][]any
	idx    int
	err    error
	closed bool
}

func (r *dummyRows) Close() { r.closed = true }
func (r *dummyRows) Err() error { return r.err }
func (r *dummyRows) CommandTag() pgconn.CommandTag { return pgconn.NewCommandTag("SELECT 1") }

func (r *dummyRows) FieldDescriptions() []pgconn.FieldDescription {
	fds := make([]pgconn.FieldDescription, len(r.cols))
	for i, col := range r.cols {
		fds[i] = pgconn.FieldDescription{Name: col}
	}
	return fds
}

func (r *dummyRows) Next() bool {
	if r.idx < len(r.vals) {
		r.idx++
		return true
	}
	return false
}

func (r *dummyRows) Scan(dest ...any) error {
	if r.idx <= 0 || r.idx > len(r.vals) {
		return errors.New("scan called out of bounds")
	}
	row := r.vals[r.idx-1]
	if len(dest) != len(row) {
		return fmt.Errorf("scan destination count mismatch: expected %d, got %d", len(row), len(dest))
	}
	for i, val := range row {
		switch d := dest[i].(type) {
		case *int:
			*d = val.(int)
		case *string:
			*d = val.(string)
		case *any:
			*d = val
		default:
			// best effort assignment
		}
	}
	return nil
}

func (r *dummyRows) Values() ([]any, error) {
	if r.idx <= 0 || r.idx > len(r.vals) {
		return nil, errors.New("values called out of bounds")
	}
	return r.vals[r.idx-1], nil
}

func (r *dummyRows) RawValues() [][]byte { return nil }
func (r *dummyRows) Conn() *pgx.Conn     { return nil }

func TestMockExecAndQuery(t *testing.T) {
	t.Run("exec successful with result populating", func(t *testing.T) {
		ctx := context.Background()
		execCalled := false
		dummy := &dummyExecQuerier{
			execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
				execCalled = true
				return pgconn.NewCommandTag("UPDATE 3"), nil
			},
		}

		var res stdsql.Result
		err := entpgx.ExecInternal(ctx, dummy, "UPDATE users SET active = true", []any{}, &res)
		require.NoError(t, err)
		require.True(t, execCalled)
		require.NotNil(t, res)

		affected, err := res.RowsAffected()
		require.NoError(t, err)
		require.Equal(t, int64(3), affected)
	})

	t.Run("doQuery successful with rows populating", func(t *testing.T) {
		ctx := context.Background()
		queryCalled := false
		dummyRowsObj := &dummyRows{
			cols: []string{"id", "name"},
			vals: [][]any{
				{1, "Alice"},
				{2, "Bob"},
			},
		}
		dummy := &dummyExecQuerier{
			queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
				queryCalled = true
				return dummyRowsObj, nil
			},
		}

		var vr entsql.Rows
		err := entpgx.DoQueryInternal(ctx, dummy, "SELECT id, name FROM users", []any{}, &vr)
		require.NoError(t, err)
		require.True(t, queryCalled)

		cols, err := vr.Columns()
		require.NoError(t, err)
		require.Equal(t, []string{"id", "name"}, cols)

		// Read first row
		require.True(t, vr.Next())

		var id int
		var name string
		require.NoError(t, vr.Scan(&id, &name))
		require.Equal(t, 1, id)
		require.Equal(t, "Alice", name)

		require.NoError(t, vr.Close())
	})

	t.Run("doQuery query error releases resources", func(t *testing.T) {
		ctx := context.Background()
		expectedErr := errors.New("db query error")
		dummy := &dummyExecQuerier{
			queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
				return nil, expectedErr
			},
		}

		var vr entsql.Rows
		err := entpgx.DoQueryInternal(ctx, dummy, "SELECT 1", []any{}, &vr)
		require.ErrorIs(t, err, expectedErr)
	})
}
