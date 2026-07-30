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
- runs selected commands through `computehop run --follow`;
- streams stdout/stderr into the window while the command is running;
- lets the user cancel the durable job when its submitted job ID has been
  observed, falling back to stopping the local follow process;
- targets This Mac by default, or a selected connected worker;
- uses `--no-project` for remote utility commands until a project folder is
  selected;
- stores UI-only settings in browser local storage for now.

Next step: replace CLI shell-out with daemon-native IPC for device discovery,
job submission, cancellation, log streaming, project selection, and persisted
settings.
