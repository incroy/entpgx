package entpgx_test

import (
	"errors"
	"testing"

	"github.com/incroy/entpgx"
	"github.com/stretchr/testify/require"
)

func TestRowsAdapter_ColumnTypes(t *testing.T) {
	adapter := entpgx.NewRowsAdapter(&dummyRows{}, nil)
	ct, err := adapter.ColumnTypes()
	require.Nil(t, ct)
	require.ErrorContains(t, err, "entpgx: ColumnTypes is not supported")
}

func TestRowsAdapter_NextResultSet(t *testing.T) {
	adapter := entpgx.NewRowsAdapter(&dummyRows{}, nil)
	require.False(t, adapter.NextResultSet())
}

func TestRowsAdapter_CloseAndCleanup(t *testing.T) {
	t.Run("cleanup executed on close", func(t *testing.T) {
		cleanupCalled := false
		mockRows := &dummyRows{}
		adapter := entpgx.NewRowsAdapter(mockRows, func() error {
			cleanupCalled = true
			return nil
		})

		err := adapter.Close()
		require.NoError(t, err)
		require.True(t, mockRows.closed)
		require.True(t, cleanupCalled)
	})

	t.Run("cleanup error returned on close", func(t *testing.T) {
		cleanupErr := errors.New("reset variable error")
		mockRows := &dummyRows{}
		adapter := entpgx.NewRowsAdapter(mockRows, func() error {
			return cleanupErr
		})

		err := adapter.Close()
		require.ErrorIs(t, err, cleanupErr)
		require.True(t, mockRows.closed)
	})
}

func TestRowsAdapter_ErrAndColumns(t *testing.T) {
	expectedErr := errors.New("rows iteration error")
	mockRows := &dummyRows{
		cols: []string{"id", "title"},
		err:  expectedErr,
	}
	adapter := entpgx.NewRowsAdapter(mockRows, nil)

	require.ErrorIs(t, adapter.Err(), expectedErr)

	cols, err := adapter.Columns()
	require.NoError(t, err)
	require.Equal(t, []string{"id", "title"}, cols)
}

func TestRowsAdapter_NextAndScan(t *testing.T) {
	mockRows := &dummyRows{
		cols: []string{"val"},
		vals: [][]any{{"hello"}},
	}
	adapter := entpgx.NewRowsAdapter(mockRows, nil)

	require.True(t, adapter.Next())

	var val string
	require.NoError(t, adapter.Scan(&val))
	require.Equal(t, "hello", val)

	require.False(t, adapter.Next())
}
