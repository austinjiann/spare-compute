# ComputeHop

> Turn every computer you own into one personal compute cluster.

## Status

This document is the product and implementation plan for ComputeHop. It defines the
intended user experience, initial architecture, execution contract, security
model, delivery milestones, and acceptance criteria.

### Implementation snapshot

Last updated: 2026-07-30.

| Slice | Status | Delivered behavior |
| --- | --- | --- |
| Job foundation and SQLite persistence | Complete | Versioned job model, validated state transitions, migrations, and durable repositories. |
| Local daemon, IPC, and CLI | Complete | Authenticated user-local control plane with `status`, `doctor`, `run`, `jobs`, `logs`, and `cancel`. |
| Durable native execution | Complete | Detached process-tree supervision, reconnectable logs, cancellation, terminal results, and daemon-restart reconciliation. |
| Privacy-safe LAN discovery | Complete | Cross-platform mDNS presence with expiring in-memory observations and no durable identity in advertisements. |
| Device pairing and trust | Complete | QUIC/TLS pairing, two-sided verification codes, persistent Ed25519 identity pins, revocation, and re-pairing. |
| Explicit remote execution | Complete | Submit to a selected paired LAN worker, observe durable state and logs, and cancel remotely. |
| Durable remote job routing | Complete | Remember the pinned worker that accepted each remote job so job-specific operations reconnect by ID without another device selector. |
| Hosted rendezvous foundation | Complete | Derive rotating anonymous pair credentials and exchange bounded, route-bound, end-to-end encrypted, expiring presence and signaling payloads through a standalone service and HTTPS client. |
| Direct ICE path and signaling foundation | Complete | Gather bounded UDP candidates with Pion ICE, exchange versioned descriptions through pair-encrypted rendezvous presence, select orchestrator/worker paths, report routing without secrets, and carry QUIC over the selected packet connection. |
| Supervised direct internet control | In progress | Daemons reconcile active pair records, retry encrypted rendezvous/ICE negotiation, run the identity-pinned control protocol over selected paths, prefer LAN for jobs, expose path state to CLI/Swift, and support explicit LAN-only daemon/install setup controls. Automated end-to-end and race coverage pass; physical unrelated-network and network-change validation remain. |
| One-VPS staging deployment | In progress | Provider-neutral Compose stack, Caddy HTTPS edge, authenticated coturn relay, bounded ports/quotas, generated local env/secrets, operator-provisioned short-lived TURN credentials for single-owner relay testing, Ubuntu-only firewall/bootstrap preflights with DNS/init/Compose/verify next steps, cwd-independent verification/credential helpers with actionable preflight and running-service failures, health checks, rollback runbook, and daemon-free, root-oriented, flag-customizable `computehop setup vps` checklist with initial cost, SSH, DNS, firewall, bootstrap, and smoke-test guidance are ready; buying the VPS and forced-relay validation remain. |
| CLI and physical Mac validation | In progress | Friendlier `--on`, `--on auto` for the single active worker, safe `connect nearby` for the single nearby unpaired worker with `connect auto` compatibility, and no-`--` command syntax, daemon-free `setup`, role shortcuts `setup orchestrator`/`setup worker`, role-aware `setup mac`, setup-helper support for short-lived TURN relay credentials, and `setup vps`, installer-first worker setup guidance, one-command `smoke`, remote `--no-project` utility runs, pre-submit remote project preparation feedback, actionable auto and explicit worker-selection errors that consistently point at `connect nearby`, example-rich help for `setup`/`status`/`devices`/`connect`/`disconnect`/`jobs`/`run`/`logs`/`cancel`/`outputs`/`smoke`, `devices`, empty `jobs` next-step guidance for setup/connect/smoke/offline states, explicit empty-log guidance, and friendly output-retrieval errors, `run --follow/--wait/--get`, `connect` as the guided pairing entry point that surfaces waiting verification requests before generic device guidance, `disconnect` as the friendly trust-revocation entry point with `unpair` compatibility, inferred and actionable pairing confirmation, first-run `doctor` guidance that points at the exact orchestrator/worker setup commands, duplicate-daemon and incompatible-daemon restart guidance, local daemon identity in status output, hidden legacy `pair` help, merged trusted/nearby presentation with friendly trust labels, LAN-only path visibility for disabled remote connectivity, and stale duplicate LAN-presence suppression are implemented; physical macOS-to-macOS discovery, pairing, execution, restart recovery, logs, and cancellation passed. Windows/Linux remain. |
| macOS menu-bar and Control Center foundation | In progress | SwiftUI `MenuBarExtra`, generated SwiftProtobuf models, authenticated Unix-socket IPC, local daemon identity, first-run next-step guidance, one-click safe nearby-worker connection, compact device selection, native job submission with This Mac or selected workers, project folder selection, output declarations and retrieval, reconnectable logs, cancellation, notifications, and diagnostics build and pass Swift/package checks. The Electron Control Center now covers daemon startup for Control Mac/Worker roles, packaged daemon staging, nearby pairing, device enable/disable, per-device allowed work, deterministic local task planning with optional OpenAI planner fallback and app-managed key storage, project-aware task suggestions, project clearing, remote preparation feedback, logs, cancellation, and output restoration so the menu bar can stay a fast status/task surface. Ad-hoc app bundle, launch-agent template verification, rewritten launch-agent install validation, incompatible manual-daemon install guard, provider-neutral credential abstraction if more planners ship, and per-user launchd installer work remain ready for development. |
| Project snapshots, incremental transfer, and declared artifacts | In progress | Remote runs resolve a local project root, create bounded content-defined snapshots, upload only missing verified chunks, and execute in isolated workspaces. Workers durably collect exact declared files/directories before success; orchestrators fetch only missing verified chunks and restore without overwrites or symlink traversal. Transfer peers negotiate bounded identity/zstd chunk encoding while preserving decoded-content hashes. The persistent verified content cache is SQLite-indexed, LRU-evicted, quota-bound, and protects active jobs plus unacknowledged artifact chunks. Artifact download/restore progress is durable and visible in CLI/Swift job summaries, and `run --get` restores to the submitted working directory by default. Automated LAN/supervised-path reuse, ignore behavior, and artifact coverage pass; secrets, upload progress, byte-range resume, and physical cross-platform validation remain. |
| Later launch slices | In progress | Direct internet control still needs physical unrelated-network and reconnect validation, and public TURN relay issuance requires a hosted entitlement boundary. Full compatibility/resource scheduling, adapters, production packaging, and release operations follow. |

“Complete” here means implemented with automated coverage and merged to `main`.
Physical multi-machine validation remains required by the launch acceptance
criteria. The current implementation boundary is also summarized in the root
README; later sections in this document describe the intended launch product,
not functionality that already exists.

ComputeHop has one complete launch target. The build phases later in this document
describe implementation order only; they are not separate public releases or a
reduced MVP. The product launches after the complete acceptance criteria are met.

ComputeHop is intended to feel like **AirDrop for compute**: nearby machines
appear automatically, pairing happens once on the LAN, and later jobs run from
the same or a different network without IP addresses, SSH configuration,
cluster administration, or repeated approval prompts.

The analogy describes the experience, not the security boundary. ComputeHop executes
code rather than merely receiving files, so pairing must create durable but
revocable trust and jobs must run with limited host access.

---

## 1. Product Vision

ComputeHop is a consumer-first distributed job runtime for computers owned by one
person.

Instead of treating a MacBook, gaming PC, home server, mini PC, and NAS as
unrelated machines, ComputeHop exposes them as one pool of compatible compute. The
user submits a background job from a designated Mac, and ComputeHop:

1. Determines what the job requires.
2. Finds compatible, available machines.
3. Chooses an appropriate machine or explains why none qualify.
4. Transfers an immutable snapshot of the required files.
5. Executes the job natively or in a container.
6. Streams logs and supported progress information.
7. Safely returns declared artifacts.

The user should normally think “run this,” not “which IP address has the right
GPU?” ComputeHop does not make unlike computers identical; it understands their
differences well enough to place jobs safely.

### Example

Instead of:

```bash
ssh desktop
cargo build --release
scp target/release/my-app .
```

the user runs:

```bash
computehop run cargo build --release
```

ComputeHop selects a compatible worker, sends only missing project content, streams
the build output, and restores the declared or Cargo-inferred artifacts.

---

## 2. Goals and Non-goals

### Goals

- Zero-address LAN discovery with human-readable device names.
- One-time, two-device pairing with remembered and revocable trust.
- Automatic cross-network reconnection for paired devices, preferring a direct
  path and using an end-to-end encrypted relay path when NAT or firewalls block
  direct connectivity.
- One designated macOS orchestrator with a CLI and SwiftUI menu-bar app.
- First-class macOS, Windows, and Linux workers.
- Native-process and OCI-container execution.
- Compatibility-aware automatic scheduling with explicit-device override.
- Immutable, incremental project transfer and content caching.
- Durable jobs that continue if the orchestrator sleeps or disconnects.
- Reconnectable stdout, stderr, status, and supported progress streams.
- Safe, explicit artifact collection and restoration.
- Useful compatibility and failure explanations instead of opaque remote errors.
- First-class adapters for Cargo, FFmpeg, and Ollama, while retaining support for
  arbitrary background commands.

### Non-goals

The ComputeHop launch product is not:

- A distributed operating system or distributed-memory runtime.
- A Kubernetes, Docker Swarm, or cloud-platform replacement.
- A remote desktop, screen-streaming, gaming, or interactive editing tool.
- A multi-user or team cluster with accounts, quotas, or role-based access.
- A package manager for compilers, desktop applications, drivers, models, or
  container engines.
