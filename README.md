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

The first implemented contract is the generic job model under `internal/job/`.
The first working vertical slice will be durable local execution through the
daemon and CLI.
