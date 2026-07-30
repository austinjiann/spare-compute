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
  const role = installed ? await readRole(plistPath, options.fs || fs) : "";
  const uid = options.uid ?? currentUID();
  const launchd = uid === "" || uid === null || uid === undefined
    ? { loaded: false, state: "", error: "Current macOS user id is unavailable." }
    : await readLaunchdState(uid, options.runCommand || defaultRunCommand);

  return {
    supported: true,
    label: serviceLabel,
    status: launchd.loaded ? "loaded" : installed ? "installed-stopped" : "not-installed",
    installed,
    loaded: launchd.loaded,
    role,
    state: launchd.state,
    path: plistPath,
    detail: launchAgentDetail({ installed, loaded: launchd.loaded, role, state: launchd.state })
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

async function readRole(filePath, fsModule) {
  try {
    return roleFromPlist(await fsModule.readFile(filePath, "utf8"));
  } catch {
    return "";
  }
}

function roleFromPlist(contents) {
  const values = [...String(contents || "").matchAll(/<string>([^<]*)<\/string>/g)]
    .map((match) => decodePlistString(match[1]).trim());
  const roleFlag = values.indexOf("--role");
  if (roleFlag < 0) {
    return "";
  }
  const role = values[roleFlag + 1] || "";
  return role === "orchestrator" || role === "worker" ? role : "";
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
  if (status.loaded) {
    const role = roleLabel(status.role);
    const state = status.state ? ` ${status.state}.` : "";
    return `Runs at login${role ? ` as ${role}` : ""}.${state}`;
  }
  if (status.installed) {
    const role = roleLabel(status.role);
    return `Installed${role ? ` as ${role}` : ""}, but not running right now.`;
  }
  return "Not installed for login. This app can still start ComputeHop for this session.";
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
  launchAgentDetail,
  launchAgentPlistPath,
  launchAgentStatus,
  roleFromPlist,
  serviceLabel
};
