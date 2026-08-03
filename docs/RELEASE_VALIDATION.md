# ComputeHop release validation evidence

This document records concrete validation runs for release-gate checklist items.
Each entry should name the exact commit, environment, commands, and artifacts so
the checklist does not depend on memory or local terminal scrollback.

Use [physical validation](PHYSICAL_VALIDATION.md) and
`computehop setup launch` for real-machine gates. Keep failed or partial runs in
this document as evidence; do not check launch gates until a passing run is
recorded.

## Physical validation entry template

```text
## YYYY-MM-DD validation name

Commit:
- <full commit SHA>

Artifacts:
- <filename> — SHA-256 <checksum>

Machines:
- Control: <OS/version/arch/device name>
- Worker: <OS/version/arch/device name>

Commands:
- <exact command>

Result:
- Passed / Failed / Partial

Evidence:
- <status/devices/jobs/logs/output snippets or paths>

Notes:
- <blockers, retries, redactions>
```

## 2026-08-03 local launch validation command

Commit validated:

```text
1d231f965a793d70a577dbe3e370dc2109d47df8
```

Environment:

- macOS 26.4, Darwin 25.4.0, arm64
- Go 1.26.5 darwin/arm64
- Node.js 26.5.0
- Swift 6.3.3, arm64-apple-macosx26.0 target
- Docker 29.4.3

Command:

```bash
make launch-local-validation
```

Coverage:

- CLI device, setup, connect, disconnect, doctor, and run guidance for duplicate
  devices, stale/offline devices, first-run setup, and recovery messages;
- project snapshot `.gitignore` and `.computehopignore` behavior;
- default exclusions for `.env`, private keys, dependency folders, build output
  folders, and caches;
- declared output path safety and conflict-prone reserved path rejection;
- remote pre-submit rejection when selected workers report missing tools or an
  unsupported executor before project upload;
- Control Center device mapping, worker readiness, missing-tool messaging,
  launch-agent setup behavior, task planning, output validation, and generated
  setup-guide screenshots;
- Control Center npm dependency audit.

This command supports launch readiness and keeps evidence reproducible. It does
not replace physical packaged-app validation on separate machines.

## 2026-08-03 merged-main release check

Commit validated:

```text
1d231f965a793d70a577dbe3e370dc2109d47df8
```

Environment:

- macOS 26.4, Darwin 25.4.0, arm64
- Go 1.26.5 darwin/arm64
- Node.js 26.5.0
- Swift 6.3.3, arm64-apple-macosx26.0 target
- Docker 29.4.3

Command:

```bash
make release-check
```

Result:

- source checks passed;
- macOS archive smoke passed;
- worker archive smoke passed;
- Linux and Windows worker archives were produced and verified.

Artifacts present after the run:

| Artifact | SHA-256 |
| --- | --- |
| `dist/macos/ComputeHop-macos.zip` | `728296546d65370ba9af0bc5d8b6b4e8e4b1dd4e12aff27b2f136df35caca082` |
| `dist/workers/ComputeHop-worker-linux-amd64.tar.gz` | `190455c39eae5f60bf8f4df4e8a37c01ef463da0c0d8628b0f04014bb713c83c` |
| `dist/workers/ComputeHop-worker-linux-arm64.tar.gz` | `41904911ef4f5794aab94ca54239c37a61ace3ed338264b27d116d82391bc791` |
| `dist/workers/ComputeHop-worker-windows-amd64.zip` | `c3557c62f57e95d82984932fc695f40edcf82148082e386417489df3af2a384c` |

Notes:

- this was run from the merged `main` checkout after local launch validation was
  added;
- this validation does not install artifacts, pair physical machines, exercise
  Linux/Windows machines, prove off-LAN connectivity, or cover signing and
  notarization.

## 2026-08-03 macOS release-signing automation

Validation source:

- branch: `agent/macos-release-signing`
- unsigned/ad-hoc development path remains the default;
- release archive path is gated by `COMPUTEHOP_CODESIGN_IDENTITY` and Apple
  notarization credentials.

Environment:

- macOS 26.4, Darwin 25.4.0, arm64
- Go 1.26.5 darwin/arm64
- Node.js 26.5.0
- Swift 6.3.3, arm64-apple-macosx26.0 target
- Docker 29.4.3

