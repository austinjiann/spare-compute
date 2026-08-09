const childProcess = require("node:child_process");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const { promisify } = require("node:util");
const { controlCenterRootForModule } = require("./runtime-paths");
const {
  launchAgentPlistPath,
  launchAgentStatus,
  serviceLabel
} = require("./launch-agent-status");

const defaultRunCommand = promisify(childProcess.execFile);
const defaultTimeoutMs = 5000;

async function installLaunchAgent(options: any = {}) {
  const platform = options.platform || process.platform;
  if (platform !== "darwin") {
    throw new Error("Background login service setup is available on macOS only.");
  }

  const fsModule = options.fs || fs;
  const runCommand = options.runCommand || defaultRunCommand;
  const role = normalizedRole(options.role);
  const uid = options.uid ?? currentUID();
  if (uid === "" || uid === null || uid === undefined) {
    throw new Error("Current macOS user id is unavailable.");
  }

  const daemonPath = await resolveDaemonExecutable(options, fsModule, platform);
  const deviceName = normalizedDeviceName(options.deviceName || os.hostname());
  const lanOnly = Boolean(options.lanOnly);
  const homeDir = options.homeDir || os.homedir();
  const plistPath = launchAgentPlistPath(homeDir);
  const logsDir = path.join(homeDir, "Library", "Logs", "ComputeHop");
  const before = options.status || await launchAgentStatus({
    platform,
    homeDir,
    uid,
    fs: fsModule,
    runCommand,
    expectedDaemonPath: daemonPath,
    expectedRole: role,
    expectedDeviceName: deviceName,
    expectedLanOnly: lanOnly
  });
  await assertReplaceablePlist(plistPath, fsModule);

  await fsModule.mkdir(path.dirname(plistPath), { recursive: true });
  await fsModule.mkdir(logsDir, { recursive: true, mode: 0o700 });
  await writeFileAtomic(
    plistPath,
    launchAgentPlist({
      daemonPath,
      role,
      deviceName,
      lanOnly,
      logPath: path.join(logsDir, "daemon.log"),
      workingDirectory: homeDir
    }),
    fsModule
  );

  const shouldBootstrap = options.bootstrap !== false && !options.currentDaemonRunning;
  if (shouldBootstrap) {
    if (before.loaded) {
      await runCommand("launchctl", ["bootout", `gui/${uid}/${serviceLabel}`], { timeout: defaultTimeoutMs });
    }
    await runCommand("launchctl", ["bootstrap", `gui/${uid}`, plistPath], { timeout: defaultTimeoutMs });
    await runCommand("launchctl", ["kickstart", "-k", `gui/${uid}/${serviceLabel}`], { timeout: defaultTimeoutMs });
  }

  const status = shouldBootstrap
    ? await launchAgentStatus({
        platform,
        homeDir,
        uid,
        fs: fsModule,
        runCommand,
        expectedDaemonPath: daemonPath,
        expectedRole: role,
        expectedDeviceName: deviceName,
        expectedLanOnly: lanOnly
      })
    : {
        supported: true,
        label: serviceLabel,
        status: "installed-stopped",
        installed: true,
        loaded: false,
        needsUpdate: false,
        role,
        deviceName,
        lanOnly,
        daemonPath,
        expectedDaemonPath: daemonPath,
        state: "",
        path: plistPath,
        detail: "Installed for login. The current session daemon will keep running until you quit or restart."
      };

  return {
    ok: true,
    installed: true,
    started: Boolean(shouldBootstrap && status.loaded),
    skippedStart: !shouldBootstrap,
    status,
    detail: installDetail({ role, started: shouldBootstrap && status.loaded, skippedStart: !shouldBootstrap })
  };
}

async function assertReplaceablePlist(plistPath, fsModule) {
  let contents = "";
  try {
    contents = await fsModule.readFile(plistPath, "utf8");
  } catch {
    return;
  }
  if (labelFromPlist(contents) !== serviceLabel) {
    throw new Error(`Refusing to replace an unrelated LaunchAgent at ${plistPath}.`);
  }
}

function labelFromPlist(contents) {
  const labelMatch = String(contents || "").match(/<key>Label<\/key>\s*<string>([^<]+)<\/string>/);
  return labelMatch ? decodePlistString(labelMatch[1]).trim() : "";
}

