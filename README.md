# ComputeHop

ComputeHop turns computers owned by one person into a pool for background
compute jobs. The current local slice accepts durable jobs through the CLI,
executes native commands under process-tree supervision, captures reconnectable
stdout and stderr, persists terminal results, and discovers nearby ComputeHop
daemons with mDNS. Nearby workers can now be paired through a mutually
authenticated QUIC handshake with matching confirmation codes and durable,
revocable public-key pins. An orchestrator can explicitly submit, inspect,
follow, and cancel a durable job on a paired worker that is currently reachable
on the same LAN. It also remembers which pinned worker accepted each remote job
so later job-specific operations can route by job ID alone. Newly paired
devices also derive private connectivity material, and the standalone hosted
service and bounded HTTPS client can exchange short-lived presence and
signaling payloads encrypted end to end with pair-scoped, route-bound keys.
A buildable SwiftUI menu-bar client now talks to the daemon over authenticated
local IPC and presents device, pairing, native job submission, reconnectable
output, and cancellation controls. A provider-neutral
one-VPS stack packages rendezvous, automatic HTTPS, STUN, and authenticated
TURN. A bounded Pion ICE path layer now gathers and selects direct or relayed
UDP candidates and exchanges versioned descriptions through encrypted
rendezvous presence. Daemons can now supervise active pair records and run the
existing identity-pinned QUIC control protocol over a selected path; job
routing prefers LAN and falls back to a ready direct or relayed ICE path. Remote jobs now
snapshot the local project, transfer only content chunks missing from the
worker, and execute from a fresh worker-owned workspace. Repeating an unchanged
job reuses the verified worker cache. Jobs can declare output files or folders;
the worker collects them before success, and the orchestrator downloads only
missing hash-verified chunks into a conflict-safe local results directory.
The verified content cache is persistent, SQLite-indexed, LRU-evicted, and
bounded to 20GiB by default. Active transfers, running jobs, and output chunks
that have not yet been successfully restored and acknowledged are protected
from eviction. Output download and restore progress is stored durably and shown
through job summaries in the CLI and menu-bar app. Operator-provisioned
short-lived TURN credentials are available for single-owner forced-relay
testing. Automated local end-to-end coverage passes, while physical
unrelated-network validation is still open. Public TURN relay traffic remains
gated on entitlement-backed, quota-limited credential issuance.

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

Jobs run on the orchestrator unless `--on` explicitly selects a paired worker.
For a remote job, `-C`/`--working-directory` names a project directory on the
orchestrator Mac—not a path that must already exist on the worker. It defaults
to the CLI's current directory. ComputeHop finds the enclosing Git worktree or
nearest recognized project marker, creates an immutable snapshot, transfers
only missing content-defined chunks, and reconstructs it under a private
per-job worker directory. `.gitignore` and `.computehopignore` rules are
applied; `.git`, `.computehop-results`, symlinks, sockets, devices, traversal,
and non-portable paths cannot enter a snapshot. Declared outputs use the same
verified content store and are restored without overwriting existing files or
following destination symlinks. Chunk transfers negotiate bounded zstd or
identity encoding while keeping hashes defined over decoded content. The
verified content cache defaults to 20GiB and can be changed with
`computehopd --cache-size 40GiB` or the macOS installer `--cache-size` flag.
Automatic placement and fully validated network-change reconnection are later
slices. Discovery records never authorize commands: the
live QUIC certificate must match the selected active public-key pin.

Pairings created before connectivity-secret support remain valid for LAN use
but cannot derive hosted rendezvous credentials. Disconnect and explicitly
connect those devices again before remote-connectivity testing.

To exercise the local control plane during development, start the daemon:

```bash
computehop_state_dir="$(mktemp -d)"
go run ./cmd/computehopd --state-dir "$computehop_state_dir" --role orchestrator
```

Then use the same state directory in another terminal:

