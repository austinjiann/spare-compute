# macOS developer package

This directory builds a real `ComputeHop.app` containing the SwiftUI menu-bar
executable plus the `computehop` and `computehopd` Go binaries. The bundle is
ad-hoc signed for local development; it is not notarized and is not a public
release artifact.

Build and verify the bundle without installing it:

```bash
make macos-package
open dist/macos/ComputeHop.app
```

Install it for the current user:

```bash
make install-macos
```

For a named worker Mac:

```bash
./packaging/macos/install.sh --role worker --device-name "Gaming Mac"
```

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
revoked and paired again.

The installer places the app in `~/Applications`, adds a safe CLI symlink at
`~/.local/bin/computehop`, and loads an unprivileged launch agent. The daemon
then starts at login in the selected role and writes diagnostics to
`~/Library/Logs/ComputeHop/daemon.log`. If a manually started daemon is already
using the ComputeHop socket or UDP port, the installer asks you to stop it
instead of killing it. After installation, the script prints role-specific next
steps: orchestrator installs get `doctor`, `connect auto`, and smoke-test
commands; worker installs tell you what to run on the orchestrator and how to
confirm the pairing locally.

The current daemon integration attempts LAN first, then a direct ICE path using
the configured rendezvous and STUN services. The menu bar and `computehop
devices` show `Remote` and the selected path when it succeeds. Public TURN
fallback remains disabled until the hosted service has entitlement-backed,
quota-limited credential issuance.

To uninstall the binaries and launch agent while preserving pairings and job
history:

```bash
make uninstall-macos
```

Before public distribution, replace ad-hoc signing with Developer ID signing,
notarization, stapling, universal binaries, an upgrade-safe installer, and
clean-machine tests. Never ship the development package as a trusted release.
