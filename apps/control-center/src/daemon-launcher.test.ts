const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const {
  assertDaemonRole,
  daemonCommand,
  daemonRoleFromPing,
  normalizedDeviceName,
  startDaemon
} = require("./daemon-launcher");

test("daemonCommand uses packaged daemon when available", async (t) => {
  const root = await tempDirectory(t);
  const bin = path.join(root, "bin");
  await fs.mkdir(bin);
  const daemon = path.join(bin, process.platform === "win32" ? "computehopd.exe" : "computehopd");
  await fs.writeFile(daemon, "");
  await fs.chmod(daemon, 0o755);

  const command = await daemonCommand({
    isPackaged: true,
    resourcesPath: root,
    deviceName: "Austin MacBook",
    role: "worker"
  });

  assert.equal(command.executable, daemon);
  assert.deepEqual(command.arguments, ["--role", "worker", "--device-name", "Austin MacBook"]);
});

test("daemonCommand falls back to go run in development", async (t) => {
  const root = await tempDirectory(t);
  await fs.mkdir(path.join(root, "cmd", "computehopd"), { recursive: true });
  await fs.writeFile(path.join(root, "go.mod"), "module example.test/app\n");

  const command = await daemonCommand({
    isPackaged: false,
    repoRoot: root,
    deviceName: "Dev Mac"
  });

  assert.equal(command.executable, "go");
  assert.deepEqual(command.arguments, [
    "run",
    "./cmd/computehopd",
    "--role",
    "orchestrator",
    "--device-name",
    "Dev Mac"
  ]);
  assert.equal(command.cwd, root);
});

test("normalizedDeviceName strips local suffix and supplies fallback", () => {
  assert.equal(normalizedDeviceName("Austin.local"), "Austin");
  assert.equal(normalizedDeviceName("  "), "This Mac");
});

test("startDaemon returns immediately when daemon already answers", async () => {
  const result = await startDaemon({
    role: "worker",
    client: {
      ping: async () => ({ daemonVersion: "test", role: "DEVICE_ROLE_WORKER" })
    },
    spawnDaemon: () => {
      throw new Error("spawn should not be called");
    }
  });

  assert.equal(result.ok, true);
  assert.equal(result.started, false);
  assert.equal(result.daemon.daemonVersion, "test");
});

test("startDaemon rejects an already-running daemon with the wrong role", async () => {
  await assert.rejects(
    () => startDaemon({
      role: "worker",
      client: {
        ping: async () => ({ daemonVersion: "test", role: "DEVICE_ROLE_ORCHESTRATOR" })
      },
      spawnDaemon: () => {
        throw new Error("spawn should not be called");
      }
    }),
    /already running as Control Mac/
  );
});

test("startDaemon launches and waits for ping", async (t) => {
  const root = await tempDirectory(t);
  await fs.mkdir(path.join(root, "cmd", "computehopd"), { recursive: true });
  await fs.writeFile(path.join(root, "go.mod"), "module example.test/app\n");
  let attempts = 0;
  let spawned = null;

  const result = await startDaemon({
    repoRoot: root,
    role: "worker",
    pollMs: 1,
    timeoutMs: 100,
    client: {
      ping: async () => {
        attempts += 1;
        if (attempts < 2) {
          throw new Error("not ready");
        }
        return { daemonVersion: "test", role: "DEVICE_ROLE_WORKER" };
      }
    },
    spawnDaemon: (executable, args, options) => {
      spawned = { executable, args, options };
      return {
        exitCode: null,
        unref() {}
      };
    }
  });

  assert.equal(result.ok, true);
  assert.equal(result.started, true);
  assert.equal(result.daemon.daemonVersion, "test");
  assert.equal(spawned.executable, "go");
  assert.deepEqual(spawned.args.slice(0, 2), ["run", "./cmd/computehopd"]);
  assert.deepEqual(spawned.args.slice(2), ["--role", "worker", "--device-name", normalizedDeviceName(os.hostname())]);
  assert.equal(spawned.options.detached, true);
});

test("startDaemon rejects a launched daemon with the wrong role", async (t) => {
  const root = await tempDirectory(t);
  await fs.mkdir(path.join(root, "cmd", "computehopd"), { recursive: true });
  await fs.writeFile(path.join(root, "go.mod"), "module example.test/app\n");
  let attempts = 0;

  await assert.rejects(
    () => startDaemon({
      repoRoot: root,
      role: "worker",
      pollMs: 1,
      timeoutMs: 100,
      client: {
        ping: async () => {
          attempts += 1;
          if (attempts === 1) {
            throw new Error("not running");
          }
          return { daemonVersion: "test", role: "DEVICE_ROLE_ORCHESTRATOR" };
        }
      },
      spawnDaemon: () => ({
        exitCode: null,
        unref() {}
      })
    }),
    /started as Control Mac/
  );
});

test("daemonRoleFromPing accepts protocol and domain labels", () => {
  assert.equal(daemonRoleFromPing({ role: "DEVICE_ROLE_WORKER" }), "worker");
  assert.equal(daemonRoleFromPing({ role: "DEVICE_ROLE_ORCHESTRATOR" }), "orchestrator");
  assert.equal(daemonRoleFromPing({ role: "worker" }), "worker");
  assert.equal(daemonRoleFromPing({ role: "orchestrator" }), "orchestrator");
  assert.equal(daemonRoleFromPing({ role: "" }), "");
});

test("assertDaemonRole allows unknown older daemon role values", () => {
  assert.doesNotThrow(() => assertDaemonRole({ role: "" }, "worker", "already running"));
});

async function tempDirectory(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "computehop-daemon-launcher-"));
  t.after(async () => {
    await fs.rm(root, { recursive: true, force: true });
  });
  return root;
}