Commands:

```bash
make macos-package-check
make macos-archive-smoke
packaging/macos/release-archive.sh # expected to fail without signing identity
make pr-check
```

Result:

- shell syntax and plist validation passed for the new signing/notarization
  scripts and entitlements file;
- macOS archive smoke passed with the added archive support files;
- `packaging/macos/release-archive.sh` failed closed before building when
  `COMPUTEHOP_CODESIGN_IDENTITY` was missing;
- the full PR gate passed.

Notes:

- this validation does not prove a real Developer ID signature, Apple
  notarization submission, stapling, Gatekeeper assessment, or clean-machine
  install because the required Apple credentials are not present in the
  developer checkout.

## 2026-08-03 packaged macOS installed-state validator

Validation source:

- branch: `agent/macos-installed-validator`;
- validator is bundled into the macOS archive next to `install.sh`.

Environment:

- macOS 26.4, Darwin 25.4.0, arm64
- Go 1.26.5 darwin/arm64
- Node.js 26.5.0
- Swift 6.3.3, arm64-apple-macosx26.0 target
- Docker 29.4.3

Commands:

```bash
make macos-package-check
make macos-archive-smoke
make pr-check
```

Expected validator coverage after a real install:

- installed app bundle verification using the package verifier;
- `~/.local/bin/computehop` symlink target and executability;
- per-user LaunchAgent plist label, daemon path, role, device name, and
  LAN-only/remote mode;
- launchd loaded-state check;
- packaged CLI version, daemon status, and doctor checks;
- optional local `hostname` smoke job for a control-Mac install.

Notes:

- this validation proves that the validator is syntactically checked and bundled
  by archive smoke; it does not prove a physical packaged install until the
  validator is run on an installed artifact.

## 2026-08-03 worker installed-state validators

Validation source:

- branch: `agent/worker-installed-validators`;
- Linux and Windows validators are bundled into worker archives next to the
  installer scripts.

Environment:

- macOS 26.4, Darwin 25.4.0, arm64
- Go 1.26.5 darwin/arm64
- Node.js 26.5.0
- Docker 29.4.3

Commands:

```bash
make worker-archives-check
make pr-check
```

Expected validator coverage after real worker installs:

- copied worker CLI/daemon/runner files;
- Linux systemd user service or Windows scheduled task definition;
- login service enabled/running state;
- expected worker device name and LAN-only/remote mode;
- packaged CLI version, daemon status, and doctor checks.

Notes:

- Linux validator behavior is covered by the worker smoke test using a fake
  systemd environment and fake installed worker files;
- Windows validator is bundled and structurally verified from macOS, but must be
  run on a real Windows worker before checking the Windows physical validation
  gates.

## 2026-08-03 macOS uninstall dry-run validation

Validation source:

- branch: `agent/macos-uninstall-check`;
- the macOS archive bundles `uninstall.sh` next to `install.sh`;
- archive smoke exercises `uninstall.sh --check` against an isolated fake
  installed app, CLI link, and LaunchAgent.

Environment:

- macOS 26.4, Darwin 25.4.0, arm64
- Go 1.26.5 darwin/arm64
- Node.js 26.5.0
- Swift 6.3.3, arm64-apple-macosx26.0 target

Commands:

```bash
packaging/macos/uninstall.sh --check
make uninstall-macos-check
make macos-archive-smoke
make pr-check
```

Result:

- standalone uninstall dry-run passed without touching the current account;
- macOS archive smoke verified the copied archive includes `uninstall.sh`;
- smoke created a fake installed app, CLI symlink, and LaunchAgent, then
  confirmed `uninstall.sh --check` reported the expected removals while leaving
  all fake installed files intact.

Notes:

- this proves the non-destructive validation path and archive contents;
- a real clean-machine uninstall still must be run before checking the full
  clean-machine release-artifact gate.

## 2026-08-03 packaged macOS control install and uninstall validation

Validation source:

- branch: `agent/macos-control-install-evidence`;
- archive built from `main` at `7ea23aa`;
- installed from an extracted `dist/macos/ComputeHop-macos.zip`, not from a
  source-tree `go run` or `swift run` process.

Environment:

