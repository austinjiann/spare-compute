# ComputeHop distribution policy

This document records the current artifact, architecture, worker packaging, and
upgrade decisions.

## Artifact policy

### macOS control app

The macOS app is the primary control surface. It contains:

- the SwiftUI menu-bar app;
- the embedded Electron Control Center;
- `computehop`;
- `computehopd`;
- installer and launch-agent resources.

Current developer/private-beta artifact:

- `dist/macos/ComputeHop-macos.zip`
- `dist/macos/ComputeHop-macos.zip.sha256`

The current macOS developer archive is built for the architecture of the Mac
that produced it. It is not a universal release artifact unless the release
process explicitly builds and verifies a universal app. Public release notes
must state the supported macOS architecture for each uploaded artifact.

Public macOS distribution remains blocked until Developer ID signing,
notarization, stapling, and clean-machine validation are complete.

### Linux and Windows workers

Decision: first public worker distribution should ship archives with included
login-service installer scripts. Native MSI/PKG/DEB/RPM-style installers are
deferred until after the archive path has real clean-machine validation.

Current worker artifacts:

- `ComputeHop-worker-linux-amd64.tar.gz`
- `ComputeHop-worker-linux-arm64.tar.gz`
- `ComputeHop-worker-windows-amd64.zip`
- matching `.sha256` files

Rationale:

- workers are headless;
- early adopters need inspectable setup;
- archives are easier to verify across Linux distributions;
- the included scripts already install per-user background services without
  requiring privileged system-wide installation.

## Upgrade behavior

ComputeHop installers should be safe to rerun for the same user. Upgrades must
replace binaries and service definitions without deleting state.

State that must be preserved:

- device identity;
- trusted pairings;
- job database;
- job logs;
- content/artifact cache;
- AI planner settings and credentials;
- user-selected worker/device configuration.

Current behavior:

- macOS installer writes the app to `~/Applications/ComputeHop.app`, refreshes
  the `~/.local/bin/computehop` symlink, rewrites only the ComputeHop launch
  agent, refuses unrelated app/CLI/launch-agent targets, restarts an existing
  ComputeHop launch agent, and preserves the state directory.
- Linux worker installer copies new worker binaries into the per-user data
  directory, rewrites the `computehop-worker.service` unit, runs
  `systemctl --user daemon-reload`, enables/starts the service, and preserves
  the user state directory.
- Windows worker installer copies new worker binaries into the per-user local
  app data directory, rewrites the scheduled-task runner, registers the
  `ComputeHop Worker` scheduled task with `-Force`, starts it, and preserves the
  user state directory.

Upgrade checks before public release:

- install old artifact;
- pair at least one worker;
- run a job;
- install new artifact over the old artifact;
- confirm identity and trust are preserved;
- confirm stale service definitions are replaced;
- confirm old jobs/logs are still readable;
- confirm uninstall removes only documented binaries/services and preserves or
  removes state exactly as documented.

## Naming rule

Release notes and artifact names must be unambiguous about:

- product version;
- operating system;
- architecture;
- whether the artifact is developer/private-beta or public/notarized.

The current developer macOS archive keeps its stable build-output name because
the app planner and validation tests refer to that path. If multiple macOS
architectures are uploaded for a release, the GitHub release asset names should
include architecture even if the local build output path remains stable.