- A hosted scheduler, job database, or cloud execution platform. ComputeHop's
  connectivity service performs only rendezvous, NAT traversal assistance, and
  encrypted packet relay; it cannot authorize jobs or read their contents.
- A promise that arbitrary commands can be run on arbitrary operating systems.
- A system for fabricating percentage progress from programs that do not expose
  progress.

Interactive shells and TTY-dependent applications are outside the launch job
contract. Jobs are background processes with closed stdin.

---

## 3. Platform and Ownership Model

### Single-owner ComputeHop

One ComputeHop installation represents one person. Every paired worker trusts one
designated orchestrator. Workers do not trust or communicate with one another.

The orchestrator is the only device that can schedule new work. This produces a
hub-and-worker topology rather than a peer-to-peer control plane, avoiding
leader election, replicated job state, and split-brain behavior.

### Platform matrix

| Platform | Orchestrator | Worker | User interface |
| --- | --- | --- | --- |
| macOS | Yes, one designated Mac | Yes | SwiftUI menu-bar app and full CLI |
| Windows | No | Yes | Worker-management CLI |
| Linux | No | Yes | Worker-management CLI |

The orchestrator's Mac is eligible to execute jobs and is evaluated using the
same compatibility and availability policy as remote workers.

Windows has a system tray, but a Windows tray application is not required for
launch. Windows remains a first-class compute worker without a full graphical
control surface.

### Lifecycle

- Desktop workers run as unprivileged per-user background agents.
- They remain available while the user is logged in, including while the screen
  is locked or the visible app is closed.
- Discovery and connectivity depend on the background agent, not on keeping the
  SwiftUI menu or a worker-management window open.
- A worker is unavailable after logout or shutdown.
- If the orchestrator is lost, a replacement must be explicitly paired with
  each worker again. ComputeHop does not use a cloud account or recovery authority.

---

## 4. System Architecture

```text
        macOS SwiftUI Menu Bar      Electron Control Center
                   │                          │
                   └────────── Local IPC ─────┘
                              │
                 Go Orchestrator Daemon
       ┌───────────────────┼───────────────────┐
       │                   │                   │
   Discovery          Scheduler          Job Database
       │                   │                   │
 Trust Manager    Compatibility Engine   Artifact Cache
       └───────────────────┼───────────────────┘
                           │
            End-to-End Authenticated Session
           Direct QUIC / ICE / TURN Fallback
          ┌────────────────┼────────────────┐
          │                │                │
    macOS Worker      Windows Worker    Linux Worker
    Native/OCI        Native/OCI        Native/OCI
```

Initial discovery and pairing happen directly on the LAN. After pairing, a
connection manager prefers a direct LAN path, then a direct internet path found
through ICE/STUN, and finally a TURN relay. The paired endpoints still
authenticate each other end to end, so the connectivity service can forward
packets but cannot decrypt job commands, files, logs, or artifacts.

### Core components

#### `computehopd`

The cross-platform Go background process. It runs in one of two roles:

- **Orchestrator role:** discovery, pairing, scheduling, project snapshots,
  secrets, global job history, artifact restoration, and local UI/CLI IPC.
- **Worker role:** capability advertisement, job custody, execution, durable
  logs, artifact staging, resource monitoring, and cache management.

On the designated Mac, one daemon may provide both roles.

#### `computehop`

The Go CLI. On the orchestrator it exposes the full job and device interface.
On workers it exposes only local setup and worker-management commands.

#### ComputeHop Menu Bar

A SwiftUI `MenuBarExtra` application that communicates only with the local
orchestrator daemon. It is intentionally small: daemon status, current device,
quick task entry, and a handoff to deeper configuration. It does not implement
networking, scheduling, or job execution itself.

#### ComputeHop Control Center

An Electron desktop application for heavier configuration: synced devices,
allowed work by device, project sync defaults, relay settings, and eventually
an explicit external/LLM planner configuration if that becomes a real feature.
It uses the same local daemon API as the CLI and menu bar for device, pairing,
job, log, cancellation, and artifact operations.

#### ComputeHop Connectivity Service

A small hosted rendezvous/STUN/TURN layer used only after local pairing. It
matches already-paired devices using opaque pair-scoped routing identifiers,
assists direct connection establishment, and relays encrypted packets when a
direct path is unavailable. It is not the scheduler and stores no job state,
project data, logs, artifacts, or device private keys.

The launch service does not require a user account. Losing the designated
orchestrator's identity still requires explicit re-pairing with each worker; the
connectivity service is not an identity-recovery authority.

### Storage

- SQLite stores paired-device records, job metadata, transitions, policies, log
  offsets, artifact metadata, and cache indexes.
- A content-addressed store holds project chunks and artifact data.
- The orchestrator owns the durable global job history.
- Each worker persists the jobs currently or recently entrusted to it so a job
  can survive an orchestrator disconnect or daemon restart.
- Cache and result storage use configurable quotas with least-recently-used
  eviction. Running jobs and uncollected results are never evicted.

---

## 5. Technology Stack

### Repository and language strategy

Use one Go module for the cross-platform runtime and CLI, plus one Xcode project
for the native macOS application.

- **Runtime language:** a supported stable Go release, pinned by the `go` and
  `toolchain` directives in `go.mod`.
- **macOS menu-bar application:** Swift 6 and SwiftUI.
- **Desktop Control Center:** Electron with a small local UI layer; local
  preferences live in the app user-data directory, while cluster-affecting
  settings still belong in the Go daemon.
- **Build system:** the Go toolchain and Xcode/Swift Package Manager for macOS.
- **Dependency locking:** commit `go.mod`, `go.sum`, `Package.resolved`, and
  package lockfiles for JavaScript apps.
- **Configuration:** TOML through `github.com/pelletier/go-toml/v2`.
- **CLI:** `github.com/spf13/cobra` with all commands delegating to shared
  application services.
- **Errors:** standard wrapped errors with `errors.Is`/`errors.As`; preserve
  typed domain and protocol error codes at process boundaries.
- **Structured logging:** the standard library's `log/slog` with local rotating
  JSON logs and concise human-readable console output.

The Go module should separate domain logic from platform code:

```text
cmd/computehop           CLI binary
cmd/computehopd          Orchestrator/worker daemon binary
cmd/computehop-connectivity  Hosted rendezvous service binary
internal/core            Job, device, capability, state, and policy types
internal/protocol        Versioned network and local IPC messages
internal/network         Discovery, pairing, QUIC sessions, and reconnection
internal/connectivity    ICE candidates, rendezvous, and relay policy
internal/store           SQLite state and content-addressed storage
internal/scheduler       Compatibility checks, queues, and placement scoring
internal/executor        Native and container process execution
internal/adapters        Cargo, FFmpeg, Ollama, and future built-in adapters
api/proto                Protocol Buffer definitions
apps/macos/ComputeHop    SwiftUI menu-bar application
apps/control-center      Electron settings and device-management application
```

The daemon and CLI share these internal packages. Do not duplicate job rules or
scheduler logic in Swift.

### Async runtime and concurrency

- Use goroutines, `context.Context`, channels, timers, and
  `golang.org/x/sync/errgroup` for concurrent work and cancellation.
- Model each device connection and active job as a supervised goroutine tree
  with bounded channels, explicit ownership, and propagated cancellation.
- Put hashing, compression, and other CPU-heavy operations behind bounded worker
  pools so transfers cannot consume every core or grow queues without limit.
- Use a single database actor per process to serialize SQLite writes while
  allowing read-only queries through short-lived connections.

### Network and protocol

