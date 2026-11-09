# Monitor Go SDK

A small Go library for writing application logs to the SQLite buffer managed by
[monitor-remote](../monitor-remote/README.md). Remote handles collection, central
delivery, retries, and cleanup. The SDK needs no central URL or API key.

Module: `github.com/rmg-unicage/monitor-go-sdk` · Package: `monitorsdk`

## Setup

1. Use **Go 1.25+**, a **C compiler**, and **`CGO_ENABLED=1`**.
2. Initialize the SQLite file through remote before connecting your application.
3. Give the application access to that same persistent file. Applications and
   remote normally run under the same OS user.

The repository's `go.work` and hello-world module already use the local SDK.
For another application with an existing `go.mod` beside `monitor-go-sdk/`, add:

```sh
go mod edit -require=github.com/rmg-unicage/monitor-go-sdk@v0.0.0
go mod edit -replace=github.com/rmg-unicage/monitor-go-sdk=../monitor-go-sdk
```

Adjust the replacement path to your checkout. After adding the import below,
run `go mod tidy` in your application.

## Usage

```go
package main

import (
    "context"
    "log"
    "os"
    "time"

    monitorsdk "github.com/rmg-unicage/monitor-go-sdk"
)

func main() {
    db, err := monitorsdk.ConnectDB(os.Getenv("MONITOR_DB_PATH"))
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    id, err := monitorsdk.LogError(ctx, db,
        "web-api", "production", "ERROR", "Database timeout")
    if err != nil {
        log.Print(err)
        return
    }
    log.Printf("Queued record %d", id)
}
```

Run your application with the buffer path, for example:

```sh
MONITOR_DB_PATH=/absolute/path/to/remote-monitor.db CGO_ENABLED=1 go run .
```

`MONITOR_DB_PATH` is read by this example; the SDK itself takes the path passed
to `ConnectDB`. Reuse the database handle and close it when the application exits.

## API

- **`ConnectDB(path)`** opens an existing prepared buffer and returns `*sql.DB`.
- **`LogError(ctx, db, app, environment, level, message, details...)`** atomically
  writes the log and its pending-delivery row, returning the numeric source ID.
- **`ErrorDetails`** optionally supplies `StackTrace`, JSON `Metadata`, and the
  original `CreatedAt` timestamp.

Supported levels: **INFO, WARN, ERROR, CRITICAL, PANIC**.
A successful call means accepted locally; central delivery happens later.
Records can still be written while remote is stopped, once the file is prepared.

The SDK never creates schemas or migrates buffers. Preserve the shared database
across restarts. For a runnable integration example, see
[monitor-hello-world](../monitor-hello-world/README.md).
