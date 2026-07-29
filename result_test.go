package entpgx_test

import (
	"testing"

	"github.com/incroy/entpgx"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestResult_LastInsertId(t *testing.T) {
	res := entpgx.NewResult(pgconn.NewCommandTag("INSERT 0 1"))
	id, err := res.LastInsertId()
	require.Zero(t, id)
	require.ErrorContains(t, err, "entpgx: LastInsertId is not supported by postgres")
}

func TestResult_RowsAffected(t *testing.T) {
	tests := []struct {
		tag      string
		expected int64
	}{
		{"INSERT 0 1", 1},
		{"UPDATE 42", 42},
		{"DELETE 0", 0},
		{"SELECT 10", 10},
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			res := entpgx.NewResult(pgconn.NewCommandTag(tt.tag))
			affected, err := res.RowsAffected()
			require.NoError(t, err)
			require.Equal(t, tt.expected, affected)
		})
	}
}
