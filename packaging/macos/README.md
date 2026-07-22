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

After the VPS and DNS are ready, enable direct cross-network paths on both the
orchestrator and every worker by reinstalling with the same endpoint values:

```bash
./packaging/macos/install.sh \
  --role orchestrator \
  --device-name "My MacBook" \
  --connectivity-url https://connect.example.com \
  --stun-server stun:turn.example.com:3478
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
instead of killing it.

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