- Use [quic-go](https://github.com/quic-go/quic-go) for QUIC on macOS, Windows,
  and Linux.
- Use [Pion ICE](https://github.com/pion/ice) for candidate gathering,
  connectivity checks, STUN-assisted NAT traversal, and TURN relay selection.
- Operate [coturn](https://github.com/coturn/coturn) as the initial STUN/TURN
  implementation. Issue short-lived, device-scoped relay credentials and
  enforce connection, duration, and bandwidth quotas.
- Use the standard `crypto/tls`, `crypto/x509`, and `crypto/ed25519` packages for
  TLS 1.3, locally generated certificates, and persistent device identities
  protected by platform filesystem permissions.
- Use `github.com/grandcat/zeroconf` for cross-platform mDNS/DNS-SD discovery,
  isolated behind an internal interface and verified on every supported OS.
- Define control, state, capability, log-envelope, and transfer-manifest messages
  in Protocol Buffers and generate Go types with
  `google.golang.org/protobuf`.
- Wrap every network message in a protocol-version envelope and length-delimit
  messages within their QUIC stream.
- Use TOML or JSON only for local configuration, diagnostics, and human-readable
  data; do not use unconstrained JSON as the trusted network protocol.

The initial protocol is custom RPC over QUIC rather than HTTP/gRPC. This keeps
control messages, resumable byte streams, logs, and artifacts on one transport
without forcing large content through unary RPC calls.

### Hosted connectivity operations

- Keep rendezvous/signaling stateless apart from short-lived opaque presence and
  routing records. Never place job or device-private state in this service.
- Run at least two failure-isolated public relay locations before launch, each
  with static IPv4/IPv6 addresses and the required UDP relay port range.
- Prefer direct paths because relay cost scales with transferred bytes. Meter
  relay traffic by anonymous device credential, apply abuse controls, and show
  the active path in the UI.
- Allow device and job policies to limit relayed input/artifact bytes or require
  confirmation before a large transfer uses paid relay bandwidth.
- Collect service health, connection success, latency, and aggregate byte counts
  without logging payloads or long-lived pair routing identifiers.
- If the hosted service is unavailable, preserve direct LAN operation and all
  durable worker job behavior.

### Local macOS IPC

- Use a user-owned Unix-domain socket between the Swift app/CLI and `computehopd`.
- Reuse a small subset of the Protocol Buffer schema and generate Swift models
  with SwiftProtobuf.
- Use Apple's Network framework for socket transport and Swift concurrency for
  request/response and event streams.
- Authenticate the peer using socket ownership and a random local capability
  token stored in Keychain and a user-only daemon file.

### Database and content store

- Use SQLite through the cgo-free `modernc.org/sqlite` `database/sql` driver so
  release builds do not depend on the host's SQLite version or a platform C
  toolchain.
- Enable [SQLite WAL mode](https://sqlite.org/wal.html), foreign keys, bounded
  busy timeouts, explicit migrations, and periodic checkpoints.
- Keep each database on a local disk; WAL databases must never live on a network
  filesystem.
- Store metadata and state in SQLite, but store project chunks, logs, and large
  artifacts as files referenced by hash.
- Use a fixed 256-bit content identity and deterministic FastCDC-style
  content-defined boundaries behind small internal interfaces. The current
  implementation uses SHA-256; negotiated zstd changes only the bounded wire
  representation and is never part of snapshot identity.
- Use a dedicated Git-compatible ignore matcher for `.gitignore` and
  `.computehopignore`, backed by conformance fixtures for negation, nesting, and
  platform path differences.
- Write content to a temporary path, verify its hash, fsync when durability is
  required, and atomically rename it into the content store.
- Index verified chunks in SQLite with access times, byte counts, and artifact
  references so each daemon can enforce a configurable LRU quota without
  deleting active job input or artifact output that has not been restored and
  acknowledged.

### Native and container execution

- Use the standard `os/exec` package for subprocess I/O and lifetime management.
- Use process groups through `golang.org/x/sys/unix` on macOS/Linux and Job
  Objects through `golang.org/x/sys/windows` so cancellation applies to the
  complete child tree.
- Use platform-native restricted permissions and a per-job workspace; never run
  jobs through an administrator shell.
- Use Docker's official Go SDK (`github.com/moby/moby/client`) against the Docker
  Engine API and the compatible Podman API rather than parsing container CLI
  output.
- Keep all engine-specific behavior behind the container-executor interface.

### Capability and metrics collection

- Use `github.com/shirou/gopsutil/v4` for the portable CPU, memory, process, and
  disk baseline.
- Add small platform backends with `golang.org/x/sys/windows`, macOS
  IOKit/System APIs, and Linux `/proc`/`sysfs` where the portable layer is
  insufficient.
- Use NVIDIA's `github.com/NVIDIA/go-nvml` bindings for NVIDIA identity, memory,
  utilization, and active-process metrics.
- Treat Apple, AMD, and other GPU utilization as optional capability-provider
  modules; scheduler correctness relies on reservations when live telemetry is
  unavailable.

### macOS orchestrator application

- Use SwiftUI with
  [MenuBarExtra](https://developer.apple.com/documentation/SwiftUI/MenuBarExtra)
  in window style for the primary interface.
- Use Observation for app state, Swift concurrency for daemon calls,
  UserNotifications for job events, Keychain Services for secrets, and
  `SMAppService` for launch-at-login registration.
- Keep the Swift process presentation-only. Closing or restarting it must not
  stop the Go orchestrator or running jobs.
- Build a signed, hardened-runtime, notarized universal application bundle that
  contains the Swift UI plus matching arm64/x86_64 Go binaries.

### Worker packaging

- **macOS:** signed/notarized application package with an unprivileged launch
  agent.
- **Windows:** x86_64 MSI built with WiX Toolset and a per-user logon task for
  the worker agent.
- **Linux:** x86_64 and arm64 `.deb`, `.rpm`, and tarball packages with a systemd
  user unit.
- Every release artifact and update manifest is signed. Updates are staged,
  verified, installed atomically where possible, and able to roll back after a
  failed health check.

### Testing, quality, and delivery

- Use `go test ./...`, `go test -race ./...`, and XCTest for Swift.
- Use table-driven tests plus `testing/quick` or `pgregory.net/rapid` for
  state-machine, scheduler, and path-validation properties.
- Use Go's built-in fuzzing for protocol decoders, pairing messages, manifests,
  and path handling.
- Enforce `gofmt`, `go vet`, Staticcheck, `govulncheck`, SwiftFormat, dependency
  license policy, and warnings-as-errors in CI.
- Use GitHub Actions for the macOS, Windows, and Linux build/test matrix, with
  signed release jobs isolated behind protected environments.
- Keep telemetry local and opt-in. Produce structured rotating logs and a
  user-reviewed diagnostic bundle; do not require a cloud observability service.

---

## 6. Discovery, Pairing, and Transport

### Discovery

- Workers advertise availability using mDNS on the local network.
- The launch pairing flow requires the orchestrator and new worker to be on a
  mutually reachable local network. Remote first-time pairing is not supported.
- Advertisements contain only routing and presentation data: protocol version,
  device name, role, and connection endpoint.
- Discovery data is never trusted for identity or authorization.
- VLANs, guest Wi-Fi, client isolation, and multicast filtering may prevent
  discovery. The launch product reports this clearly.
- The background daemon advertises and browses continuously while the user is
  logged in; the visible menu-bar or worker-management UI does not need to stay
  open.

### Pairing

1. The orchestrator shows an unpaired worker discovered on the LAN.
2. The user initiates pairing.
3. Both devices display matching short verification text derived from the
   authenticated handshake.
4. The user confirms on both devices.
5. Each side stores the other side's pinned identity and pair-scoped remote
   connectivity material.
6. Future connections authenticate silently until the pairing is revoked or an
   identity changes.

Headless workers display and accept the verification through their local CLI.
Revocation is available from the Mac app and from the worker CLI.

### Reachability after pairing

Discovery, trust, and reachability are separate:

- **Discovery** answers where a nearby worker is and uses LAN mDNS.
- **Pairing** answers which device identity is trusted and persists until
  revocation.
- **Reachability** answers which network path currently connects the paired
  devices and may change repeatedly without changing trust.

For every paired worker, the connection manager tries paths in this order:

1. A remembered or newly discovered direct LAN endpoint.
2. A direct UDP path selected through ICE/STUN connectivity checks.
3. A TURN-relayed UDP path when NAT, carrier NAT, or a firewall prevents direct
   connectivity.

Both daemons maintain outbound presence with the connectivity service while
remote connectivity is enabled. Rendezvous uses an opaque, pair-scoped routing
identifier and short-lived credentials; it never makes an unpaired device
trusted. The user may disable remote connectivity and operate in LAN-only mode.

Path changes are treated as reconnects, not as proof that one QUIC connection
will live forever. The application protocol resumes control state, logs, and
transfers from durable acknowledgements after the new end-to-end session is
authenticated.

### Identity and encryption

- Each installation generates a long-lived local device key and certificate.
- All trusted traffic uses mutually authenticated TLS 1.3 over QUIC.
- Pairing pins identities; mDNS names and IP addresses never establish trust.
- Direct and TURN-relayed sessions use the same pinned endpoint identities and
  end-to-end authentication. A relay is never a trusted job endpoint.
- Protocol messages include a schema version and reject unsupported versions
  with an actionable upgrade message.
- Local IPC uses Unix-domain sockets on macOS/Linux and named pipes on Windows,
  restricted to the owning user.

### QUIC streams

Use independent streams so one large transfer cannot block control traffic:

- Control requests and state transitions.
- Heartbeats and capability updates.
- Content-addressed input and artifact transfer.
- Sequenced stdout and stderr records.
- Structured progress and resource events.

Logs and events carry monotonically increasing offsets. After reconnecting, the
orchestrator requests records after its last durable offset.

---

## 7. Public Job Interface

### CLI

```bash
# Discovery and trust
computehop setup
computehop setup orchestrator
computehop setup worker --device-name <name>
computehop setup mac
computehop setup mac --role worker --device-name <name>
computehop setup mac --role worker --device-name <name> --lan-only
computehop setup mac --role orchestrator --connectivity-domain <domain> --turn-domain <domain>
computehop setup vps
computehop setup vps --connectivity-domain <domain> --turn-domain <domain> --email <email> --public-ip <ip>
computehop doctor
computehop devices
computehop connect
computehop connect nearby
computehop connect <device>
computehop connect confirm
computehop disconnect <device>

# Ad hoc and saved jobs
computehop run <program> [args...]
computehop run <job-name>
computehop run --on auto <program> [args...]
computehop run --on auto --no-project <program> [args...]
computehop run --on <device> --output <relative-path> <program> [args...]
computehop run --on <device> --output <relative-path> --follow --get <program> [args...]
computehop smoke
computehop smoke --on <device>

# Observation and control
computehop jobs
computehop logs <job-id> --follow
computehop cancel <job-id>
computehop retry <job-id>
computehop outputs <job-id>

# Commands used locally on a worker
computehop worker status
computehop worker pause
computehop worker resume
computehop disconnect <orchestrator>
```

`computehop run` is the single remote-job command. A separate `computehop exec` command is
unnecessary and would create overlapping semantics.

Programs are represented as an executable plus an argument array. ComputeHop does not
invoke a shell unless the job explicitly opts into a particular shell. This
avoids quoting differences and accidental shell interpretation across operating
systems.

### Optional `computehop.toml`

Simple commands remain zero-configuration. When ComputeHop cannot infer requirements,
inputs, outputs, or execution mode, the CLI or UI prompts for the missing facts
and offers to save a named job in `computehop.toml`.

```toml
schema_version = 1

[jobs.release]
command = ["cargo", "build", "--release"]
executor = "native"
working_directory = "."
inputs = ["."]
outputs = ["target/release/my-app"]
secrets = []
retryable = true

[jobs.release.requirements]
tools = ["cargo"]
cpu = 4
memory = "8GiB"
```

The manifest is optional, shareable, and suitable for source control. Secrets
are referenced by name and never stored in the file.

### Core types

#### `JobSpec`

- Stable job ID and optional saved-job name.
- Executable and argument array.
- Native or container executor and optional image reference.
- Working directory and input roots.
- Declared output paths.
- Hard compatibility requirements.
- Resource reservations and limits.
- Non-secret environment values and named secret references.
- Explicit device preference, priority, and retry policy.
- Adapter identity and version when an adapter is used.

#### `DeviceCapabilities`

- Device ID, display name, OS, version, and architecture.
- CPU topology, available memory, free disk, battery, thermal state, and load.
- GPU vendors, models, backends, memory where available, and supported APIs.
- Installed tools and versions discovered by supported adapters.
- Advertised services such as Ollama and their relevant capabilities.
- Available executors and supported container platforms.
- User availability policy and current resource reservations.

#### `PlacementDecision`

- Selected worker or no-compatible-worker result.
- Hard constraints accepted or rejected per candidate.
- Score components for every eligible candidate.
- Human-readable explanation shown in CLI and UI.

#### `JobState`

```text
Created → Validating → Queued → Snapshotting → Transferring
        → Starting → Running → Collecting → Restoring → Succeeded

Terminal alternatives: Failed, Cancelled, Rejected, Lost
```

Every transition is durable and timestamped. A failure includes its phase,
worker, structured reason, and whether retry is safe.

---

## 8. Compatibility and Scheduling

### Compatibility preflight

Preflight occurs before transferring project data. It evaluates:

- Worker trust, connectivity, availability policy, and protocol version.
- Operating system and CPU architecture.
- Native versus container executor availability.
- Container image platform compatibility.
- Required tools, services, versions, and GPU backend.
- Free CPU, memory, disk, GPU memory when measurable, and resource reservations.
- Adapter-specific constraints, such as Cargo target platform or an Ollama model.

If an arbitrary command does not provide enough information for automatic
placement, ComputeHop asks for the missing facts rather than guessing. The answers can
be used once or saved to `computehop.toml`.

Example failure:

```text
No compatible worker can run this job:

- Gaming PC: Docker is not installed
- MacBook: image requires linux/amd64
- Home Server: 6 GiB memory available; job requires 8 GiB
```

### Placement

Scheduling has two phases:

1. Apply hard constraints and remove ineligible candidates.
2. Score eligible candidates using:
   - Current load and existing reservations.
   - Available CPU, memory, disk, and accelerator resources.
   - User device policy, battery, thermal state, and idle state.
   - Expected transfer size and content already cached by the worker.
   - Adapter affinity and platform suitability.
   - Explicit user preference.

The launch scheduler should describe its result as the best **eligible** device,
not as a perfect prediction of runtime. Historical execution time and learned
performance models can be added later without changing the job contract.

### Availability policy

Each device supports:

- Available, paused, and drain states.
- CPU and memory limits.
- GPU availability policy.
- Avoid-on-battery and minimum-battery thresholds.
- Thermal and active-use protection.
- Optional quiet hours.

The orchestrator queues a job if compatible devices exist but are temporarily
busy. It rejects the job immediately when no known device is compatible.

An explicit `--on` choice bypasses scoring but never bypasses authentication,
compatibility, availability, or resource-safety checks.

---

## 9. Project Snapshots and Incremental Transfer

### Project-root selection

- Use the Git repository root when invoked inside a Git worktree.
- Otherwise use the nearest recognized project marker, then the current
  directory when no marker exists.
- Allow an explicit root override.

### Default inclusion rules

- Include regular files that are not excluded by the active ignore rules.
- Respect `.gitignore` and `.computehopignore`.
- Always exclude `.git` and ComputeHop's result-staging directory.
- Reject symlinks and special files; portable snapshots contain regular files
  and inferred directories only.
- Reject absolute paths, `..` traversal, device files, sockets, and escaping
  symlinks.

### Snapshot algorithm

1. Walk eligible inputs and record metadata.
2. Hash file content in chunks.
3. Recheck files that changed during hashing and repeat until a stable snapshot
   is obtained or report that the project is changing too rapidly.
4. Send the manifest of content hashes to the chosen worker.
5. Transfer only chunks absent from the worker's content store.
6. Materialize a fresh, job-specific workspace from verified chunks.

The worker never executes directly from a mutable shared directory.

### Caching

- Content hashes deduplicate input and artifact transfer.
- Adapter caches are isolated by project identity and compatibility tuple, such
  as OS, architecture, toolchain, and relevant options.
- ComputeHop does not skip execution merely because the same command ran previously;
  generic command-result caching is unsafe without an explicit cache contract.
- Cache entries use size quotas and LRU eviction.

---

## 10. Execution

### Native executor

- Starts a normal process as the logged-in, unprivileged user.
- Uses a ComputeHop-managed per-job workspace.
- Receives only declared environment values and named secrets.
- Has no administrator elevation.
- Cannot access paths outside the workspace unless the user explicitly shares
  those paths with the job.
- Captures stdout and stderr independently.
- Tracks and cancels the entire process tree, using platform-appropriate process
  groups or Windows Job Objects.

Native execution supports host-specific tools such as Xcode, Windows compilers,
Metal-backed AI, locally installed FFmpeg, Blender, and Ollama.

### Container executor

- Uses a detected Docker- or Podman-compatible engine.
- Requires an explicit image or adapter-supplied image.
- Validates the image platform against the worker.
- Mounts the job workspace and only explicitly shared paths.
- Applies declared CPU, memory, and GPU limits where the engine supports them.
- Never assumes that Linux containers can produce native macOS or Windows
  outputs.

If a worker lacks the requested engine, ComputeHop reports incompatibility. ComputeHop does
not install or manage a Linux VM, Docker Desktop, Podman, or GPU drivers.

### Tool provisioning

ComputeHop detects required tools and supported versions but does not install them. If
a native job lacks a tool, ComputeHop may suggest a defined container alternative.
Adapters must not silently modify a worker or download large dependencies.

---

## 11. Workload Adapters

Executors answer “how does this process run?” Adapters answer “what does this
workload mean?” Keeping them separate prevents one generic `Executor` interface from
mixing process control, compatibility inference, artifact detection, and progress
parsing.

### Cargo adapter

- Detect `Cargo.toml` and the workspace root.
- Require a compatible Rust/Cargo toolchain or configured container image.
- Respect native target OS and architecture unless a cross-compilation target is
  explicitly declared.
- Retain remote Cargo registry and target caches only within compatible cache
  namespaces.
- Use Cargo's structured message output to identify artifacts and supported
  progress events.
- Return declared or confidently inferred final artifacts rather than copying an
  entire remote target directory indiscriminately.

### FFmpeg adapter

- Parse explicit input and output arguments without treating arbitrary command
  text as a shell.
- Include local input media in the snapshot.
- Require an appropriate FFmpeg installation or container image.
- Infer accelerator requirements only when the requested codec/backend makes
  them explicit.
- Use FFmpeg's machine-readable progress output for duration and progress events.
- Return declared output media atomically.

### Ollama adapter

- Discover workers advertising a reachable local Ollama service.
- Advertise available models and relevant GPU/backend capabilities.
- Route non-interactive inference requests and stream their responses.
- Report a missing model as an incompatibility.
- Never download a model implicitly; an explicit pull command remains a normal
  user-authorized job.

### Future adapters

Likely adapters include Go, Node/pnpm, Docker builds, Blender, HandBrake,
compression, embeddings, and data-processing tools.

Adapters remain internal Go interfaces at launch. A Go interface is not a stable
dynamic plugin ABI. A later third-party plugin system should use a versioned
process protocol or WASM boundary.

---

## 12. Logs, Progress, Metrics, and Artifacts

### Logs and progress

- Stream stdout and stderr as sequenced byte records.
- Buffer records durably on the worker while the orchestrator is disconnected.
- Resume from the last acknowledged offset after reconnecting.
- Expose structured percentage progress only when an adapter or application
  provides a reliable source.
- Generic jobs show running time, logs, and resources without invented progress.

### Resource metrics

Guarantee a portable baseline:

- CPU capability and utilization.
- Total and available memory.
- Free workspace disk.
- Battery, thermal, and availability state where applicable.
- GPU identity and supported backend.

Live GPU utilization and GPU-memory metrics are best-effort because APIs differ
across Apple Silicon, NVIDIA, AMD, Windows, and Linux. Scheduling correctness
must not depend on a metric that a platform cannot provide reliably.

### Artifact collection

- Only declared or adapter-inferred relative paths are collectable.
- The worker hashes and stages artifacts before reporting job success.
- The orchestrator verifies hashes during transfer.
- Outputs are restored atomically to their declared local paths.
- The orchestrator records the local version of each output path at submission.
- If a destination changed during the job, ComputeHop preserves the remote artifact in
  a per-job results directory and reports the conflict instead of overwriting.
- Artifacts remain fetchable until acknowledged or expired by an explicit
  retention policy.

---

## 13. Failure and Recovery Semantics

### Orchestrator disconnect

- A started worker job continues.
- The worker keeps state, logs, and artifacts locally.
- The Mac retries a direct LAN path, then direct ICE connectivity, then TURN
  relay fallback. After authenticating the paired worker, it resumes event
  streams by offset and collects the result.
- No new jobs can be scheduled while the designated orchestrator is unavailable.

### Worker disconnect

- A queued or transferring job waits for a bounded reconnect period.
- A confirmed-running job becomes `Disconnected` while its state is unknown. It
  becomes `Lost` only after the recovery policy expires without authoritative
  worker state.
- ComputeHop does not automatically start a second copy unless the job is explicitly
  marked retryable.

### Daemon and machine failures

- Durable transition records allow either daemon to reconstruct job state.
- A worker-side runner writes logs and terminal status independently enough for
  the worker daemon to recover after a daemon restart.
- A full machine crash terminates native and container work unless the underlying
  runtime independently preserved it; ComputeHop reports failure rather than claiming
transparent continuation.

### Connectivity-service failure

- Existing direct LAN or internet sessions continue without the rendezvous
  service.
- Local discovery, pairing, scheduling, and direct LAN execution remain usable
  during a service outage.
- New cross-network sessions may be unavailable until rendezvous or TURN
  recovers; workers continue accepted jobs and retain logs and artifacts.
- Clients retry with bounded exponential backoff and clearly distinguish
  `Worker offline` from `Connectivity service unavailable`.

### Cancellation

- Cancellation is an authenticated, idempotent request.
- While the worker is unreachable, cancellation remains `Pending` and the UI
  must not claim that the process stopped until the worker acknowledges it.
- The worker sends a graceful termination signal to the complete process tree.
- After a configurable grace period, it force-terminates remaining processes.
- Partial outputs are not restored as successful artifacts but remain available
  for diagnostics according to retention policy.

### Retry rules

- Transfer and connection operations may retry before execution begins.
- After the worker confirms process start, no automatic rerun occurs unless the
  job definition explicitly declares retry safety.
- Manual retry creates a new job ID linked to the previous attempt.

---

## 14. Security Model

### Trust boundary

Pairing grants the orchestrator authority to submit unprivileged jobs to the
worker's managed ComputeHop workspace. It does not grant administrator access or
unrestricted filesystem access.

### Required protections

- Mutually authenticated and encrypted network traffic.
- Verification on both devices during initial pairing.
- Persistent identity pinning and explicit revocation.
- Authenticated, user-scoped local IPC.
- Workspace path normalization and symlink-escape protection.
- No automatic forwarding of the submitter's complete environment.
- Explicit secret names, encrypted transport, no cache persistence, and no
  manifest serialization.
- Process-tree containment and resource limits.
- Hash verification for transferred content and artifacts.
- Replay protection for pairing, commands, cancellation, and state transitions.
- Pair-scoped, unguessable rendezvous identifiers and short-lived STUN/TURN
  credentials with rate limits and revocation.
- End-to-end encryption on relayed paths; the connectivity service may observe
  routing metadata, timing, and byte counts but cannot decrypt payloads.
- No job commands, file contents, logs, artifacts, secrets, or private device
  keys persisted by the connectivity service.
- Audit records for pairing, revocation, submission, placement, cancellation,
  and terminal job state.

### Secret behavior

- Secrets originate from macOS Keychain or a masked interactive prompt.
- A job requests secrets by logical name.
- The orchestrator releases them only after worker authentication and job
  acceptance.
- The worker injects them into the declared environment or a temporary file,
  according to the job definition.
- Temporary secret files are permission-restricted and removed after execution.
- Values are never intentionally written to SQLite, content stores, manifests,
  or diagnostic bundles.

No generic redaction system can guarantee safety if the child process prints a
secret itself. The UI must warn about that limitation.

---

## 15. macOS User Experience

### Menu-bar surface

The menu bar is the fast path, not the settings product. It should show:

- daemon status and local identity;
- worker availability at a glance;
- a compact device picker;
- one natural-language task box;
- project selection when needed;
- the current/most recent job only when it needs attention;
- refresh, quit, and Control Center handoff.

Avoid putting detailed policy, route, cache, relay, capability, or historical
job management in the menu bar.

### Control Center surface

The Control Center owns the heavier setup and management flows:

- synced devices and trust/revocation;
- allowed work by device;
- connection mode, LAN-only, relay, and VPS settings;
- default project sync and artifact behavior;
- cache quotas;
- logs/history views;
- future external/LLM planner provider and permission settings, only after that
  planner exists.

#### Notifications

- Connection requests with two-sided verification codes.
- Job completion and failure.
- Artifact conflicts.
- Worker incompatibility or required setup.
- Lost connectivity and recovered jobs.

### AirDrop-like qualities

- Nearby devices appear automatically.
- Connecting is deliberate but performed only once per device identity.
- Trusted workers reconnect without prompts.
- Commands use device names, never addresses.
- Automatic placement is the default, while explicit selection remains easy.
- Failures explain what the user can do next.

---

## 16. Launch Build Plan

Every phase below is required for launch. The phases are an internal dependency
order that keeps the system testable while it is built; completing an early
phase does not redefine the product as a smaller release.

### Phase 1: Trusted cross-platform execution

- Go daemon and CLI on macOS, Windows, and Linux.
- Designated Mac orchestrator and worker roles.
- mDNS discovery.
- Two-sided pairing, persistent identities, reconnect, and revocation.
- Explicit-device native execution in a managed workspace.
- Durable job state, stdout/stderr streaming, and cancellation.

**Exit condition:** From the Mac, a user discovers and pairs a worker, runs a
background command without an IP address, watches its logs, and cancels or sees
its terminal result.

### Phase 2: Paired connectivity across networks

- Opaque pair-scoped rendezvous and authenticated background presence.
- ICE/STUN candidate gathering and direct UDP connectivity checks.
- TURN relay fallback with short-lived credentials, quotas, and path reporting.
- End-to-end paired-device authentication over both direct and relayed paths.
- Reconnection after network changes without repeating device pairing.
- Connectivity-service outage behavior and an explicit LAN-only mode.

**Exit condition:** After pairing on the same LAN, the Mac can move to another
network, reconnect to the worker directly or through the relay, run a background
command, stream logs, and issue an acknowledged cancellation without exposing
job contents to the connectivity service.

### Phase 3: Snapshots and artifacts

- Project-root detection and ignore rules.
- Stable content-addressed snapshots.
- Incremental transfer and worker content cache.
- Declared outputs, hash-verified return, atomic restoration, and conflict
  preservation.
- Explicit encrypted secrets.
- Disconnect/reconnect log and artifact recovery.

**Exit condition:** A project job continues while the Mac sleeps and safely
returns its declared output after reconnection without resending unchanged
content.

### Phase 4: Compatibility and scheduling

- Capability collection and versioned advertisements.
- Compatibility preflight with per-device explanations.
- Resource reservations and job queue.
- Automatic availability policy and placement scoring.
- Manual device override.
- Native and detected Docker/Podman executors.

**Exit condition:** ComputeHop chooses among unlike workers without attempting known
incompatible placements and clearly explains every rejection and selection.

### Phase 5: Representative workloads

- Cargo adapter and compatible remote caches.
- FFmpeg adapter and structured progress.
- Ollama service/model discovery and request routing.
- Adapter-specific requirements, outputs, metrics, and diagnostics.

**Exit condition:** Development, media, and AI workflows each complete through
the same generic job lifecycle while exposing their necessary specialization.

### Phase 6: Consumer experience and hardening

- SwiftUI menu-bar application.
- Installers, launch-at-login, upgrades, protocol compatibility, and uninstall.
- Device policies, notifications, diagnostics, cache controls, and job history.
- Security review, fuzzing, chaos testing, performance work, and documentation.

**Exit condition:** A new user can install ComputeHop, pair a machine locally,
run representative jobs from the same or a different network, understand
failures, revoke access, and recover from ordinary network, relay, or sleep
interruptions without cluster expertise.

### After launch

- Additional adapters and versioned third-party plugin protocol.
- Windows tray application if demand justifies it.
- Historical runtime estimates and learned placement.
- Multi-region connectivity-service capacity, smarter path selection, and
  optional account-based device recovery only after a separate security design.
- Remote first-time pairing only if it can preserve two-sided verification and
  resist unsolicited pairing abuse as well as the local flow.
- Household or multi-user sharing only after identities, permissions, quotas,
  and audit semantics are explicitly designed.

---

## 17. Engineering Execution and Deployment Plan

The launch build phases define what must exist. This section defines how to
build, integrate, deploy, and release it. The steps are dependency-ordered
engineering checkpoints, not smaller public product versions. ComputeHop still
launches only after the complete acceptance criteria are satisfied.

### Execution strategy

Build the system as a sequence of working vertical slices. Each slice must pass
through the real CLI, daemon, persistence layer, and protocol rather than being
implemented as disconnected packages that are integrated at the end.

```text
contracts and state machines
            |
local durable execution
            |
LAN discovery and paired remote execution
            |
cross-network direct and relayed execution
            |
snapshots, artifacts, and reconnect recovery
            |
compatibility, reservations, and scheduling
            |
workload adapters and macOS experience
            |
security hardening, packaging, and launch
```

For every step:

1. Define or update the domain contract and legal state transitions.
2. Add database migrations and versioned Protocol Buffer messages when needed.
3. Implement the smallest end-to-end path through the real boundaries.
4. Add unit, integration, failure, and security tests appropriate to the path.
5. Exercise it on physical target machines, not only local processes or CI.
6. Record the supported behavior, diagnostics, and rollback behavior.
7. Merge only when the repository remains buildable and earlier slices still
   pass.

### Step 0: Foundation and frozen boundaries

- Create the Go module, Swift application workspace, Protocol Buffer packages,
  migration directory, test fixtures, packaging directories, and CI workflows.
- Define stable domain types for devices, identities, capabilities, jobs,
  requirements, reservations, logs, artifacts, connection paths, and errors.
- Define the job-state machine, retry rules, ownership rules, protocol-version
  negotiation, and database migration policy before implementing networking.
- Keep generated Protobuf types, SQLite rows, QUIC messages, and Swift view
  models at system boundaries. Core scheduling and job logic use domain types.
- Establish dependency rules: commands perform wiring, application packages
  coordinate use cases, and infrastructure packages implement narrow
  interfaces owned by the core.
- Configure formatting, linting, dependency scanning, unit tests, race tests,
  fuzz targets, cross-platform compilation, and reproducible release metadata.

**Checkpoint:** All three Go binaries and the empty Swift application build in
CI, schemas can be generated deterministically, migrations apply to a fresh
database, and domain-state tests pass.

### Step 1: Durable local job slice

- Start `computehopd` in orchestrator and worker roles on one Mac.
- Connect the CLI to the daemon through authenticated local IPC.
- Persist a submitted job, execute it in a managed workspace, capture ordered
  stdout/stderr, and persist its terminal result.
- Implement process-tree cancellation, daemon restart recovery, job listing,
  log offsets, structured errors, and local diagnostics.
- Prove the custody rule: once a worker accepts a job, its database is the
  authority for execution even if the submitting client disappears.

**Checkpoint:** A CLI-submitted job survives a UI or CLI exit, exposes durable
logs, can be cancelled without orphaning children, and has the correct state
after a daemon restart.

### Step 2: LAN discovery, trust, and explicit remote execution

**Implementation status:** discovery, pairing, daemon-free first-run `setup`,
role shortcut macOS installer command generation with `setup orchestrator` and
`setup worker`, role-aware macOS installer command generation with `setup mac`,
short-lived TURN relay credential flags in the setup helpers, and
flag-customizable one-VPS `setup vps`, first-run `doctor` with daemon-not-running and missing-worker setup command guidance,
duplicate-daemon startup guidance for local socket or ComputeHop port conflicts,
local daemon identity in status output, `connect` as the guided pairing entry point,
safe `connect nearby` for the single nearby unpaired worker, actionable `connect confirm`
messages, friendly `disconnect` with legacy `unpair` compatibility, actionable `--on auto` and explicit worker-selection failure guidance, `--on auto` for the single active worker, explicit `run`,
remote `run --no-project` for utility commands that need no local files, one-command `smoke`,
pre-submit remote project preparation feedback before snapshot/upload,
`run --follow/--wait/--get`, `jobs`, `logs`, and `cancel` routing through
identity-pinned QUIC, and durable remote job placement are
implemented. Remote runs now transfer an immutable project
snapshot and use an isolated worker workspace; `-C` identifies the local source
directory and defaults to the CLI's current directory, while `--no-project`
intentionally skips snapshot transfer. The macOS-to-macOS
physical flow has passed discovery, pairing, execution, daemon restart recovery,
durable log retrieval, and cancellation. The checkpoint remains open until
Windows and Linux workers pass the same physical flow.

- Advertise and browse daemon endpoints with mDNS without treating discovery as
  authentication.
- Implement device identity creation, two-sided verification, trust storage,
  reconnect, revocation, and re-pairing.
- Establish mutually authenticated QUIC sessions pinned to paired identities.
- Run the existing durable job slice on an explicitly selected worker, then add
  a safe `--on auto` path before full compatibility scheduling.
- Persist remote job placement on the orchestrator so later job-specific
  operations reconnect to the correct worker by ID after daemon restarts.
- Validate macOS-to-macOS, macOS-to-Windows, and macOS-to-Linux behavior on
  physical machines as soon as each worker build exists.

**Checkpoint:** `computehop connect` first shows any waiting verification
request and exact confirm/reject next steps, while `computehop connect nearby`
starts trust setup only when one nearby unpaired worker is visible.
`computehop smoke` runs a cheap hostname check against the selected worker.
`computehop run --on auto <command>` selects the only active worker and explains
how to connect or choose explicitly when it cannot select safely, while
`computehop run --on <name> <command>` discovers a specific worker without an
address, pairs it once, reconnects without prompting, streams logs, and explains
how to list, connect, or disambiguate workers when the requested name or short
ID is not an active paired worker.

### Step 3: Cross-network connectivity

**Implementation status:** the merged foundation derives a separate 256-bit
connectivity secret during confirmed pairing, persists it only on the two
devices, rotates anonymous route credentials every five minutes, and adds a
bounded in-memory rendezvous/signaling service. The service sees only opaque
route IDs, credential digests, endpoint roles, timing, and encrypted payloads.
The client now enforces HTTPS, refuses redirects, bounds responses, maps typed
service errors, and encrypts payloads with authenticated role, direction,
generation, and rotating-route context so stored ciphertext cannot be replayed
in a later credential window. A versioned encrypted presence document now
carries bounded non-trickle ICE credentials and candidates, and an integration
test exchanges real descriptions through the opaque service before selecting a
working path. The Pion path primitive also carries a QUIC stream and reports
whether ICE selected a host, server-reflexive, or relay candidate without
exposing credentials.
The current deployment slice adds a one-VPS staging stack with Caddy automatic
HTTPS and an authenticated coturn STUN/TURN service using a bounded relay port
range and Docker secret. A checked `deploy/vps/init.sh` creates the local `.env`
file and server-only TURN shared secret after domains and a public IPv4 are
known. A checked `deploy/vps/turn-credentials.sh` derives short-lived coturn
REST username/password credentials from that server-only secret and prints
friendly setup-helper commands plus direct macOS installer commands for
single-owner forced-relay testing without copying the shared secret to clients.
The containers have been built and locally smoke
tested, but the live VPS has not been purchased. Daemons now reconcile active
pair records, retry encrypted presence exchange and ICE selection, run the
existing identity-pinned QUIC control protocol over ready paths, and make those
paths available after LAN attempts fail. CLI and Swift surfaces report
connecting, remote, disabled, and selected-path state, while daemon, setup, and
macOS installer controls support an explicit LAN-only mode that refuses remote
connectivity flags. Automated end-to-end tests cover the full
rendezvous-to-authenticated-control lifecycle, but physical unrelated-network
and network-change testing remain. Existing pairings without connectivity
material must be explicitly re-paired before remote testing.

An anonymous pair route proves possession of a peer-created secret, not a
customer entitlement to consume paid relay bandwidth: anyone can create a new
pair and route. Shared public staging and production therefore must not issue
TURN credentials until the service can verify an expiring, revocable
entitlement or invite and enforce per-entitlement quotas. The coturn shared
secret remains server-only. A single-owner self-hosted environment can use the
operator-provisioned short-lived credentials from `deploy/vps/turn-credentials.sh`
while this service boundary is being built.

- Deploy a staging rendezvous service, STUN endpoints, and TURN relays before
  implementing remote path selection in the client.
- Add anonymous, short-lived presence and pair-scoped signaling that disclose
  no job content to the hosted service.
- Gather ICE candidates and attempt paths in order: LAN direct, remote direct,
  then TURN relay.
- Run the same pinned, mutually authenticated application session over every
  path so the relay never becomes the trust boundary.
- Add path visibility, reconnect after network changes, credential expiry,
  relay quotas, and large-transfer confirmation.

**Checkpoint:** Devices paired on a LAN can later execute, observe, reconnect,
and cancel from unrelated networks. Forced TURN tests prove that job content is
still end-to-end encrypted.

### Step 4: Project snapshots, transfer, and artifact recovery

**Implementation status:** project transfer and explicit artifact return are implemented end to end.
The orchestrator prefers an enclosing Git root, falls back to the nearest known
project marker or selected directory, applies nested `.gitignore` and
`.computehopignore` rules, retries unstable reads, and creates a bounded,
canonical SHA-256 manifest over deterministic content-defined chunks. The
worker preflights its persistent verified content store, receives only missing
chunks, re-verifies every read and upload, repairs corrupt cached entries, and
atomically materializes a new owner-only workspace for each accepted job.
Transfers are resumable at chunk granularity because a retried submission
preflights the persistent cache again. Real LAN and supervised-path integration
tests execute from the reconstructed workspace and prove that an unchanged
second submission uploads no chunks. Peers negotiate zstd or identity per
chunk, fall back to identity when compression saves fewer than 64 bytes, cap
encoded and decoded sizes, and verify SHA-256 over decoded bytes. Jobs may
declare up to 64 portable relative output files or directories. The worker
recursively collects exact declarations
into an immutable manifest, remains cancellable while collecting, and reports
success only after durable publication. The orchestrator preflights its local
content store, downloads and re-verifies only missing chunks, stages a complete
result, and never overwrites existing files or follows destination symlinks;
conflicts are preserved under `.computehop-conflicts`. After a full restore, the
orchestrator acknowledges the artifact bundle so the worker may evict those
chunks under later cache pressure. The persistent verified content cache now has
a configurable quota, LRU pruning, startup reconciliation, snapshot
reservations, active-use pins, and conservative protection while jobs are
running or collecting artifacts. CLI aliases and the macOS menu expose retrieval
after restart by durable job placement. Artifact download and local restore
progress is persisted independently of job ownership, so the orchestrator can
show progress for worker-owned jobs in CLI and Swift job summaries. Immediate
`run --get` restores declared outputs to the submitted working directory by
default while the standalone `artifacts` command keeps its isolated
`.computehop-results/<job-id>` default for later/manual retrieval. Symlinks and
special files are safely rejected for now rather than preserved. Upload progress,
byte-range resume, secret delivery, and physical Windows/Linux validation remain,
so the Step 4 checkpoint is not complete.

- Add project-root resolution, ignore semantics, immutable manifests,
  content-defined chunks, negotiated compression, and a bounded content cache.
- Preflight manifests before sending content, request only missing chunks, and
  verify every received chunk by hash.
- Enforce a persistent content-cache quota with LRU eviction, startup
  reconciliation, active snapshot reservations, and artifact retention until
  successful restoration is acknowledged.
- Persist artifact download/restore byte progress and expose it through local
  and remote job summaries in the CLI and macOS menu bar.
- Materialize each job into an isolated workspace and never execute directly
  from the cache.
- Add declared artifacts, staged collection, hash verification, resumable
  return, atomic local restoration, and conflict preservation.
- Add reconnectable log and transfer offsets plus explicit, encrypted,
  non-durable secret delivery.

**Checkpoint:** Repeating an unchanged project job transfers almost no project
content; changing one region transfers only affected chunks; disconnecting the
Mac does not lose the job, logs, or safely declared output.

### Step 5: Compatibility and scheduling

- Collect versioned capabilities and normalize platform, architecture, tools,
  container engines, services, models, GPUs, memory, storage, power, and load.
- Implement compatibility as a hard filter with a specific rejection reason for
  every excluded worker.
- Add resource reservations, per-worker queues, availability policy, placement
  scoring, deterministic tie-breaking, and manual override.
- Add the OCI executor only after native execution has complete lifecycle and
  cancellation semantics; reuse the same workspace, logs, artifact, and job
  state contracts.
- Ensure stale telemetry affects ranking but cannot bypass reservations or hard
  compatibility rules.

**Checkpoint:** Automatic placement chooses only compatible workers, does not
overcommit reserved resources, and explains both its selection and every
rejection.

### Step 6: Representative workload adapters

- Implement Cargo, FFmpeg, and Ollama adapters on top of the generic job model.
- Give each adapter explicit detection, requirements, inputs, outputs, progress,
  and diagnostic behavior without embedding workload rules in the scheduler.
- Test native and container paths where the workload supports both.
- Publish one reproducible end-to-end example for development, media, and AI.

**Checkpoint:** All three workload families complete through the same job
lifecycle, while unsupported platform, tool, model, or GPU combinations fail in
preflight rather than after a large transfer.

### Step 7: macOS product experience

**Implementation status:** the SwiftUI menu-bar foundation now builds from the
root Swift package, uses generated SwiftProtobuf models, authenticates to local
IPC protocol v6, and presents daemon health with local identity, first-run
next-step guidance, compact device selection, task planning, native job
submission to the Mac or a paired available worker, local project folder
selection for incremental remote transfer, reconnectable output, artifact
restoration, and cancellation. Unit tests cover framing, ping identity mapping,
Auto worker target submission, no-project Smoke Test submission, pairing
confirmation guidance, revocation actions, setup guidance, CI/check planning
through repository validation targets such as `make pr-check`, package-manager
script selection, invalid
command-input guidance, empty-log placeholders, copyable CLI run/log handoffs,
job-completion notifications and their persisted setting, diagnostic command
copying, a menu-bar handoff button that opens the Electron Control Center when
installed or packaged in the development checkout, named pending-worker state
instead of silently falling back to This Mac when a selected worker disappears,
configurable setup/VPS defaults, and safe command parsing; a real
Swift client has successfully pinged the Go daemon, submitted a durable native
job, and read its output. A separate Electron Control Center now owns the
heavier settings surface, connects to the daemon over local IPC, lists nearby
and trusted devices, pairs, disables/re-enables, or forgets workers, plans plain-language tasks into
previewed commands with package/Makefile/language/Docker fallbacks, prefers repository
validation targets for CI/check requests, recognizes package/release targets such
as `make macos-package`, includes conservative inferred outputs for known package
targets such as `dist/macos/ComputeHop.app`, can use an
optional OpenAI planner fallback for tasks local rules cannot map, runs a projectless
smoke test on the selected computer, suggests project-aware Check/Test/Build/Lint/Docker
task chips after project selection, applies allowed-work checkboxes to suggested and
planned work, keeps unknown exact commands behind an explicit allowance, avoids
substituting the development checkout when no project is selected, keeps projectless
utility jobs from snapshotting selected projects, and blocks project-style runs until
a project folder is selected. AI-planned commands are still
previewed, mapped through the same per-device allowed-work policy, and rejected
before preview when they contain shell operators, multiline commands, shell
wrappers, obvious interactive commands, privilege escalation, or destructive
removal. The optional OpenAI key can be saved from
the app using Electron `safeStorage` encryption where available, with
environment variables retained as a fallback. It can also start the local daemon
from the app in development and
from the bundled daemon in packaged builds, package an unpacked current-platform
app directory with the daemon copied into Electron's runtime resources, persist
Control Center preferences under the app user-data directory, auto-start the
daemon once after settings/runtime load when LAN discovery is enabled, offer an Auto
worker target and default to it until the user makes an explicit device choice
when exactly one connected worker is available, persist explicit run-target
choices across app restarts without silently falling back to This Mac while a
remote target is temporarily unavailable, show a named waiting state for the
selected worker while it reconnects, keep run and job-history actions disabled
with a worker-specific explanation until that worker is available, display the
resolved backing worker name for Auto-worker submissions, route Auto-worker job follow-ups through remembered
job placement, list recent jobs for the
selected computer, open persisted job logs, declare files/folders to bring back,
show remote snapshot/upload preparation feedback before long project
submissions, detach live Control Center streams without cancelling daemon jobs
when the window closes, restore succeeded job outputs to the job's submitted
project folder by default, prompt for output restore after successful
UI-submitted jobs with declared or inferred outputs, and cancel listed jobs.
Durable daemon-backed cluster settings for that app remain. A host-architecture
developer app bundle now includes the menu app, CLI, and daemon; a guarded
per-user installer configures an
unprivileged launch agent and preserves durable state on uninstall. Developer
ID signing, notarization, universal release binaries, upgrade handling, and
clean-machine tests remain.

- Connect the SwiftUI menu-bar app to the stable local IPC contract.
- Add device discovery, connection confirmation, trust and revocation, connection
  path, policies, job submission, logs, cancellation, history, artifacts,
  notifications, diagnostics, and cache controls.
- Keep the app presentation-only so restarting it cannot interrupt the daemon,
  transfers, or jobs.
- Test first install, launch at login, permission prompts, daemon health,
  upgrades, protocol mismatch, and uninstall on a clean Mac user account.

**Checkpoint:** A new user can complete the representative workflows without
typing an address, editing configuration, learning cluster terminology, or
keeping the menu open.

### Step 8: Hardening and release candidates

- Complete the verification matrix in the next section, including adversarial
  inputs, reconnects, resource exhaustion, sleep, network changes, and upgrade
  compatibility.
- Perform a focused security review of pairing, key storage, local IPC, relay
  credentials, path handling, secret delivery, native execution, and updates.
- Establish performance budgets for idle resource use, discovery time, pairing,
  reconnect, scheduling, incremental transfer, log latency, and relay overhead.
- Run signed internal builds, a small hardware-diverse private test, and release
  candidates with production-equivalent connectivity infrastructure.
- Freeze new features during release-candidate testing; only correctness,
  security, compatibility, packaging, and documentation fixes enter the launch
  branch.

**Checkpoint:** The complete launch acceptance checklist passes against signed
release artifacts and production-equivalent hosted services.

### Environments

Maintain three isolated environments:

- **Development:** local daemons, local test certificates, simulated NAT and
  failure fixtures, and an optional local TURN server. No production keys.
- **Staging:** public rendezvous/STUN/TURN endpoints, separate signing-independent
  client configuration, synthetic health checks, short retention, and strict
  quotas. All release candidates are exercised here first.
- **Production:** at least two failure-isolated connectivity locations, protected
  secrets, monitored relay traffic, abuse controls, capacity alarms, and an
  independently testable rollback path.

Development, staging, and production must use different service credentials,
databases or ephemeral state namespaces, DNS names, and update channels. A
client displays its channel in diagnostics and never silently changes channels.

### Hosted connectivity deployment

1. Provision one small public staging VPS with a static IPv4 address and
   sufficient transfer allowance. Production later requires at least two hosts
   in different failure domains.
2. Configure DNS for rendezvous and TURN endpoints, TLS certificates, firewall
   rules, UDP connectivity, TURN relay port ranges, and time synchronization.
3. Deploy `computehop-connectivity` and coturn as separately supervised,
   least-privileged services with immutable configuration.
4. Keep presence and signaling records short-lived. Do not store job payloads,
   project names, commands, artifacts, secrets, or long-lived pair mappings.
5. Publish health checks for rendezvous, STUN, TURN allocation, and an actual
   forced-relay ComputeHop session rather than relying only on process uptime.
6. Alert on allocation failures, connection success, latency, bandwidth,
   unusual credential use, disk pressure, certificate expiry, and regional
   unavailability.
7. Deploy to staging, run compatibility and forced-relay tests, canary one
   production location, then roll out to the second location.
8. Preserve the previous server artifact and configuration for immediate
   rollback. Protocol changes remain backward-compatible for at least one
   client upgrade window.

Relay bandwidth is the main variable operating cost and abuse risk. Prefer
direct connections, issue short-lived scoped credentials, enforce per-device
and global quotas, and require confirmation before unexpectedly large relayed
transfers. Do not launch an unlimited anonymous TURN service.

### Client build and release pipeline

Every tagged release runs the following protected pipeline:

1. Validate formatting, generation, migrations, licenses, unit tests, race
   tests, fuzz smoke tests, integration tests, and cross-platform compilation.
2. Build versioned Go binaries for the supported operating systems and
   architectures from the same commit.
3. Build the universal macOS app containing matching Swift and Go components,
   sign it with Developer ID, enable the hardened runtime, notarize it, and
   staple the result.
4. Build and sign the Windows MSI and worker executables.
5. Build Linux `.deb`, `.rpm`, and tarball packages plus checksums.
6. Generate a signed update manifest containing versions, protocol ranges,
   hashes, sizes, channels, and rollback metadata.
7. Install every artifact on clean virtual machines or users, start its daemon,
   run a pairing and job smoke test, exercise upgrade from the previous release,
   and verify uninstall behavior.
8. Publish first to the internal channel, then private test, then a small stable
   canary, and finally the stable channel after health and crash gates pass.

Signing and notarization credentials live only in protected CI environments or
hardware-backed signing services. Normal pull requests can test packaging but
cannot produce trusted public releases.

### Rollout and rollback

- Hosted services deploy independently from clients and remain compatible with
  the current and previous supported client protocol range.
- Client updates are staged by channel and percentage. A failed health check or
  abnormal crash, pairing, connection, or job-failure rate pauses promotion.
- Never downgrade a user database destructively. Migrations are forward-safe,
  backed up before risky changes, and tested against the previous public schema.
- A failed client update restores the last known-good application and daemon
  together; mixed component versions are rejected with a useful diagnostic.
- A hosted rollback must not invalidate already accepted worker jobs or block
  LAN/direct operation.
- Revoked release keys, compromised relay credentials, and emergency update
  disablement have documented runbooks before launch.

### Accounts and infrastructure to prepare

- Source repository with protected branches, CI environments, release channels,
  dependency alerts, and encrypted secrets.
- Apple Developer membership, Developer ID certificates, notarization access,
  and clean signing/release Macs or protected macOS CI runners.
- Windows code-signing arrangement before distributing public installers.
- Product domain, DNS control, transactional support email, privacy policy,
  security contact, and a vulnerability-reporting process.
- Two public connectivity hosts, bandwidth budgets, spending alerts, service
  monitoring, certificate automation, and an incident notification path.
- A physical test pool containing at least Apple silicon macOS, Windows x86_64,
  Linux x86_64, and representative NVIDIA hardware; add Linux arm64 if it is a
  launch target.
- Clean install and upgrade fixtures for the oldest supported OS versions, plus
  networks that can exercise IPv4, IPv6, carrier-grade NAT, blocked UDP, and
  forced TURN relay.

PostgreSQL, Redis, Kubernetes, and a general account system are not launch
prerequisites. Add shared durable service storage only if the connectivity tier
must horizontally scale beyond short-lived in-memory routing, and design account
recovery separately from paired-device trust.

### Execution tracking

Track work by the checkpoints above, not by package completion percentages. A
checkpoint is complete only when its real end-to-end scenario works and its
failure behavior is tested.

For a solo build, keep only one primary vertical slice in progress. Small
platform, packaging, and infrastructure tasks may proceed alongside it when
they do not create a second unfinished architecture. At the end of each week or
iteration:

- Demonstrate the newest end-to-end behavior on real machines.
- Record failures, security assumptions, unresolved decisions, and measured
  performance.
- Re-run all earlier checkpoint scenarios.
- Choose the next dependency that prevents the following checkpoint.
- Remove or defer speculative abstractions that are not required by a launch
  acceptance criterion.

The first implementation target is deliberately small but real: persist a job,
execute it locally through the daemon, stream ordered logs through the CLI,
cancel its full process tree, restart the daemon, and recover the correct state.
Everything else extends that proven lifecycle across trust, networks, machines,
files, schedulers, and user interface.

---

## 18. Verification Strategy

### Unit tests

- Manifest parsing, schema upgrades, and validation.
- Compatibility predicates and deterministic scheduler scoring.
- Job-state transition legality and retry classification.
- Snapshot inclusion, ignore behavior, hashing, and unstable-file detection.
- Path normalization, traversal rejection, and symlink containment.
- Artifact conflict and atomic-restore behavior.
- Log sequencing, acknowledgement, and reconnect offsets.
- Adapter command parsing and progress event handling.

### Integration tests

- Multi-process discovery, pairing, reconnect, revocation, and re-pairing.
- Same-LAN pairing followed by cross-network direct ICE and TURN-relayed
  reconnection.
- Connectivity path changes among local direct, remote direct, relayed, and
  offline states without duplicating jobs or logs.
- Native and container execution with resource constraints.
- Incremental snapshots and missing-chunk transfer.
- Orchestrator disconnect during transfer, execution, and artifact collection.
- Worker daemon restart while its runner is active.
- Cancellation of nested process trees.
- Cache quota and retention behavior.
- Protocol-version mismatch and rolling upgrades.

### Cross-platform matrix

- macOS arm64 orchestrator and worker.
- macOS x86_64 where still supported.
- Windows x86_64 worker.
- Linux x86_64 worker.
- Linux arm64 worker where hardware is available.
- Docker and Podman capability paths.
- Compatible and intentionally incompatible container-image platforms.

### Security tests

- Forged and replayed discovery or pairing messages.
- Unpaired and revoked-orchestrator commands.
- Forged rendezvous presence, stolen or expired TURN credentials, relay
  impersonation, and cross-pair routing attempts.
- Confirm relayed payloads remain end-to-end encrypted and that connectivity
  services receive no job, secret, path, or artifact contents.
- Modified snapshot chunks and artifacts.
- Absolute paths, traversal, escaping symlinks, and unsafe file types.
- Secret persistence in logs, databases, caches, crash reports, and diagnostics.
- Unauthorized local IPC clients.
- Duplicate cancellation and state-transition messages.

### Failure and chaos tests

- Orchestrator sleep and network changes.
- NAT rebinding, carrier NAT, blocked UDP, failed direct ICE checks, TURN
  failover, and connectivity-service outage.
- Packet loss and mid-stream QUIC reconnect.
- Worker shutdown, process crash, and daemon crash.
- Full disk during snapshot materialization or artifact staging.
- Local output modification while a job is running.
- Worker load or battery policy changing after a job is queued.

### Representative end-to-end scenarios

1. Run a Cargo build on a compatible Windows, Linux, or macOS target and return
   the correct platform artifact.
2. Reject a Cargo placement that would silently produce the wrong target.
3. Transfer a large FFmpeg input incrementally, stream reliable progress, and
   restore the output safely.
4. Route an Ollama request to a worker with the required model and GPU backend.
5. Explain a missing container engine, native tool, model, GPU backend, or memory
   requirement before transferring files.
6. Start a long job, close or sleep the Mac, reconnect, resume logs, and collect
   its artifacts.
7. Pair a worker on the home LAN, move the Mac to an unrelated network, connect
   directly when possible or through TURN otherwise, and complete a job without
   pairing again.

---

## 19. Launch Acceptance Criteria

ComputeHop is ready to launch when all of the following are true:

- A user can install the Mac orchestrator and at least one worker without
  manually configuring an address or SSH key.
- A discovered worker is trusted only after matching verification is confirmed
  on both devices.
- A trusted worker reconnects automatically and can be revoked from either end.
- After local pairing, a trusted worker is reachable from another network using
  a direct path when possible and an end-to-end encrypted relay path otherwise.
- A connectivity-service outage does not break local discovery, direct LAN
  execution, or jobs already accepted by workers.
- The Mac can schedule native or container background work on macOS, Windows,
  and Linux workers when their declared requirements are satisfied.
- Incompatible jobs fail before file transfer with specific reasons.
- Automatic scheduling respects device availability, resources, battery,
  thermal state, and active reservations.
- Project inputs use immutable incremental snapshots and cannot escape the
  declared root.
- Running jobs survive an orchestrator disconnect and resume observable state on
  reconnect.
- Logs are ordered and reconnectable; generic commands do not show fabricated
  progress.
- Only declared or adapter-inferred artifacts return, with local conflicts
  preserved rather than overwritten.
- Native jobs are unprivileged and restricted to managed workspaces plus
  explicitly shared paths.
- Secrets are explicit and absent from manifests, caches, and durable ComputeHop
  records.
- Cargo, FFmpeg, and Ollama each complete a documented end-to-end workflow.
- The menu-bar UI exposes pairing, devices, placement, jobs, logs, policies,
  connection path, cancellation, failures, and artifacts without requiring
  cluster terminology.

---

## 20. Product Principles

1. **Invisible setup, visible decisions.** Discovery and reconnection should be
   automatic, while trust and placement remain inspectable.
2. **Compatibility before transfer.** Do not send gigabytes to learn that a
   worker lacks the right OS, tool, image platform, model, or GPU.
3. **Safe defaults over magical guessing.** Ask for missing facts and offer to
   save them rather than silently choosing an unsafe interpretation.
4. **Durability without distributed consensus.** A single orchestrator owns
   scheduling; workers durably own jobs already entrusted to them.
5. **One job model, specialized adapters.** Discovery, trust, transfer,
   execution, logs, and artifacts stay generic; workload knowledge is modular.
6. **No hidden host mutation.** ComputeHop detects capabilities but does not silently
   install software, models, drivers, or container engines.
7. **Revocable trust.** “Pair once” means remembered until revoked, not trusted
   irrevocably forever.
8. **Connectivity does not create trust.** LAN discovery, ICE candidates, IP
   addresses, rendezvous records, and relays only provide paths; pinned paired
   identities decide who may submit or execute work.
9. **Explain failure.** The user should know what was incompatible, what failed,
   whether retry is safe, and where every artifact went.
