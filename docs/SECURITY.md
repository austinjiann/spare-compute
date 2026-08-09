# ComputeHop security model

ComputeHop lets one computer ask another computer to run background commands.
That is powerful by design. Treat a paired device like a machine you would be
willing to SSH into.

This document explains the current trust boundary in plain language. It is not
a claim that ComputeHop is sandboxed or production-hardened yet.

## Short version

- Pair only computers you own or fully control.
- Confirm the same pairing code on both devices.
- A paired control Mac can ask a worker to run commands as the local user that
  runs `computehopd`.
- Native jobs are not a security sandbox. They are normal user processes.
- Project uploads can include whatever files are inside the chosen project
  after ignore rules are applied.
- Job logs can contain secrets if the command prints them.
- The hosted/VPS connectivity service must not receive job commands, project
  files, logs, artifacts, private device keys, or pairing verification codes.
- Diagnostics bundles intentionally omit raw logs, public keys, pairing codes,
  network addresses, environment values, project files, artifacts, and database
  files, but you should still review a bundle before sharing it.

## Pairing and trusted devices

Pairing is the point where two devices decide to trust each other.

During pairing:

1. The devices exchange identity information over an encrypted connection.
2. ComputeHop shows a verification code on both devices.
3. You confirm only if the codes match exactly.
4. Each side stores the other device's public identity locally.

After that, the saved identity is used to reject impostors. A nearby device with
the same display name is not enough; it must prove possession of the paired key.

You should disconnect/revoke a device when:

- the computer is sold, wiped, stolen, or shared with someone else;
- the OS user account that runs ComputeHop is compromised;
- you no longer want that machine to run jobs for this control Mac.

## What a trusted control Mac can do

If a worker trusts a control Mac, that control Mac can submit jobs to the
worker. A job can:

- start a native process;
- run inside a container when the worker advertises Docker/Podman support;
- read files in its materialized worker workspace;
- write declared outputs;
- print stdout/stderr logs;
- use tools, network access, credentials, and local files that the worker
  process can access.

Native jobs run as the same OS user account as the worker daemon. They should
not run as root or administrator. They are isolated by workspace convention and
file-transfer rules, not by a full VM/security sandbox.

## What a trusted worker can do

A worker receives project snapshots and job specs from a paired control Mac. A
malicious or compromised worker could:

- inspect uploaded project files;
- alter command results;
- omit or falsify job logs;
- return malicious declared outputs;
- consume CPU, memory, disk, GPU, or network resources.

Only run private code on workers you control.

## Project files and ignore rules

For project jobs, ComputeHop snapshots the folder you choose. The snapshotter
respects `.gitignore` and `.computehopignore`, and skips common local secrets,
dependency folders, build outputs, and caches by default.

That default is a guardrail, not a guarantee. Before running sensitive projects
on another computer:

- keep secrets out of the project tree when possible;
- add explicit `.computehopignore` rules for private files;
- use `.env.example` or other non-secret samples instead of real `.env` files;
- check declared outputs before restoring them.

## Logs, environment, and secrets

ComputeHop does not intentionally store environment values in diagnostics
bundles. The redacted diagnostics command omits raw job logs entirely.

However, any command can print secrets itself. If a build script echoes an API
key, that value can appear in durable job logs. Avoid running commands that
print secrets, and prefer short-lived credentials for work that needs them.

## Local IPC

The CLI, macOS menu bar, and Control Center talk to the local daemon through an
owner-only local IPC socket protected by a local capability token.

That token is for local control only. It is not sent through mDNS discovery and
should not be copied to other machines.

If another process running as the same OS user can read your ComputeHop state
directory, assume it may be able to control your local daemon.

## LAN discovery

LAN discovery uses mDNS so devices with ComputeHop open can find each other on
the same network.

Discovery advertisements are hints. They are not trust. A discovered device
must still complete pairing before it can run jobs or receive project data.

mDNS can reveal that a ComputeHop device exists on the local network, along with
basic presentation and routing information. Do not run discovery on networks
where that presence is sensitive.

## Off-LAN and VPS connectivity

The VPS stack is for rendezvous and TURN relay connectivity after devices have
already been paired.

The connectivity service is designed so it does not receive job commands,
project file contents, logs, artifacts, private device keys, or long-lived
pairing identities. Pair-specific signaling payloads are encrypted end to end
between the paired devices.

The service can still observe operational metadata such as timing, IPs that
connect to the service, approximate traffic volume, and route IDs. If the
project operates a public relay, it still needs account, quota, abuse-prevention,
monitoring, incident-response, and key-rotation policy before launch.

Do not copy the coturn shared secret to client machines. Use generated
short-lived TURN credentials instead.

## Containers

Container execution is available only when a worker advertises a Docker- or
Podman-compatible Engine API.

Containers help with dependency consistency, but they are not yet ComputeHop's
public default security boundary. ComputeHop does not install Docker/Podman,
manage Linux VMs, manage GPU drivers, manage private registry credentials, or
promise container escape protection.

The public policy is documented in [`CONTAINER_POLICY.md`](CONTAINER_POLICY.md):
native execution remains the default, container jobs require an explicit
container executor request and image, and private registry credentials stay on
the worker's local Docker/Podman configuration.

## AI planner

Typed plain-language planning requires a configured OpenAI-compatible API key.
The planner receives project metadata, not raw project file contents, and must
produce one command plan. API keys should be stored through the app settings
path or provided through environment variables.

The model is not a safety boundary. Its output is locally rejected when it uses
unsupported shell syntax, dangerous or interactive commands, unsafe output
paths, missing project context, disallowed work, incompatible placement, or
reported-missing tools. The user still reviews the accepted command before it
runs, with the same command-execution risks described above.

## Diagnostics bundles

Use:

```bash
computehop diagnostics
```

The bundle includes daemon status, device summaries, pending pairing states, and
recent job metadata. It omits raw logs and applies redaction to common secret
shapes such as tokens, passwords, API keys, and credentialed URLs.

Review the zip before attaching it to an issue or sending it to another person.

## Current non-goals

ComputeHop does not currently provide:

- multi-user authorization;
- team or organization policy controls;
- mandatory access control for native jobs;
- a VM sandbox;
- malware scanning for uploaded projects or returned artifacts;
- protection from a malicious paired device;
- public relay abuse controls;
- signed/notarized public release artifacts.

These are launch and post-launch security work, not hidden features.
