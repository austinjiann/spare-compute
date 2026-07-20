# ComputeHop

ComputeHop turns computers owned by one person into a pool for background
compute jobs. The current local slice accepts durable jobs through the CLI,
executes native commands under process-tree supervision, captures reconnectable
stdout and stderr, persists terminal results, and discovers nearby ComputeHop
daemons with mDNS. Nearby workers can now be paired through a mutually
authenticated QUIC handshake with matching confirmation codes and durable,
revocable public-key pins.

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
`computehopd` through a user-owned Unix-domain socket on macOS/Linux or a
user-scoped named pipe on Windows. Requests use a versioned Protocol Buffer
contract and an owner-only random capability token. The daemon validates each
request and owns all SQLite access. ComputeHop creates its state directory with
owner-only permissions and rejects unsafe custom directories.

For now, jobs run on the orchestrator Mac in the submitted working directory.
Project snapshots, isolated per-job workspaces, artifact return, and remote
execution are later slices. Pairing establishes identity and trust but does not
yet authorize a remote job operation. Discovery records never authorize
commands. Do not modify or remove a submitted working directory while its job
is running.

To exercise the local control plane during development, start the daemon:

```bash
computehop_state_dir="$(mktemp -d)"
go run ./cmd/computehopd --state-dir "$computehop_state_dir" --role orchestrator
```

Then use the same state directory in another terminal:

```bash
go run ./cmd/computehop --state-dir "$computehop_state_dir" status
go run ./cmd/computehop --state-dir "$computehop_state_dir" devices
go run ./cmd/computehop --state-dir "$computehop_state_dir" pair <device-name-or-session>
go run ./cmd/computehop --state-dir "$computehop_state_dir" pair
go run ./cmd/computehop --state-dir "$computehop_state_dir" pair confirm <pairing-id>
go run ./cmd/computehop --state-dir "$computehop_state_dir" unpair <device-name-or-id>
go run ./cmd/computehop --state-dir "$computehop_state_dir" run -- echo hello
go run ./cmd/computehop --state-dir "$computehop_state_dir" jobs
go run ./cmd/computehop --state-dir "$computehop_state_dir" logs --follow <job-id>
go run ./cmd/computehop --state-dir "$computehop_state_dir" cancel <job-id>
```

The orchestrator launches a detached runner for each job. The runner owns the
native process tree, durable log writer, cancellation acknowledgement, and final
state transition. Consequently, closing the submitting CLI—or restarting the
orchestrator daemon—does not abandon an accepted process.

Each daemon also creates an owner-only, persistent Ed25519 installation
identity and advertises `_computehop._udp` on the LAN. Advertisements contain a
random per-session presence ID, device name, role, protocol version, and routing
hints only—the durable identity fingerprint is never placed in mDNS.
Observations remain in memory, expire when they stop being refreshed, and are
untrusted until an explicit pairing reveals and pins the public key.

macOS defaults to the `orchestrator` role; Linux and Windows default to
`worker`. Use `--role worker` when running a worker daemon on another Mac. A
pairing starts from the orchestrator with `computehop pair <device>`. Both local
CLIs then show the same connection-bound verification code. Compare it exactly
and run `computehop pair confirm <pairing-id>` on both machines. A worker stores
at most one active orchestrator pin, while the orchestrator may store multiple
workers. `computehop unpair` durably revokes the selected local pin; pairing
again is explicit.
