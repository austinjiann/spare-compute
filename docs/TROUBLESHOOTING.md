# ComputeHop troubleshooting

Start here when ComputeHop cannot find a worker, cannot pair, cannot run a job,
or cannot restore outputs.

The fastest safe snapshot for support is:

```bash
computehop diagnostics
```

Review the generated zip before sharing it. It intentionally omits raw logs,
keys, pairing codes, network addresses, environment values, project files,
artifacts, and database files.

## Basic health checks

Run these on the computer you are using:

```bash
computehop status
computehop doctor
computehop devices
```

Use `status` to check the local daemon, `doctor` for next-step guidance, and
`devices` to inspect nearby, paired, offline, and revoked devices.

## Daemon already running

Symptom:

- Starting `computehopd` prints an “already running” or “address already in use”
  style error.
- The packaged installer says a manual daemon is already running.

What it means:

- A development daemon, packaged launch agent, or another copied app already has
  the local socket or pairing port.

What to do:

1. If you started the daemon manually in a terminal, stop it with `Ctrl-C`.
2. Check the current daemon:

   ```bash
   computehop status
   computehop doctor
   ```

3. If you want to remove the packaged launch agent and app copy:

   ```bash
   make uninstall-macos
   ```

4. If you are testing the packaged archive, rerun the installer check:

   ```bash
   make install-macos-check
   ```

Avoid running `go run ./cmd/computehopd`, the menu-bar app, and an installed
launch agent for the same role at the same time.

## Duplicate devices

Symptom:

- `computehop devices` shows multiple rows with the same name.
- `--on auto` refuses to choose a worker.
- The UI shows an offline trusted worker plus another similar nearby worker.

What it means:

- More than one device has the same display name, or one worker was reinstalled
  and now has a new device identity.

What to do:

1. Prefer a short device ID instead of a name:

   ```bash
   computehop devices
   computehop run --on <short-id> --no-project hostname
   ```

2. Rename one worker when starting or installing it:

   ```bash
   computehop setup worker --device-name "Gaming PC"
   ```

3. Disconnect stale trusted devices from the Control Center or CLI:

   ```bash
   computehop disconnect <device>
   ```

4. Pair the intended nearby worker again:

   ```bash
   computehop connect nearby
   computehop connect confirm
   ```

## Pairing code mismatch

Symptom:

- The pairing code on the control Mac does not match the worker.

What it means:

- You may be seeing a different device, stale pairing session, or network
  interference. Do not confirm the pairing.

What to do:

1. Reject or let the pairing expire.
2. Refresh devices:

   ```bash
   computehop devices
   ```

3. Start a new pairing:

   ```bash
   computehop connect nearby
   ```

4. Confirm only when both devices show the exact same code:

   ```bash
   computehop connect confirm
   ```

If codes still do not match, disconnect any stale trusted row and pair again.

## Worker offline

Symptom:

- A paired worker appears as offline.
- A run fails with “paired worker is unavailable”.
- The menu says the worker is LAN-only or remote access is off.

What it means:

- The worker daemon is not running, LAN discovery cannot see it, or off-LAN
  connectivity is not configured/validated for that pair.

What to do:

1. On the worker, check:

   ```bash
   computehop status
   computehop doctor
   ```

2. Put both devices on the same LAN and refresh:

   ```bash
   computehop devices
   ```

3. If you installed with `--lan-only`, remote/VPS connectivity is intentionally
   disabled. Reinstall without `--lan-only` only after the VPS stack is ready.
4. Test with a utility job:

   ```bash
   computehop smoke --on auto
   ```

5. If the worker was reinstalled or renamed, disconnect the old trusted row and
   pair again.

## Missing tools

Symptom:

- A planned task says the selected worker does not report Go, Node, Docker,
  Swift, Python, FFmpeg, Ollama, or another required tool.
- A remote run is rejected before project upload.

What it means:

- ComputeHop detected that the chosen worker probably cannot run the requested
  command. This is intentional; it avoids uploading a project to a machine that
  already reports a missing dependency.

What to do:

1. Run `computehop devices` and check the worker capability hints.
2. Install the missing tool on the worker using that platform's normal installer.
3. Restart or refresh ComputeHop on the worker so capabilities are advertised.
4. Select a different worker or run locally.

ComputeHop does not install Go, Node, Docker/Podman, GPU drivers, models, or
other workload tools for you.

## Project upload failure

Symptom:

- A remote project job fails while snapshotting or transferring.
- The UI stays in preparation/upload and then reports a failure.

What it means:

- ComputeHop could not read, snapshot, or upload the selected project folder.

What to do:

1. Confirm the selected project folder still exists and is readable.
2. Check ignore rules:

   ```bash
   git status --ignored
   ```

3. Remove very large generated folders from the project or add them to
   `.computehopignore`.
4. Keep secrets out of the project tree; use `.env.example` rather than real
   `.env` files.
5. Retry with a small utility command first:

   ```bash
   computehop run --on auto --no-project hostname
   ```

If utility jobs work but project jobs fail, the issue is likely snapshot or
file-transfer specific.

## Output restore conflicts

Symptom:

- A job succeeds, but output restore reports conflicts.
- Returned files are staged with conflict names instead of overwriting local
  files.

What it means:

- ComputeHop found existing local files where declared outputs would be restored.
  It preserves local files instead of overwriting them.

What to do:

1. Inspect the conflict files and generated restored files.
2. Move or delete the local file only if you are sure it is safe.
3. Retry output restore:

   ```bash
   computehop outputs <job-id>
   ```

For future jobs, declare a clean output path such as `dist`, `out`, or a
dedicated report folder.

## CI blocked by GitHub Actions billing

Symptom:

- GitHub Actions fails before runner startup.
- Local `make pr-check` passes, but GitHub does not run the workflow.

What it means:

- This is usually repository/account billing, Actions allowance, or spending
  limit state. It is external to the code path.

What to do:

1. Check the repository Actions tab and billing/spending limit settings.
2. Make the repository public if you want GitHub-hosted public-repo Actions
   behavior.
3. Rerun the workflow after billing/Actions access is fixed.
4. Until then, validate locally:

   ```bash
   make pr-check
   make release-check
   ```

## When to disconnect and reconnect

Disconnect a trusted worker when:

- the worker was reinstalled and now has a new identity;
- the worker name is duplicated and you are not sure which row is current;
- pairing or routing state appears stale;
- you no longer trust that computer.

Use the Control Center **Disconnect** action or:

```bash
computehop disconnect <device>
```

Then put both devices on the same LAN and pair again.