```bash
go run ./cmd/computehop --state-dir "$computehop_state_dir" status
go run ./cmd/computehop --state-dir "$computehop_state_dir" setup
go run ./cmd/computehop --state-dir "$computehop_state_dir" setup orchestrator
go run ./cmd/computehop --state-dir "$computehop_state_dir" setup worker --device-name "Gaming PC"
go run ./cmd/computehop --state-dir "$computehop_state_dir" setup mac
go run ./cmd/computehop --state-dir "$computehop_state_dir" setup mac --role worker --device-name "Gaming PC"
go run ./cmd/computehop --state-dir "$computehop_state_dir" setup mac --role worker --device-name "Gaming PC" --lan-only
go run ./cmd/computehop --state-dir "$computehop_state_dir" setup mac --role orchestrator --connectivity-domain connect.example.com --turn-domain turn.example.com
go run ./cmd/computehop --state-dir "$computehop_state_dir" setup worker --device-name "Gaming PC" --connectivity-domain connect.example.com --turn-domain turn.example.com --turn-server 'turn:turn.example.com:3478?transport=udp' --turn-username 1800000000:computehop --turn-password secret
go run ./cmd/computehop --state-dir "$computehop_state_dir" setup vps
go run ./cmd/computehop --state-dir "$computehop_state_dir" setup vps --connectivity-domain connect.example.com --turn-domain turn.example.com --email admin@example.com --public-ip 203.0.113.10
go run ./cmd/computehop --state-dir "$computehop_state_dir" doctor
go run ./cmd/computehop --state-dir "$computehop_state_dir" devices
go run ./cmd/computehop --state-dir "$computehop_state_dir" connect
go run ./cmd/computehop --state-dir "$computehop_state_dir" connect nearby
go run ./cmd/computehop --state-dir "$computehop_state_dir" connect <device-name-or-session>
go run ./cmd/computehop --state-dir "$computehop_state_dir" connect confirm
go run ./cmd/computehop --state-dir "$computehop_state_dir" disconnect <device-name-or-id>
go run ./cmd/computehop --state-dir "$computehop_state_dir" run echo hello
go run ./cmd/computehop --state-dir "$computehop_state_dir" jobs
go run ./cmd/computehop --state-dir "$computehop_state_dir" logs --follow <job-id>
go run ./cmd/computehop --state-dir "$computehop_state_dir" cancel <job-id>
go run ./cmd/computehop --state-dir "$computehop_state_dir" run -o result.txt sh -c 'printf done > result.txt'
go run ./cmd/computehop --state-dir "$computehop_state_dir" run -o result.txt --follow --get sh -c 'printf done > result.txt'
go run ./cmd/computehop --state-dir "$computehop_state_dir" outputs <job-id>
```

`computehop status` and `computehop doctor` also print the local daemon's
device name, role, and short device ID when available. `setup` prints the
first-run local, connection, smoke-test, and one-VPS commands without requiring
the daemon to be running; `setup orchestrator` and `setup worker` are friendly
role shortcuts for exact macOS install commands, while `setup mac` exposes the
same role selection as a flag for scripting; these guides include optional cache
sizing, explicit LAN-only installs, optional VPS endpoint flags, and short-lived
TURN relay credentials printed by the VPS. `setup vps` expands the hosted side into a concrete buy,
DNS, firewall, bootstrap, install, TURN credential, and smoke-test checklist for
the one-VPS stack.
Pass `--connectivity-domain`, `--turn-domain`, `--email`, and `--public-ip` to
print the checklist with your actual VPS values instead of the example values.
The first-run and doctor guidance prefers the packaged macOS worker installer
and labels raw `go run` daemon commands as development-only. `doctor` is the quickest manual smoke-check: it is
safe to run before the daemon is up, points at `computehop setup orchestrator`
when ComputeHop is not running, and otherwise verifies daemon reachability, LAN
discovery, paired-device counts, reachable workers, and nearby unpaired devices
before printing the next command to run for the current state. When no worker is
visible yet, that next command is `computehop setup worker --device-name
"Gaming PC"`. `devices` merges
trusted peers with matching LAN presence and collapses duplicate same-name LAN
records for a single connected peer so stale daemon restarts do not look like
extra unpaired computers. `connect` is the
friendlier pairing entry point: run it with no arguments for the next connection
step, `connect nearby` to start trust setup only when exactly one nearby
unpaired worker is visible, `connect <device>` when you need to choose
explicitly, and `connect confirm` on both devices after the verification code
matches. `connect auto` remains a compatibility alias for `connect nearby`.
If a second daemon is started while the first one is still using the local
socket or ComputeHop network port, `computehopd` now reports that another daemon
appears to be running and points the user to `computehop status` instead of
printing only a raw bind error.

