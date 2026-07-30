const childProcess = require("node:child_process");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const { promisify } = require("node:util");

const defaultRunCommand = promisify(childProcess.execFile);
const serviceLabel = "com.computehop.daemon";

async function launchAgentStatus(options = {}) {
  const platform = options.platform || process.platform;
  if (platform !== "darwin") {
    return {
      supported: false,
      label: serviceLabel,
      status: "unsupported",
      installed: false,
      loaded: false,
      role: "",
      path: "",
      detail: "Background login service status is available on macOS."
    };
  }

  const homeDir = options.homeDir || os.homedir();
  const plistPath = launchAgentPlistPath(homeDir);
  const installed = await fileExists(plistPath, options.fs || fs);
  const config = installed ? await readConfig(plistPath, options.fs || fs) : {};
  const role = config.role || "";
  const deviceName = config.deviceName || "";
  const lanOnly = Boolean(config.lanOnly);
  const daemonPath = config.daemonPath || "";
  const expectedDaemonPath = String(options.expectedDaemonPath || "").trim();
  const needsUpdate = Boolean(installed && expectedDaemonPath && daemonPath && !samePath(daemonPath, expectedDaemonPath));
  const uid = options.uid ?? currentUID();
  const launchd = uid === "" || uid === null || uid === undefined
    ? { loaded: false, state: "", error: "Current macOS user id is unavailable." }
    : await readLaunchdState(uid, options.runCommand || defaultRunCommand);

  return {
    supported: true,
    label: serviceLabel,
    status: needsUpdate ? "needs-update" : launchd.loaded ? "loaded" : installed ? "installed-stopped" : "not-installed",
    installed,
    loaded: launchd.loaded,
    needsUpdate,
    role,
    deviceName,
    lanOnly,
    daemonPath,
    expectedDaemonPath,
    state: launchd.state,
    path: plistPath,
    detail: launchAgentDetail({
      installed,
      loaded: launchd.loaded,
      needsUpdate,
      role,
      deviceName,
      lanOnly,
      state: launchd.state
    })
  };
}

function launchAgentPlistPath(homeDir) {
  return path.join(homeDir, "Library", "LaunchAgents", `${serviceLabel}.plist`);
}

async function fileExists(filePath, fsModule) {
  try {
    await fsModule.access(filePath);
    return true;
  } catch {
    return false;
  }
}

async function readConfig(filePath, fsModule) {
  try {
    return launchAgentConfigFromPlist(await fsModule.readFile(filePath, "utf8"));
  } catch {
    return {};
  }
}

function launchAgentConfigFromPlist(contents) {
  const values = plistStringValues(contents);
  const roleFlag = values.indexOf("--role");
  const role = roleFlag >= 0 ? values[roleFlag + 1] || "" : "";
  const deviceNameFlag = values.indexOf("--device-name");
  const deviceName = deviceNameFlag >= 0 ? values[deviceNameFlag + 1] || "" : "";
  return {
    daemonPath: values[0] || "",
    role: role === "orchestrator" || role === "worker" ? role : "",
    deviceName,
    lanOnly: values.includes("--lan-only")
  };
}

function roleFromPlist(contents) {
  return launchAgentConfigFromPlist(contents).role;
}

function plistStringValues(contents) {
  return [...String(contents || "").matchAll(/<string>([^<]*)<\/string>/g)]
    .map((match) => decodePlistString(match[1]).trim());
}

function decodePlistString(value) {
  return String(value || "")
    .replaceAll("&lt;", "<")
    .replaceAll("&gt;", ">")
    .replaceAll("&amp;", "&");
}

async function readLaunchdState(uid, runCommand) {
  try {
    const result = await runCommand("launchctl", ["print", `gui/${uid}/${serviceLabel}`], { timeout: 2500 });
    const stdout = String(result?.stdout || "");
    return {
      loaded: true,
      state: launchdStateFromOutput(stdout),
      error: ""
    };
  } catch (error) {
    return {
      loaded: false,
      state: "",
      error: String(error?.message || "")
    };
  }
}

function launchdStateFromOutput(output) {
  const match = String(output || "").match(/\bstate\s*=\s*([^\n]+)/i);
  return match ? match[1].trim() : "";
}

function launchAgentDetail(status = {}) {
  if (status.needsUpdate) {
    return "Installed from an older app location. Update the background service when convenient.";
  }
  if (status.loaded) {
    const role = roleLabel(status.role);
    const name = status.deviceName ? ` named ${status.deviceName}` : "";
    const network = status.lanOnly ? " LAN-only" : "";
    const state = status.state ? ` ${status.state}.` : "";
    return `Runs at login${role ? ` as ${role}` : ""}${name}${network}.${state}`;
  }
  if (status.installed) {
    const role = roleLabel(status.role);
    const name = status.deviceName ? ` named ${status.deviceName}` : "";
    const network = status.lanOnly ? " LAN-only" : "";
    return `Installed${role ? ` as ${role}` : ""}${name}${network}, but not running right now.`;
  }
  return "Not installed for login. This app can still start ComputeHop for this session.";
}

function samePath(left, right) {
  const first = String(left || "").trim();
  const second = String(right || "").trim();
  if (!first || !second) {
    return false;
  }
  return path.resolve(first) === path.resolve(second);
}

function roleLabel(role) {
  if (role === "orchestrator") {
    return "Control Mac";
  }
  if (role === "worker") {
    return "Worker";
  }
  return "";
}

function currentUID() {
  return typeof process.getuid === "function" ? process.getuid() : "";
}

module.exports = {
  launchAgentConfigFromPlist,
  launchAgentDetail,
  launchAgentPlistPath,
  launchAgentStatus,
  roleFromPlist,
  serviceLabel
};
