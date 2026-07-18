# ComputeHop

ComputeHop turns computers owned by one person into a pool for background
compute jobs. The project is currently in its architecture and scaffolding
stage.

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
implemented by `internal/infra/sqlite/`. The worker application service can
durably accept and manage queued jobs, and `computehopd` can initialize that
state and run as the background process.

The daemon does not expose IPC or execute commands yet. Those are the next
parts of the first working vertical slice.

To verify daemon state initialization during development:

```bash
computehop_state_dir="$(mktemp -d)"
go run ./cmd/computehopd --check --state-dir "$computehop_state_dir"
```
