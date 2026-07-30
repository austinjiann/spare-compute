# Worker developer packages

These packages let a Mac orchestrator control Linux and Windows worker machines
without asking every worker to build from source. They are development packages,
not signed production installers.

Build worker archives from the repo:

```bash
make worker-archives
```

Outputs are written to `dist/workers/`:

- `ComputeHop-worker-linux-amd64.tar.gz`
- `ComputeHop-worker-linux-arm64.tar.gz`
- `ComputeHop-worker-windows-amd64.zip`
- matching `.sha256` files

Override the target list when needed:

```bash
COMPUTEHOP_WORKER_TARGETS="linux/amd64 windows/amd64" make worker-archives
```

## Linux worker

Copy the matching `.tar.gz` and `.sha256` to the Linux machine:

```bash
shasum -a 256 -c ComputeHop-worker-linux-amd64.tar.gz.sha256
# Or on Linux systems without shasum:
# sha256sum -c ComputeHop-worker-linux-amd64.tar.gz.sha256
tar -xzf ComputeHop-worker-linux-amd64.tar.gz
cd ComputeHop-worker-linux-amd64
./run-worker.sh --lan-only
```

For a per-user systemd service:

```bash
COMPUTEHOP_DEVICE_NAME="Gaming PC" ./install-systemd-user.sh
```

## Windows worker

Copy the `.zip` and `.sha256` to the Windows machine, then in PowerShell:

```powershell
Get-FileHash .\ComputeHop-worker-windows-amd64.zip -Algorithm SHA256
Expand-Archive .\ComputeHop-worker-windows-amd64.zip .
cd .\ComputeHop-worker-windows-amd64
.\run-worker.ps1 -DeviceName "Gaming PC" --lan-only
```

For a per-user scheduled task:

```powershell
.\install-scheduled-task.ps1 -DeviceName "Gaming PC"
```

## Pair from the Mac

With the worker running on the same LAN:

```bash
computehop connect nearby
computehop connect confirm
computehop smoke
```

On the worker, confirm the same pairing code with:

```bash
./bin/computehop connect confirm
```

After `computehop smoke` prints the worker hostname, remote job submission is
working from the Mac orchestrator.
