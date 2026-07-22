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

The installer places the app in `~/Applications`, adds a safe CLI symlink at
`~/.local/bin/computehop`, and loads an unprivileged launch agent. The daemon
then starts at login in orchestrator mode and writes diagnostics to
`~/Library/Logs/ComputeHop/daemon.log`. If a manually started daemon is already
using the ComputeHop socket or UDP port, the installer asks you to stop it
instead of killing it.

To uninstall the binaries and launch agent while preserving pairings and job
history:

```bash
make uninstall-macos
```

Before public distribution, replace ad-hoc signing with Developer ID signing,
notarization, stapling, universal binaries, an upgrade-safe installer, and
clean-machine tests. Never ship the development package as a trusted release.