async function resolveDaemonExecutable(options: any = {}, fsModule = fs, platform = process.platform) {
  const candidates = options.daemonPath
    ? [options.daemonPath]
    : [
        parentAppDaemonPath(options.resourcesPath, platform),
        options.resourcesPath ? path.join(options.resourcesPath, "bin", executableName("computehopd", platform)) : "",
        options.controlCenterRoot ? path.join(options.controlCenterRoot, "resources", "bin", executableName("computehopd", platform)) : "",
        path.join(controlCenterRootForModule(__dirname), "resources", "bin", executableName("computehopd", platform))
      ].filter(Boolean);

  for (const candidate of candidates) {
    if (await executableExists(candidate, fsModule, platform)) {
      return candidate;
    }
  }
  throw new Error("No bundled ComputeHop daemon was found. Package Control Center first, or use the macOS installer.");
}

function parentAppDaemonPath(resourcesPath, platform) {
  if (!resourcesPath) {
    return "";
  }

  const currentResources = path.resolve(resourcesPath);
  const currentContents = path.dirname(currentResources);
  const currentApp = path.dirname(currentContents);
  if (path.basename(currentResources) !== "Resources" || path.basename(currentContents) !== "Contents" || !currentApp.endsWith(".app")) {
    return "";
  }

  const parentResources = path.dirname(currentApp);
  const parentContents = path.dirname(parentResources);
  const parentApp = path.dirname(parentContents);
  if (path.basename(parentResources) !== "Resources" || path.basename(parentContents) !== "Contents" || !parentApp.endsWith(".app")) {
    return "";
  }

  return path.join(parentResources, "bin", executableName("computehopd", platform));
}

async function executableExists(filePath, fsModule, platform) {
  try {
    const info = await fsModule.stat(filePath);
    return info.isFile() && (platform === "win32" || (info.mode & 0o111) !== 0);
  } catch {
    return false;
  }
}

async function writeFileAtomic(filePath, contents, fsModule) {
  const temporary = `${filePath}.tmp-${process.pid}-${Date.now()}`;
  await fsModule.writeFile(temporary, contents, { mode: 0o644 });
  await fsModule.rename(temporary, filePath);
}

function launchAgentPlist(config) {
  const values = [
    config.daemonPath,
    "--role",
    config.role,
    "--device-name",
    normalizedDeviceName(config.deviceName)
  ];
  if (config.lanOnly) {
    values.push("--lan-only");
  }
  return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${escapePlistString(serviceLabel)}</string>
  <key>ProgramArguments</key>
  <array>
${values.map((value) => `    <string>${escapePlistString(value)}</string>`).join("\n")}
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardOutPath</key>
  <string>${escapePlistString(config.logPath)}</string>
  <key>StandardErrorPath</key>
  <string>${escapePlistString(config.logPath)}</string>
  <key>WorkingDirectory</key>
  <string>${escapePlistString(config.workingDirectory)}</string>
</dict>
</plist>
`;
}

function installDetail(result: any = {}) {
  const role = roleLabel(result.role);
  if (result.started) {
    return `ComputeHop now starts at login${role ? ` as ${role}` : ""}.`;
  }
  if (result.skippedStart) {
    return `ComputeHop will start at next login${role ? ` as ${role}` : ""}.`;
  }
  return "ComputeHop was installed for login.";
}

function normalizedRole(role) {
  return String(role || "").trim().toLowerCase() === "worker" ? "worker" : "orchestrator";
}

function normalizedDeviceName(value) {
  const trimmed = String(value || "").replace(/\.local$/i, "").trim();
  return trimmed || "This Mac";
}

function roleLabel(role) {
  return normalizedRole(role) === "worker" ? "Worker" : "Control Mac";
}

function executableName(name, platform = process.platform) {
  return platform === "win32" ? `${name}.exe` : name;
}

function escapePlistString(value) {
  return String(value || "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function decodePlistString(value) {
  return String(value || "")
    .replaceAll("&lt;", "<")
    .replaceAll("&gt;", ">")
    .replaceAll("&amp;", "&");
}

function currentUID() {
  return typeof process.getuid === "function" ? process.getuid() : "";
}

module.exports = {
  installLaunchAgent,
  labelFromPlist,
  launchAgentPlist,
  normalizedDeviceName,
  parentAppDaemonPath,
  resolveDaemonExecutable
};
