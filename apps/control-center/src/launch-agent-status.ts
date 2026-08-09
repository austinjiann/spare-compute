const childProcess = require("node:child_process");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const { promisify } = require("node:util");

const defaultRunCommand = promisify(childProcess.execFile);
const serviceLabel = "com.computehop.daemon";

async function launchAgentStatus(options: any = {}) {
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
  const config: any = installed ? await readConfig(plistPath, options.fs || fs) : {};
  const role = config.role || "";
  const deviceName = config.deviceName || "";
  const lanOnly = Boolean(config.lanOnly);
  const daemonPath = config.daemonPath || "";
  const expectedDaemonPath = String(options.expectedDaemonPath || "").trim();
  const expectedRole = expectedRoleValue(options.expectedRole);
  const expectedDeviceName = String(options.expectedDeviceName || "").trim();
  const hasExpectedLanOnly = typeof options.expectedLanOnly === "boolean";
  const daemonPathNeedsUpdate = Boolean(installed && expectedDaemonPath && daemonPath && !samePath(daemonPath, expectedDaemonPath));
  const roleNeedsUpdate = Boolean(installed && expectedRole && role !== expectedRole);
  const deviceNameNeedsUpdate = Boolean(installed && expectedDeviceName && deviceName !== expectedDeviceName);
  const lanOnlyNeedsUpdate = Boolean(installed && hasExpectedLanOnly && lanOnly !== options.expectedLanOnly);
  const uid = options.uid ?? currentUID();
  const launchd = uid === "" || uid === null || uid === undefined
    ? { loaded: false, state: "", daemonPath: "", error: "Current macOS user id is unavailable." }
    : await readLaunchdState(uid, options.runCommand || defaultRunCommand);
  const runningDaemonPathNeedsUpdate = Boolean(
    launchd.loaded &&
    expectedDaemonPath &&
    launchd.daemonPath &&
    !samePath(launchd.daemonPath, expectedDaemonPath)
  );
  const needsUpdate = daemonPathNeedsUpdate || roleNeedsUpdate || deviceNameNeedsUpdate || lanOnlyNeedsUpdate;
  const updatePending = !needsUpdate && runningDaemonPathNeedsUpdate;

  return {
    supported: true,
    label: serviceLabel,
    status: needsUpdate ? "needs-update" : updatePending ? "update-pending" : launchd.loaded ? "loaded" : installed ? "installed-stopped" : "not-installed",
    installed,
    loaded: launchd.loaded,
    needsUpdate,
    role,
    deviceName,
    lanOnly,
    daemonPath,
    runningDaemonPath: launchd.daemonPath,
    expectedDaemonPath,
    expectedRole,
    expectedDeviceName,
    expectedLanOnly: hasExpectedLanOnly ? options.expectedLanOnly : undefined,
    daemonPathNeedsUpdate,
    runningDaemonPathNeedsUpdate,
    updatePending,
    roleNeedsUpdate,
    deviceNameNeedsUpdate,
    lanOnlyNeedsUpdate,
    state: launchd.state,
    path: plistPath,
    detail: launchAgentDetail({
      installed,
      loaded: launchd.loaded,
      needsUpdate,
      updatePending,
      daemonPathNeedsUpdate,
      roleNeedsUpdate,
      deviceNameNeedsUpdate,
      lanOnlyNeedsUpdate,
      role,
      deviceName,
      lanOnly,
      state: launchd.state
    })
  };
}

function launchAgentPlistPath(homeDir) {
  return path.posix.join(homeDir, "Library", "LaunchAgents", `${serviceLabel}.plist`);
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
  const values = plistArrayStringValues(contents, "ProgramArguments");
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

function plistArrayStringValues(contents, key) {
  const escapedKey = String(key || "").replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = String(contents || "").match(
    new RegExp(`<key>\\s*${escapedKey}\\s*</key>\\s*<array>([\\s\\S]*?)</array>`, "i")
  );
  return match ? plistStringValues(match[1]) : [];
}

function roleFromPlist(contents) {
  return launchAgentConfigFromPlist(contents).role;
}

function expectedRoleValue(value) {
  const role = String(value || "").trim().toLowerCase();
  return role === "orchestrator" || role === "worker" ? role : "";
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
      daemonPath: launchdDaemonPathFromOutput(stdout),
      error: ""
    };
  } catch (error) {
    return {
      loaded: false,
      state: "",
      daemonPath: "",
      error: String(error?.message || "")
    };
  }
}

function launchdStateFromOutput(output) {
  const match = String(output || "").match(/\bstate\s*=\s*([^\n]+)/i);
  return match ? match[1].trim() : "";
}

function launchdDaemonPathFromOutput(output) {
  const match = String(output || "").match(/^\s*program\s*=\s*([^\n]+)$/im);
  return match ? match[1].trim().replace(/^"|"$/g, "") : "";
}

function launchAgentDetail(status: any = {}) {
  if (status.updatePending) {
    return "The background service is updated and will use this app copy after the next login.";
  }
  if (status.needsUpdate) {
    if (status.daemonPathNeedsUpdate) {
      return "Installed from an older app location. Update the background service when convenient.";
    }
    if (status.roleNeedsUpdate) {
      return "Installed for a different role. Update the background service to match this app.";
    }
    if (status.deviceNameNeedsUpdate) {
      return "Installed with a different device name. Update the background service to match this app.";
    }
    if (status.lanOnlyNeedsUpdate) {
      return "Installed with different network settings. Update the background service to match this app.";
    }
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
