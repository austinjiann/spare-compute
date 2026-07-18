// Package sqlite implements ComputeHop's local durable metadata store.
//
// It owns schema migrations, SQLite connection policy, and infrastructure
// implementations of domain repository interfaces. Large logs, project chunks,
// workspaces, and artifacts remain files outside this database.
package sqlite
