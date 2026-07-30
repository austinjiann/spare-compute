const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const { spawn } = require("node:child_process");
const { LocalDaemonClient } = require("./local-daemon");

const repoRoot = path.resolve(__dirname, "..", "..", "..");
const DEFAULT_START_TIMEOUT_MS = 10_000;
const DEFAULT_POLL_MS = 250;

let activeStart = null;

async function startDaemon(options = {}) {
  const client = options.client || new LocalDaemonClient();
  const alreadyRunning = await ping(client);
  if (alreadyRunning) {
    return {
      ok: true,
      started: false,
      detail: "ComputeHop is already running.",
      daemon: alreadyRunning
    };
  }

  if (activeStart) {
    return activeStart;
  }

  activeStart = launchAndWait({ ...options, client }).finally(() => {
    activeStart = null;
  });
  return activeStart;
}

async function launchAndWait(options) {
  const client = options.client;
  const command = await daemonCommand(options);
  const spawnDaemon = options.spawnDaemon || spawn;
  const child = spawnDaemon(command.executable, command.arguments, {
    cwd: command.cwd,
    detached: true,
    stdio: "ignore",
    env: process.env
  });
  let spawnError = null;
  if (typeof child.once === "function") {
    child.once("error", (error) => {
      spawnError = error;
    });
  }
  if (typeof child.unref === "function") {
    child.unref();
  }

  const deadline = Date.now() + (options.timeoutMs || DEFAULT_START_TIMEOUT_MS);
  let lastError = null;
  while (Date.now() < deadline) {
    if (spawnError) {
      throw new Error(`Could not start ComputeHop: ${spawnError.message}`);
    }
    if (child.exitCode !== null && child.exitCode !== undefined) {
      break;
    }
    const daemon = await ping(client);
    if (daemon) {
      return {
        ok: true,
        started: true,
        detail: "ComputeHop started.",
        daemon,
        command: command.label
      };
    }
    lastError = "daemon did not answer yet";
    await delay(options.pollMs || DEFAULT_POLL_MS);
  }

  const exitDetail = child.exitCode === null || child.exitCode === undefined
    ? "it did not become ready in time"
    : `it exited with code ${child.exitCode}`;
  const reason = lastError ? ` (${lastError})` : "";
  throw new Error(`Started ComputeHop, but ${exitDetail}${reason}. Try the packaged installer or run computehop doctor.`);
}

async function daemonCommand(options = {}) {
  const deviceName = normalizedDeviceName(options.deviceName || os.hostname());
  const role = options.role || "orchestrator";
  const root = options.repoRoot || repoRoot;
  const resourcesPath = options.resourcesPath || process.resourcesPath || "";
  const isPackaged = Boolean(options.isPackaged);

  if (isPackaged && resourcesPath) {
    const embedded = path.join(resourcesPath, "bin", executableName("computehopd"));
    if (await executableExists(embedded)) {
      return {
        executable: embedded,
        arguments: daemonArguments(role, deviceName),
        cwd: path.dirname(embedded),
        label: `${embedded} ${daemonArguments(role, quoteForLabel(deviceName)).join(" ")}`
      };
    }
  }

  const daemonMain = path.join(root, "cmd", "computehopd");
  const goMod = path.join(root, "go.mod");
  if (await directoryExists(daemonMain) && await fileExists(goMod)) {
    const args = ["run", "./cmd/computehopd", ...daemonArguments(role, deviceName)];
    return {
      executable: "go",
      arguments: args,
      cwd: root,
      label: `go ${args.map(quoteForLabel).join(" ")}`
    };
  }

  throw new Error("Could not find a ComputeHop daemon to start. Use the macOS installer or run from the repository checkout.");
}

function daemonArguments(role, deviceName) {
  return ["--role", role, "--device-name", deviceName];
}

function normalizedDeviceName(value) {
  const trimmed = String(value || "").replace(/\.local$/i, "").trim();
  return trimmed || "This Mac";
}

async function ping(client) {
  try {
    return await client.ping();
  } catch {
    return null;
  }
}

async function fileExists(filePath) {
  try {
    return (await fs.stat(filePath)).isFile();
  } catch {
    return false;
  }
}

async function directoryExists(filePath) {
  try {
    return (await fs.stat(filePath)).isDirectory();
  } catch {
    return false;
  }
}

async function executableExists(filePath) {
  try {
    const info = await fs.stat(filePath);
    return info.isFile() && (process.platform === "win32" || (info.mode & 0o111) !== 0);
  } catch {
    return false;
  }
}

function executableName(name) {
  return process.platform === "win32" ? `${name}.exe` : name;
}

function quoteForLabel(value) {
  const text = String(value);
  if (/^[A-Za-z0-9_./:-]+$/.test(text)) {
    return text;
  }
  return `"${text.replaceAll("\\", "\\\\").replaceAll('"', '\\"')}"`;
}

function delay(ms) {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
}

module.exports = {
  daemonCommand,
  normalizedDeviceName,
  startDaemon
};
