# macOS developer package

This directory builds a real `ComputeHop.app` containing the SwiftUI menu-bar
executable, an embedded Electron Control Center app, and the `computehop` and
`computehopd` Go binaries. The bundle is ad-hoc signed for local development;
it is not notarized and is not a public release artifact.

Build and verify the bundle without installing it:

```bash
make macos-package
open dist/macos/ComputeHop.app
```

Build a copyable developer archive for another Mac:

```bash
make macos-archive
```

This writes `dist/macos/ComputeHop-macos.zip` and
`dist/macos/ComputeHop-macos.zip.sha256`. Copy both files to the other Mac,
then expand the archive and install from the included helper:

```bash
shasum -a 256 -c ComputeHop-macos.zip.sha256
ditto -x -k ComputeHop-macos.zip .
cd ComputeHop-macos
./install.sh --check --role worker --device-name "Gaming Mac" --lan-only
./install.sh --role worker --device-name "Gaming Mac" --lan-only
./validate-installed.sh --role worker --device-name "Gaming Mac" --lan-only
```

The bundle verifier checks the Swift app, embedded Control Center, embedded CLI
and daemon binaries, ad-hoc signature, version commands, the launch-agent
template that the installer rewrites for the selected role, and the embedded
Control Center's background-service resolver. That resolver must prefer the
parent `ComputeHop.app` daemon over the nested Control Center daemon before the
package is accepted when Node is available. Archives are verified before they
are written, so worker Macs can install the copied app without needing the full
developer toolchain.

The current developer archive is built for the architecture of the Mac that
created it. Public release notes must identify the supported architecture, or a
separate universal-app build must be created and validated.

Build a signed and notarized release-candidate archive:

```bash
COMPUTEHOP_CODESIGN_IDENTITY="Developer ID Application: Your Name (TEAMID)" \
COMPUTEHOP_NOTARY_KEYCHAIN_PROFILE="computehop-notary" \
COMPUTEHOP_BUILD_NUMBER=1 \
make macos-release-archive
```

`macos-release-archive` signs with hardened runtime, submits the app to Apple's
notary service, staples the ticket, verifies the stapled app, then archives it.
Instead of `COMPUTEHOP_NOTARY_KEYCHAIN_PROFILE`, CI can provide
`COMPUTEHOP_NOTARY_APPLE_ID`, `COMPUTEHOP_NOTARY_TEAM_ID`, and
`COMPUTEHOP_NOTARY_PASSWORD`. Signed and notarized artifacts still require the
clean-machine validation matrix before they are public release artifacts.

Run a non-mutating archive smoke test:

```bash
make macos-archive-smoke
```

The smoke target builds the copyable archive in a temporary output directory,
checks its SHA-256 file, extracts it, verifies the copied app, confirms the
embedded CLI and daemons answer version checks, and runs isolated `install.sh
--check` dry-runs for both Control Mac and Worker LAN-only installs. It does not
write to `~/Applications`, `~/.local/bin`, or `~/Library/LaunchAgents`.
It also exercises `uninstall.sh --check` against an isolated fake install and
verifies that the dry-run leaves the fake app, CLI link, and LaunchAgent intact.

Check the installer path without changing the current user account:

```bash
make install-macos-check
```

`install.sh --check` builds and verifies the app in a temporary directory,
validates the selected role/connectivity flags, rejects unrelated existing app,
CLI, or LaunchAgent targets, renders and validates the rewritten LaunchAgent in
the temporary directory, including device-name, LAN-only, cache-size, and
remote-connectivity arguments, and prints what would be installed. It does not
copy into `~/Applications`, touch `~/.local/bin`, write
`~/Library/LaunchAgents`, restart launchd, or open the app.

Install it for the current user:

```bash
make install-macos
```

Validate an installed package:

```bash
./packaging/macos/validate-installed.sh --role orchestrator
./packaging/macos/validate-installed.sh --role orchestrator --run-local-smoke
```

From an extracted archive, run the bundled validator next to `install.sh`:

```bash
./validate-installed.sh --role worker --device-name "Gaming PC" --lan-only
```

The validator checks the installed app bundle, CLI symlink, LaunchAgent plist,
loaded launchd service, daemon status, and doctor output. `--run-local-smoke`
adds a local `hostname` job and is intended for control-Mac validation.

Print the full two-Mac LAN package smoke checklist:

```bash
computehop setup smoke
```

For a named worker Mac:

```bash
./packaging/macos/install.sh --role worker --device-name "Gaming Mac"
```

If you already built or copied a `ComputeHop.app` bundle and this installer
script is available, pass the app explicitly to avoid rebuilding from the
checkout:

```bash
./packaging/macos/install.sh --app /path/to/ComputeHop.app --check --role worker --device-name "Gaming Mac"
./packaging/macos/install.sh --app /path/to/ComputeHop.app --role worker --device-name "Gaming Mac"
```

When `install.sh` sits next to a copied `ComputeHop.app`, as it does inside the
developer archive, `--app` is optional.

The daemon keeps a verified content cache for project chunks and returned
artifacts. It defaults to 20GiB and can be tuned during install:

```bash
./packaging/macos/install.sh --cache-size 40GiB
```

After the VPS and DNS are ready, enable direct cross-network paths on both the
orchestrator and every worker by printing the exact installer command for each
Mac and running the command it prints:

```bash
computehop setup orchestrator \
  --connectivity-domain connect.example.com \
  --turn-domain turn.example.com

computehop setup worker \
  --device-name "Gaming Mac" \
  --connectivity-domain connect.example.com \
  --turn-domain turn.example.com
```

For forced-relay testing with the one-VPS stack, generate short-lived TURN
credentials on the VPS and use the printed `computehop setup ...` or direct
installer commands:

```bash
cd deploy/vps
./turn-credentials.sh
```

Use `--role worker` on worker Macs. Reinstalling preserves the state directory,
pairings, and job history. Pair devices once on the same LAN before separating
their networks; old pairings created before connectivity-secret support must be
disconnected and connected again.

The installer places the app in `~/Applications`, adds a safe CLI symlink at
`~/.local/bin/computehop`, and loads an unprivileged launch agent. The daemon
then starts at login in the selected role and writes diagnostics to
`~/Library/Logs/ComputeHop/daemon.log`. Before bootstrapping launchd, the
installer validates the rewritten launch-agent label, daemon path, selected
role, device-name, LAN-only, cache-size, connectivity flags, log paths, and
working directory. If a manually started daemon is already using the ComputeHop
socket or UDP port, including a daemon from a different development build, the
installer asks you to stop it instead of killing it.
After installation, the script prints role-specific next steps: orchestrator
installs get `doctor`, `connect nearby`, and smoke-test commands; worker
installs tell you what to run on the orchestrator and how to confirm the pairing
locally. Pass `--lan-only` when you want the launch agent to ignore hosted
rendezvous, ICE, and TURN settings and use same-LAN discovery only.

The current daemon integration attempts LAN first, then a direct ICE path using
the configured rendezvous and STUN services unless installed with `--lan-only`.
The menu bar and `computehop devices` show `Remote` and the selected path when
it succeeds. Public TURN fallback remains disabled until the hosted service has
entitlement-backed, quota-limited credential issuance.

To uninstall the binaries and launch agent while preserving pairings and job
history:

```bash
make uninstall-macos-check
make uninstall-macos
```

Before public distribution, replace ad-hoc signing with Developer ID signing,
notarization, stapling, universal binaries, an upgrade-safe installer, and
clean-machine tests. Never ship the development package as a trusted release.