After connecting a currently nearby worker, use `--on auto` when there is one
active worker. Use an explicit name or device ID when you have more than one
worker. If automatic selection cannot choose safely, the CLI tells you whether
to run `computehop connect nearby` for setup or `computehop devices` to pick an
explicit worker. For the cheap “does remote execution work?” check, run
`computehop smoke`; it submits `hostname` to the selected worker without
uploading a project and follows the result:

```bash
go run ./cmd/computehop --state-dir "$computehop_state_dir" smoke
go run ./cmd/computehop --state-dir "$computehop_state_dir" run --on auto echo hello
go run ./cmd/computehop --state-dir "$computehop_state_dir" run --on auto --no-project hostname
go run ./cmd/computehop --state-dir "$computehop_state_dir" run --on auto cargo build --release
go run ./cmd/computehop --state-dir "$computehop_state_dir" run --on "Gaming PC" -C /local/project cargo test
go run ./cmd/computehop --state-dir "$computehop_state_dir" run --on "Gaming PC" -o target/release/my-app cargo build --release
go run ./cmd/computehop --state-dir "$computehop_state_dir" run --on "Gaming PC" -o target/release/my-app --follow --get cargo build --release
go run ./cmd/computehop --state-dir "$computehop_state_dir" jobs --on auto
go run ./cmd/computehop --state-dir "$computehop_state_dir" logs --follow <job-id>
go run ./cmd/computehop --state-dir "$computehop_state_dir" cancel <job-id>
go run ./cmd/computehop --state-dir "$computehop_state_dir" outputs <job-id>
```

Repeat `-o`/`--output` for each relative file or directory to return. Add
`--follow` to stream logs from `run`, `--wait` to block until completion without
streaming logs, and `--get`/`--fetch` to download declared outputs after a
successful job. `--get` implies waiting and restores to the submitted working
directory by default; use `--to <directory>` to choose another destination. Use
`--no-project` for remote utility commands that do not need local files or
declared outputs, so the worker runs the command without a project snapshot
upload. Remote project runs print a pre-submit preparation message before the
blocking snapshot/upload step so the CLI does not look idle. You
can still fetch later with `computehop outputs <job-id>` (`artifacts`, `fetch`,
and `download` remain aliases), which infers its worker and restores to
`.computehop-results/<job-id>` by default. Existing files are never overwritten.
Incoming conflicts are retained beneath `.computehop-conflicts` in the destination.
While outputs are being fetched or restored, `computehop jobs`, remote job
refreshes, and the menu-bar job list show durable byte-level progress for the
current download or restore phase.

Remote job state, logs, and execution history remain authoritative on the
selected worker. The orchestrator stores only a durable mapping from each
submitted remote job ID to the pinned worker identity. That mapping lets
`logs` and `cancel` reconnect without `--on`, including after an orchestrator
daemon restart. `--on` remains an explicit override and is still required to
browse a worker's complete history with `jobs`; job lists are not aggregated
yet. `--on auto` is a first scheduler step: it selects the only active paired
worker, or asks you to choose when there are none or multiple. The older
`--device` spelling remains a hidden compatibility alias. Jobs
submitted by an older build have no placement record and still require an
explicit selector.

