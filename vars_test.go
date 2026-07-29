package entpgx_test

import (
	"context"
	"testing"

	"github.com/incroy/entpgx"
	"github.com/stretchr/testify/require"
)

func TestWithVar(t *testing.T) {
	ctx := context.Background()

	// Initial context should not contain "foo"
	val, ok := entpgx.VarFromContext(ctx, "foo")
	require.False(t, ok)
	require.Empty(t, val)

	// Add variable "foo" = "bar"
	ctx1 := entpgx.WithVar(ctx, "foo", "bar")
	val, ok = entpgx.VarFromContext(ctx1, "foo")
	require.True(t, ok)
	require.Equal(t, "bar", val)

	// Original context remains unchanged
	val, ok = entpgx.VarFromContext(ctx, "foo")
	require.False(t, ok)
	require.Empty(t, val)

	// Add second variable "baz" = "qux" on top of ctx1
	ctx2 := entpgx.WithVar(ctx1, "baz", "qux")
	val, ok = entpgx.VarFromContext(ctx2, "foo")
	require.True(t, ok)
	require.Equal(t, "bar", val)

	val, ok = entpgx.VarFromContext(ctx2, "baz")
	require.True(t, ok)
	require.Equal(t, "qux", val)
}

func TestWithIntVar(t *testing.T) {
	ctx := context.Background()
	ctx = entpgx.WithIntVar(ctx, "app.user_id", 42)

	val, ok := entpgx.VarFromContext(ctx, "app.user_id")
	require.True(t, ok)
	require.Equal(t, "42", val)
}

func TestVarFromContext_NotFound(t *testing.T) {
	ctx := context.Background()
	ctx = entpgx.WithVar(ctx, "key1", "val1")

	val, ok := entpgx.VarFromContext(ctx, "non_existent_key")
	require.False(t, ok)
	require.Empty(t, val)
}