- macOS 26.4, Darwin 25.4.0, arm64
- Go 1.26.5 darwin/arm64
- Node.js 26.5.0
- Swift 6.3.3, arm64-apple-macosx26.0 target

Commands:

```bash
make install-macos-check
make uninstall-macos-check
make macos-archive
make macos-archive-smoke
shasum -a 256 -c dist/macos/ComputeHop-macos.zip.sha256
./install.sh --role orchestrator --no-open
./validate-installed.sh --role orchestrator --run-local-smoke
./uninstall.sh --check
computehop status
computehop doctor
make uninstall-macos
./install.sh --role orchestrator --no-open
./validate-installed.sh --role orchestrator --run-local-smoke
```

Installed artifact:

| Artifact | SHA-256 |
| --- | --- |
| `dist/macos/ComputeHop-macos.zip` | `9425779b688611e92fe90a85cd4430b945d144d641ff036fddc449b77f1f7848` |

Result:

- install dry-run passed after stopping the previous development daemon;
- copied archive checksum verification passed;
- packaged archive installed the control-Mac app to
  `~/Applications/ComputeHop.app`;
- packaged archive installed the CLI link at `~/.local/bin/computehop`;
- packaged archive installed and loaded
  `~/Library/LaunchAgents/com.computehop.daemon.plist`;
- launchd reported the daemon running from
  `~/Applications/ComputeHop.app/Contents/Resources/bin/computehopd` with
  `--role orchestrator`;
- `computehop status` and `computehop doctor` reported
  `computehopd 0.1.0 ready`, LAN discovery available, and role
  `orchestrator`;
- installed-state validation passed with `--run-local-smoke`;
- uninstall check reported only the documented app, CLI link, and LaunchAgent
  removals while preserving durable state;
- actual uninstall removed the documented app, CLI link, and LaunchAgent,
  unloaded the launchd service, and preserved
  `~/Library/Application Support/ComputeHop/computehop.db`;
- the packaged archive was reinstalled and revalidated as the final local
  state.

Notes:

- this validates the packaged control-Mac install path on the current macOS
  account; it is not a clean-machine release validation;
- this does not validate the packaged worker Mac, two-Mac pairing, remote
  packaged jobs, Linux/Windows physical installs, signing/notarization, or
  off-LAN connectivity.

## 2026-08-03 clean-checkout artifact build

Commit validated:

```text
eca9ac6d5e55c39d603877e7c767ad784263ef52
```

Environment:

- macOS 26.4, Darwin 25.4.0, arm64
- Go 1.26.5 darwin/arm64
- Node.js 26.5.0
- Swift 6.3.3, arm64-apple-macosx26.0 target
- Docker 29.4.3

Validation source:

- fresh clone of `/Users/austinphoebe/spare-compute`
- checked out `main`
- no committed or ignored build outputs copied into the clone

Commands:

```bash
make release-check
make macos-archive
```

Result:

- source checks passed;
- macOS archive smoke passed;
- worker archive smoke passed;
- macOS developer archive was produced;
- Linux and Windows worker archives were produced and verified.

Artifacts produced:

| Artifact | SHA-256 |
| --- | --- |
| `dist/macos/ComputeHop-macos.zip` | `d6e16f2c983e0e9314835de743bd3cd6b6ef2be7ca0f88f05021dce0b25f84c7` |
| `dist/workers/ComputeHop-worker-linux-amd64.tar.gz` | `10d83d96df326949ee104ed39c75275d914e87bc26278a5d15e2aac725ab6af5` |
| `dist/workers/ComputeHop-worker-linux-arm64.tar.gz` | `f23a36fdbb2ef03ba502c8a5527ed74e32620152710e1a037ccee74c8ab3d9fa` |
| `dist/workers/ComputeHop-worker-windows-amd64.zip` | `d1f185eb97ed553771cc57d98e4b1bce2cfd485c0eb529c5dcdcec0da852072e` |

Notes:

- the macOS archive is an ad-hoc signed developer/private-beta artifact, not a
  notarized public artifact;
- the macOS archive was built on arm64 and should be documented as arm64 unless
  a separate universal build is produced and validated;
- this validation does not cover installing the artifacts on clean machines,
  pairing devices, running remote jobs from packaged installs, signing,
  notarization, or VPS/off-LAN behavior.
