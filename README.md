# entpgx

[![Go Reference](https://pkg.go.dev/badge/github.com/incroy/entpgx.svg)](https://pkg.go.dev/github.com/incroy/entpgx)
[![Go Report Card](https://goreportcard.com/badge/github.com/incroy/entpgx)](https://goreportcard.com/report/github.com/incroy/entpgx)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

High-performance, production-ready [`entgo.io/ent`](https://entgo.io/) `dialect.Driver` implementation natively backed by [`jackc/pgx/v5`](https://github.com/jackc/pgx).

`entpgx` completely eliminates `database/sql` overhead. Connection pooling, query execution, row scanning, and transaction management stream directly through `pgxpool.Pool` and `pgx.Tx` using PostgreSQL's native wire protocol.

---

## Key Features

- **Zero `database/sql` Overhead**: Bypasses the Go standard library SQL wrapper. Executes queries and scans rows directly via `pgx/v5`.
- **Full `pgxpool.Pool` Control**: Customize TLS settings, tracers/logging, pool sizes (`MinConns`/`MaxConns`), health checks, and custom `pgtype` OID codecs.
- **Leak-Safe Session Variables / Row-Level Security (RLS)**: Safely pass dynamic session context (e.g. `app.tenant_id`) down to PostgreSQL queries. Automatically reset on pool release and scoped via `SET LOCAL` during transactions.
- **Native Transaction Features**: Supports nested transactions via PostgreSQL `SAVEPOINT`s and provides an escape hatch (`tx.Raw()`) to execute native `pgx` operations (such as `CopyFrom`, `LISTEN`/`NOTIFY`, and Large Objects) within ent transactions.
- **Seamless Ent Parity**: Full support for `ent` CRUD operations, error classification (`ent.IsNotFound`, `ent.IsConstraintError`), hooks, privacy rules, and third-party extensions like `ariga/entcache`.

---

## Installation

```bash
go get github.com/incroy/entpgx
```

---

## Quick Start

### 1. Basic Connection

```go
package main

import (
	"context"
	"log"

	"github.com/incroy/entpgx"
	"yourproject/ent" // generated ent client
)

func main() {
	ctx := context.Background()

	// Open driver with PostgreSQL connection string
	drv, err := entpgx.Open(ctx, "postgres://postgres:pass@localhost:5432/dbname?sslmode=disable")
	if err != nil {
		log.Fatalf("failed opening connection: %v", err)
	}
	defer drv.Close()

	// Wrap as ent client
	client := ent.NewClient(ent.Driver(drv))
	defer client.Close()

	// Use client normally with ent
	users, err := client.User.Query().All(ctx)
	if err != nil {
		log.Fatalf("failed querying users: %v", err)
	}
	log.Printf("found %d users", len(users))
}
```

---

### 2. Custom `pgxpool.Pool` Configuration

If you need advanced pool features (e.g., OpenTelemetry tracing, custom connection limits, TLS, custom type codecs), parse the config manually and pass the pool to `entpgx.NewDriver`:

```go
package main

import (
	"context"
	"log"

	"github.com/incroy/entpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"yourproject/ent"
)

func main() {
	ctx := context.Background()

	cfg, err := pgxpool.ParseConfig("postgres://postgres:pass@localhost:5432/dbname")
	if err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}

	// Customize pgxpool configuration
	cfg.MinConns = 5
	cfg.MaxConns = 50

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	drv := entpgx.NewDriver(pool)
	client := ent.NewClient(ent.Driver(drv))
	defer client.Close()
}
```

---

## Advanced Usage

### 1. Row-Level Security (RLS) & Session Variables

Attach custom session variables (GUCs) to the context using `entpgx.WithVar` or `entpgx.WithIntVar`. These can be read by PostgreSQL RLS policies via `current_setting('app.tenant_id')`:

```go
// Attach tenant ID to context
ctx = entpgx.WithVar(ctx, "app.tenant_id", "tenant_12345")

// Every query executed with this context will automatically set "app.tenant_id"
users, err := client.User.Query().All(ctx)
```

> [!NOTE]
> - **Pool Queries**: Automatically pins a connection for the query execution, runs `set_config($1, $2, false)`, and issues a `RESET` before releasing the connection back to the pool.
> - **Transactions**: Uses PostgreSQL `SET LOCAL` (`set_config($1, $2, true)`). PostgreSQL automatically reverts `SET LOCAL` at `COMMIT` or `ROLLBACK`, eliminating cross-request variable leaks.

---

### 2. Native `pgx` Escape Hatch (`tx.Raw()`)

Access the underlying `pgx.Tx` inside an ent transaction to perform high-performance bulk operations (`CopyFrom`), `LISTEN`/`NOTIFY`, or Large Object operations within the exact same transaction boundaries:

```go
err := entpgx.RunInTx(ctx, drv, func(ctx context.Context, tx *entpgx.Tx) error {
	// 1. Perform ent CRUD operations
	user, err := client.User.Create().SetName("Alice").Save(ctx)
	if err != nil {
		return err
	}

	// 2. Access raw pgx.Tx for bulk CopyFrom insertion
	rawTx := tx.Raw()
	_, err = rawTx.CopyFrom(
		ctx,
		pgx.Identifier{"audit_logs"},
		[]string{"user_id", "action"},
		pgx.CopyFromRows([][]any{
			{user.ID, "user_created"},
		}),
	)
	return err
})
```

---

### 3. Capturing `Tx` from `ent.Client`

If your code initiates a transaction using `client.Tx(ctx)`, you can capture the underlying `*entpgx.Tx` handle using `WithTxCapture`:

```go
box := &entpgx.TxBox{}
ctx = entpgx.WithTxCapture(ctx, box)

txClient, err := client.Tx(ctx)
if err != nil {
	return err
}

// box.Tx now holds the *entpgx.Tx handle
rawPgxTx := box.Tx.Raw()
```

---

## Known Caveats & Considerations

### 1. Ent Auto-Migration (`client.Schema.Create`)

Ent's built-in auto-migration tool (`client.Schema.Create`) relies on an internal unchecked type assertion in `entgo.io/ent/dialect/sql/schema/atlas.go` that expects a concrete `*database/sql.Rows`. Because `entpgx` does not construct `database/sql.Rows`, running `client.Schema.Create` will cause a panic.

**Recommended Solutions**:
1. **Migration CLI (Recommended for Production)**: Run schema migrations using standard migration tools such as the **Atlas CLI**, **golang-migrate**, or **goose** against your database DSN.
2. **Migration-Only Client**: If you prefer using `client.Schema.Create()`, initialize a secondary migration client backed by `database/sql` via `pgx/v5/stdlib`:
   ```go
   import (
       "entgo.io/ent/dialect/sql"
       _ "github.com/jackc/pgx/v5/stdlib"
   )

   // Open migration-only client
   db, _ := sql.Open("pgx", dsn)
   migrationClient := ent.NewClient(ent.Driver(db))
   _ = migrationClient.Schema.Create(ctx)
   ```

---

### 2. `ColumnTypes()`

`*sql.ColumnType` does not expose an exported constructor outside Go's `database/sql` package. As a result, `ColumnTypes()` returns an unsupported error on `entpgx`. 

This does **not** affect standard ent CRUD operations or entity queries. Standard generated ent entities and `GroupBy(...).Aggregate(...)` queries function normally.

---

## Running Tests

### Unit & Mock Tests
```bash
go test -v ./...
```

### Integration Tests (Live Postgres)
Set `ENTPGX_TEST_DSN` (or `POSTGRES_DSN` / `DATABASE_URL`) to run full integration tests against a PostgreSQL instance:

```bash
ENTPGX_TEST_DSN="postgres://postgres:postgres@localhost:5432/testdb?sslmode=disable" go test -v ./...
```

---

## License

`entpgx` is licensed under the [Apache License 2.0](LICENSE).