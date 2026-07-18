# ComputeHop

ComputeHop turns computers owned by one person into a pool for background
compute jobs. The current local control-plane slice accepts durable jobs through
the CLI and daemon; native command execution is the next implementation step.

See [`docs/PLAN.md`](docs/PLAN.md) for the product, architecture, security,
execution, deployment, and launch plan.

## Repository layout

- `cmd/` contains the three Go executable entry points.
- `api/` owns the versioned Protocol Buffer contracts.
- `gen/` receives generated Go and Swift protocol code.
- `internal/` contains all private Go application and infrastructure packages.
- `apps/macos/` contains the presentation-only SwiftUI menu-bar application.
- `test/` contains tests that cross package or process boundaries.
- `packaging/` contains native installer definitions.
- `deploy/` contains hosted connectivity deployment configuration.

The generic job model lives under `internal/job/`, with local durable metadata
implemented by `internal/infra/sqlite/`. The `computehop` CLI talks only to
`computehopd` through a user-owned Unix-domain socket. Requests use a versioned
Protocol Buffer contract and an owner-only random capability token. The daemon
validates each request and owns all SQLite access. ComputeHop creates its state
directory with owner-only permissions and rejects unsafe custom directories.

The daemon does not execute submitted commands yet. Jobs remain queued until the
native executor lands in the next slice.

To exercise the local control plane during development, start the daemon:

```bash
computehop_state_dir="$(mktemp -d)"
go run ./cmd/computehopd --state-dir "$computehop_state_dir"
```

Then use the same state directory in another terminal:

```bash
go run ./cmd/computehop --state-dir "$computehop_state_dir" status
go run ./cmd/computehop --state-dir "$computehop_state_dir" run -- echo hello
go run ./cmd/computehop --state-dir "$computehop_state_dir" jobs
```
