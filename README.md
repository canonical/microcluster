# Microcluster

## Contents

- [Introduction](#introduction)
- [Example package and tutorial](#example-package-and-tutorial)
- [Import Microcluster](#import-microcluster)
- [Configure and start the Microcluster service](#configure-and-start-the-microcluster-service)
- [Lifecycle actions (hooks)](#lifecycle-actions-hooks)
- [Query the Dqlite database directly](#query-the-dqlite-database-directly)
- [Create your own API endpoints](#create-your-own-api-endpoints)
- [Create your own schema extensions](#create-your-own-schema-extensions)
- [Additional developer documentation](#additional-developer-documentation)

## Introduction

Microcluster is a Go library that provides an easy-to-use framework for creating and managing highly available [Dqlite](https://github.com/canonical/dqlite) clusters (using [go-dqlite](https://github.com/canonical/go-dqlite)). It offers an extensible API and database, which can be used to directly connect to your underlying services.

Build your service with Microcluster-managed HTTPS endpoints, API extensions, schema updates, lifecycle actions, and additional HTTPS listeners.

## Example package and tutorial

The [example package](example) in this repository, which includes a [tutorial](example/README.md#tutorial), provides a practical example of a Microcluster project that you can use as a starting point for your own project.

## Import Microcluster

To get the latest LTS release of Microcluster, run:

```
go get github.com/canonical/microcluster/v3@latest
```

## Configure and start the Microcluster service

All of Microcluster's state is stored in the `StateDir` directory. Learn more about the state directory in [doc/state.md](doc/state.md).

To define a new Microcluster app:

```go
m, err := microcluster.App(microcluster.Args{StateDir: "/path/to/state"})
if err != nil {
    // ...
}
```

Define application services for Microcluster in `DaemonArgs`:

```go
dargs := microcluster.DaemonArgs{
    Version: "0.0.1",

    ExtensionsSchema: []schema.Update{
        // Sequential list of modifications to the database.
    },

    APIExtensions: []string{
        // Sequential list of API capabilities.
    },

    Servers: map[string]rest.Server{
        "my-server": {
            CoreAPI: true,
            ServeUnix: true,
            Resources: []rest.Resources{
                {
                    PathPrefix: "1.0",
                    Endpoints: []rest.Endpoint{
                        // Additional API endpoints to offer on "/1.0".
                    },
                },
            }
        }
    }
}
```

Learn more about setting up additional servers in [doc/api.md](doc/api.md).

Start the daemon with your configuration:

```go
err := m.Start(ctx, dargs)
if err != nil {
    // ...
}
```

### Lifecycle actions (hooks)

The complete set of Microcluster hooks and their behaviors are defined [in this Go file](https://github.com/canonical/microcluster/blob/v3/microcluster/types/hooks.go).

Example using the `OnStart` hook:

```go
dargs.Hooks = &types.Hooks{
    OnStart: func(ctx context.Context, s types.State) error {
        // Code to execute on each startup.
    }
}
```

### Query the Dqlite database directly

```go
_, batch, err := m.Sql(ctx, "SELECT name, address FROM core_cluster_members WHERE role='voter'")
if err != nil {
    // ...
}

for i, result := range batch.Results {
    fmt.Printf("Query %d:\n", i)

    fmt.Println("Type:", result.Type)
    fmt.Println("Columns:", result.Columns)
    fmt.Println("RowsAffected:", result.RowsAffected)
    fmt.Println("Rows:", result.Rows)
}
```

Learn more about the Dqlite database in [doc/database.md](doc/database.md).

### Signal Handling

MicroCluster provides built-in signal handling for graceful daemon shutdown. By default, MicroCluster handles `SIGPWR`, `SIGTERM`, `SIGINT`, and `SIGQUIT` signals. You can customize this behavior by setting `ShutdownSignals` in your `DaemonArgs`:

```go
import "golang.org/x/sys/unix"

// Use default signals (SIGPWR, SIGTERM, SIGINT, SIGQUIT)
dargs := microcluster.DaemonArgs{
    // ShutdownSignals not set (nil), defaults will be used.
}

// Or customize the signals:
dargs := microcluster.DaemonArgs{
    ShutdownSignals: []os.Signal{unix.SIGTERM, unix.SIGINT, unix.SIGQUIT}, // Omit SIGPWR.
    // other configuration...
}

// Disable signal handling entirely:
dargs := microcluster.DaemonArgs{
    ShutdownSignals: []os.Signal{}, // Empty slice, no signal handling.
}
```

For simpler cases, you can handle only specific signals:

```go
// Shutdown only on SIGINT (Ctrl+C)
dargs := microcluster.DaemonArgs{
    ShutdownSignals: []os.Signal{unix.SIGINT},
}
```

When `ShutdownSignals` is not set (nil), MicroCluster uses default signals for graceful shutdown. When set to an empty slice, no signal handling is performed.


### Create your own API endpoints

```go
endpoint := types.Endpoint{
    Path: "mypath" // API is served over /mypath

    Post: types.EndpointAction{Handler: myHandler} // POST action for the endpoint.
}

func myHandler(s types.State, r *http.Request) types.Response {
    msg := fmt.Sprintf("This is a response from %q at %q", s.Name(), s.Address())

    return types.SyncResponse(true, msg)
}

// Include your endpoints in DaemonArgs like so to serve over /1.0 over the default listener.
dargs := microcluster.DaemonArgs{
    ExtensionServers: map[string]types.Server{
        "my-server": {
            CoreAPI: true,
            ServeUnix: true,
            Resources: []types.Resources{
                {
                    PathPrefix: "1.0",
                    Endpoints: []types.Endpoint{endpoint},
                },
            }
        }
    }
}
```

Learn more about the API in [doc/api.md](doc/api.md).

### Create your own schema extensions

```go
var schemaUpdate1 schema.Update = func(ctx context.Context, tx *sql.Tx) error {
    _, err := tx.ExecContext(ctx, "CREATE TABLE services (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT);")

    return err
}

// Include your schema extensions in DaemonArgs.
dargs := microcluster.DaemonArgs{
    ExtensionsSchema: []schema.Update{
        schemaUpdate1,
    }
}
```

Learn more about schema updates in [doc/upgrades.md](doc/upgrades.md).

### Additional developer documentation

View the [doc](doc) directory for more information.

You can also view the [Godoc-generated reference documentation](https://pkg.go.dev/github.com/canonical/microcluster/v3), which is generated from docstrings within the Microcluster code.
