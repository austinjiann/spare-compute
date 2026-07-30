const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const { daemonCommand, normalizedDeviceName, startDaemon } = require("./daemon-launcher");

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
    client: {
      ping: async () => ({ daemonVersion: "test" })
    },
    spawnDaemon: () => {
      throw new Error("spawn should not be called");
    }
  });

  assert.equal(result.ok, true);
  assert.equal(result.started, false);
  assert.equal(result.daemon.daemonVersion, "test");
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
        return { daemonVersion: "test" };
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

async function tempDirectory(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "computehop-daemon-launcher-"));
  t.after(async () => {
    await fs.rm(root, { recursive: true, force: true });
  });
  return root;
}
