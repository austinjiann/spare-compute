# ComputeHop

> Run background jobs on your own computers without SSH.

ComputeHop turns the machines you own into a small personal compute pool. The
target experience is simple: install the app, connect a nearby worker once, then
run commands from your Mac while ComputeHop handles discovery, transfer, logs,
and results.

This is still a development build. The current priority is making the local Mac
orchestrator + nearby worker path boring and reliable before adding hosted
internet connectivity.

## What works now

- Go daemon and CLI.
- macOS SwiftUI menu-bar app.
- LAN discovery with mDNS.
- Two-device pairing with verification codes and revocable trust.
- Remote native command execution on a paired reachable worker.
- Durable job state, reconnectable logs, cancellation, and restart recovery.
- Incremental project snapshots and declared output/artifact restore.
- Same-LAN-only setup mode.
- One-VPS rendezvous/STUN/TURN deployment scaffolding for later remote-network
  testing.

## Not launch-ready yet

- CI is currently blocked by GitHub account billing/spending limits for private
  repo Actions.
- The VPS path still needs a real host, DNS, firewall validation, and
  forced-relay testing.
- Windows and Linux workers are planned but still need physical validation.
- The macOS app bundle is ad-hoc signed for development, not notarized for
  public release.
- Scheduling is still basic; explicit worker selection and `--on auto` are the
  reliable paths.

## Quickstart for local development

Run an orchestrator daemon on your Mac:

```bash
go run ./cmd/computehopd --role orchestrator --device-name "My Mac"
```

Run a worker daemon on another computer on the same LAN:

```bash
go run ./cmd/computehopd --role worker --device-name "Gaming PC"
```

From the orchestrator Mac, connect and test:

```bash
go run ./cmd/computehop devices
go run ./cmd/computehop connect nearby
go run ./cmd/computehop connect confirm
go run ./cmd/computehop smoke
go run ./cmd/computehop run --on auto --no-project hostname
```

Run `connect confirm` on both devices after comparing the exact verification
code. Use `go run ./cmd/computehop connect` with no arguments if you forget the
next step; it prints the active pairing request or the next setup action.

To run a project-backed command from the orchestrator Mac:

```bash
go run ./cmd/computehop run --on auto -C /path/to/project cargo test
```

To declare and fetch outputs:

```bash
go run ./cmd/computehop run --on auto -C /path/to/project -o target/release/my-app --follow --get cargo build --release
```

## macOS menu bar

Start the daemon first, then launch the development menu app:

```bash
swift run ComputeHop
```

The menu app is presentation-only. Jobs, pairing, networking, logs, and artifact
state stay in the Go daemon, so closing the menu does not stop accepted work.

To build the development app bundle:

```bash
make macos-package
open dist/macos/ComputeHop.app
```

See [`apps/macos/README.md`](apps/macos/README.md) and
[`packaging/macos/README.md`](packaging/macos/README.md) for the current macOS
boundary.

## VPS path

You do not need a VPS for the first local product path. Use LAN pairing and
remote execution first.

When ready to test unrelated networks, the one-VPS staging stack lives in
[`deploy/vps`](deploy/vps):

```bash
go run ./cmd/computehop setup vps \
  --connectivity-domain connect.example.com \
  --turn-domain turn.example.com \
  --email admin@example.com \
  --public-ip 203.0.113.10
```

The VPS is only for rendezvous, STUN, and encrypted relay traffic. It is not a
hosted scheduler and it cannot read job commands, files, logs, or artifacts.

## Repository layout

- `cmd/` — Go binaries: CLI, daemon, and hosted connectivity service.
- `api/` — versioned Protocol Buffer contracts.
- `gen/` — generated Go and Swift protocol code.
- `internal/` — private Go application and infrastructure packages.
- `apps/macos/` — SwiftUI menu-bar app.
- `deploy/` — hosted connectivity deployment configuration.
- `packaging/` — native app/installer packaging.
- `docs/PLAN.md` — full product, architecture, security, and launch plan.

## Checks

Useful local checks:

```bash
go test ./...
swift test
swift build
make deploy-check
make macos-package-check
git diff --check
```

Full validation before launch still requires physical multi-machine testing:
discovery, pairing, restart recovery, remote execution, logs, cancellation,
artifact restore, and forced-relay behavior after the VPS is live.
