# ComputeHop release validation evidence

This document records concrete validation runs for release-gate checklist items.
Each entry should name the exact commit, environment, commands, and artifacts so
the checklist does not depend on memory or local terminal scrollback.

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
