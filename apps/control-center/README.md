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

- connects directly to the local ComputeHop daemon over the owner-only local IPC
  socket;
- reads trusted and nearby devices from the daemon;
- starts nearby-device pairing from the Devices list;
- shows active pairing verification codes and lets the user confirm or reject
  them in-app;
- forgets paired devices through daemon IPC;
- plans plain-language tasks such as "run tests", "build the app", and
  "check CI" into one safe command using local project rules;
- previews the exact command before running when Preview before running is
  enabled;
- submits selected commands as durable native jobs;
- polls daemon job logs and streams stdout/stderr into the window;
- cancels running jobs through daemon IPC;
- targets This Mac by default, or a selected connected worker;
- skips project sync for remote utility commands until a project folder is
  selected;
- stores UI-only settings in browser local storage for now.

Next step: add an optional LLM planner that can explain and compose more complex
multi-step work while keeping the same explicit command preview before
submission.
