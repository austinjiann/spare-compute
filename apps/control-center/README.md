# ComputeHop Control Center

This is the larger settings surface for ComputeHop. The macOS menu bar should
stay small: status, device picker, and quick task entry. Device sync, allowed
work, project sync, relay settings, and future AI planner settings belong here.

Run it in development:

```bash
cd apps/control-center
npm install
npm run dev
```

Current scope:

- reads devices from `computehop devices` when the CLI is installed;
- falls back to `go run ./cmd/computehop devices` from the repo during local dev;
- stores UI-only settings in browser local storage for now.

Next step: persist these settings through the Go daemon instead of keeping them
only in the Electron renderer.
