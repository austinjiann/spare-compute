const fs = require("node:fs/promises");
const path = require("node:path");

const settingsFileName = "control-center-settings.json";

function defaultSettings() {
  return {
    projectRoot: "",
    artifacts: "",
    ignoreHeavyFolders: true,
    lanDiscovery: true,
    remoteRelay: false,
    askBeforeRun: true,
    daemonRole: "orchestrator",
    aiProvider: "off",
    syncedDevices: {},
    capabilities: {
      builds: true,
      tests: true,
      docker: true,
      ai: true,
      video: true,
      commands: false
    }
  };
}

async function loadSettings(options = {}) {
  try {
    const raw = await fs.readFile(settingsPath(options), "utf8");
    return normalizeSettings(JSON.parse(raw));
  } catch (error) {
    if (error?.code === "ENOENT" || error instanceof SyntaxError) {
      return defaultSettings();
    }
    throw error;
  }
}

async function saveSettings(settings, options = {}) {
  const filePath = settingsPath(options);
  const normalized = normalizeSettings(settings);
  await fs.mkdir(path.dirname(filePath), { recursive: true, mode: 0o700 });
  const temporaryPath = `${filePath}.${process.pid}.${Date.now()}.tmp`;
  await fs.writeFile(temporaryPath, `${JSON.stringify(normalized, null, 2)}\n`, { mode: 0o600 });
  await fs.rename(temporaryPath, filePath);
  return normalized;
}

function normalizeSettings(settings = {}) {
  const defaults = defaultSettings();
  const source = isObject(settings) ? settings : {};
  const capabilities = isObject(source.capabilities) ? source.capabilities : {};

  return {
    projectRoot: stringSetting(source.projectRoot, defaults.projectRoot),
    artifacts: stringSetting(source.artifacts, defaults.artifacts),
    ignoreHeavyFolders: booleanSetting(source.ignoreHeavyFolders, defaults.ignoreHeavyFolders),
    lanDiscovery: booleanSetting(source.lanDiscovery, defaults.lanDiscovery),
    remoteRelay: booleanSetting(source.remoteRelay, defaults.remoteRelay),
    askBeforeRun: booleanSetting(source.askBeforeRun, defaults.askBeforeRun),
    daemonRole: normalizeDaemonRole(source.daemonRole, defaults.daemonRole),
    aiProvider: normalizeAIProvider(source.aiProvider, defaults.aiProvider),
    syncedDevices: isObject(source.syncedDevices) ? source.syncedDevices : {},
    capabilities: {
      builds: booleanSetting(capabilities.builds, defaults.capabilities.builds),
      tests: booleanSetting(capabilities.tests, defaults.capabilities.tests),
      docker: booleanSetting(capabilities.docker, defaults.capabilities.docker),
      ai: booleanSetting(capabilities.ai, defaults.capabilities.ai),
      video: booleanSetting(capabilities.video, defaults.capabilities.video),
      commands: booleanSetting(capabilities.commands, defaults.capabilities.commands)
    }
  };
}

function settingsPath(options = {}) {
  const userDataPath = options.userDataPath || options.app?.getPath?.("userData");
  if (!userDataPath) {
    throw new Error("userDataPath is required for Control Center settings");
  }
  return path.join(userDataPath, settingsFileName);
}

function booleanSetting(value, fallback) {
  return typeof value === "boolean" ? value : fallback;
}

function stringSetting(value, fallback) {
  return typeof value === "string" ? value : fallback;
}

function normalizeDaemonRole(value, fallback) {
  return value === "worker" || value === "orchestrator" ? value : fallback;
}

function normalizeAIProvider(value, fallback) {
  return value === "off" || value === "openai" || value === "local" ? value : fallback;
}

function isObject(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

module.exports = {
  defaultSettings,
  loadSettings,
  normalizeSettings,
  saveSettings,
  settingsPath
};