The orchestrator launches a detached runner for each job. The runner owns the
native process tree, durable log writer, cancellation acknowledgement, and final
state transition. Consequently, closing the submitting CLI—or restarting the
orchestrator daemon—does not abandon an accepted process.

Each daemon also creates an owner-only, persistent Ed25519 installation
identity and advertises `_computehop._udp` on the LAN. Advertisements contain a
random per-session presence ID, device name, role, protocol version, and routing
hints only—the durable identity fingerprint is never placed in mDNS.
Observations remain in memory, expire when they stop being refreshed, and are
untrusted until an explicit connection setup reveals and pins the public key.

macOS defaults to the `orchestrator` role; Linux and Windows default to
`worker`. Use `--role worker` when running a worker daemon on another Mac. A
connection starts from the orchestrator with `computehop connect nearby` when one
nearby unpaired worker is visible, or `computehop connect <device>` when you
need to choose among multiple devices. Both local CLIs then show the same
connection-bound verification code. Compare it exactly and run
`computehop connect confirm` on both machines; the CLI infers
the request when only one is actionable, tells you when the other machine still
needs confirmation, and asks for an ID only if there is ambiguity. A worker
stores at most one connected orchestrator pin, while the
orchestrator may store multiple workers. `computehop disconnect` durably revokes the
selected local pin; connecting again is explicit. `computehop unpair` remains a
compatibility alias. Remote job connections use a
separate protocol on the same QUIC listener, pin both endpoint identities, and
re-check worker-side trust for every operation so revocation takes effect
without waiting for a restart. The older `pair` command remains callable for
compatibility but is hidden from normal help; new flows should use `connect`.

The full macOS-to-macOS path has been physically exercised: discovery,
two-sided pairing, remote hostname execution, reconnectable logs after both
daemon restarts, and remote process-tree cancellation all passed. Windows and
Linux physical-worker validation remain open.

To launch the development menu-bar app against the default daemon state:

```bash
swift run ComputeHop
```

The app can connect nearby workers, show when a paired worker is offline because
remote connectivity is LAN-only, disconnect paired devices by revoking the local
trust pin, copy normal, LAN-only, or VPS-ready worker setup commands, submit a
native command to this Mac, the single active worker through Auto worker, or a
paired available worker, skip project upload for remote utility commands, choose
the local project folder to snapshot, declare comma-separated output paths,
restore completed outputs through a native folder picker, and read durable logs
directly in the menu. Quotes only group literal arguments; the app does not silently invoke a
shell. The app and generated
Swift protocol models build with `swift build` and test with `swift test`. See
[`apps/macos/README.md`](apps/macos/README.md) for the current packaging
boundary. See [`deploy/vps/README.md`](deploy/vps/README.md) for the one-VPS
staging setup to use after purchasing a host. After DNS is pointed at the VPS,
`deploy/vps/init.sh` writes the local `.env` file and generates the server-only
TURN shared secret from the chosen domains, operations email, and public IPv4.
`deploy/vps/turn-credentials.sh` keeps that shared secret on the VPS and prints
short-lived TURN username/password setup-helper and direct installer commands
for single-owner forced-relay testing.

To build a real local macOS app bundle containing the menu app, CLI, and daemon:

```bash
make macos-package
open dist/macos/ComputeHop.app
```

After stopping any daemon started manually with `go run`, `make install-macos`
installs the orchestrator bundle for the current user and configures the daemon
to start at login. The installer also accepts `--role worker`, `--device-name`,
`--cache-size`, `--lan-only`, `--connectivity-url`, `--stun-server`,
`--turn-server`, `--turn-username`, and `--turn-password` for a named worker,
cache tuning, explicit same-LAN-only operation, direct connectivity, or
operator-provisioned TURN relay fallback. This developer package is ad-hoc
signed, not notarized, and is not yet a public release artifact. See
[`packaging/macos/README.md`](packaging/macos/README.md) for install and
uninstall behavior.
