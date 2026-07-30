const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const {
  defaultSettings,
  loadSettings,
  normalizeSettings,
  saveSettings,
  settingsPath
} = require("./settings-store");

test("loadSettings returns defaults when no app settings file exists", async (t) => {
  const root = await tempDirectory(t);

  assert.deepEqual(await loadSettings({ userDataPath: root }), defaultSettings());
});

test("saveSettings writes normalized settings to app user data", async (t) => {
  const root = await tempDirectory(t);

  const saved = await saveSettings({
    projectRoot: "/Users/austin/project",
    artifacts: "dist, report.pdf",
    selectedDeviceID: "worker-1",
    selectedDeviceName: "Gaming PC",
    lanDiscovery: false,
    daemonRole: "worker",
    syncedDevices: {
      "worker-1": false
    },
    deviceCapabilities: {
      "worker-1": {
        docker: false,
        tests: true
      }
    },
    capabilities: {
      tests: false,
      commands: true
    }
  }, { userDataPath: root });

  assert.equal(saved.projectRoot, "/Users/austin/project");
  assert.equal(saved.artifacts, "dist, report.pdf");
  assert.equal(saved.selectedDeviceID, "worker-1");
  assert.equal(saved.selectedDeviceName, "Gaming PC");
  assert.equal(saved.lanDiscovery, false);
  assert.equal(saved.daemonRole, "worker");
  assert.deepEqual(saved.syncedDevices, { "worker-1": false });
  assert.deepEqual(saved.deviceCapabilities, {
    "worker-1": {
      docker: false,
      tests: true
    }
  });
  assert.equal(saved.capabilities.builds, true);
  assert.equal(saved.capabilities.tests, false);
  assert.equal(saved.capabilities.commands, true);

  const loaded = await loadSettings({ userDataPath: root });
  assert.deepEqual(loaded, saved);

  const info = await fs.stat(settingsPath({ userDataPath: root }));
  if (process.platform !== "win32") {
    assert.equal(info.mode & 0o077, 0);
  }
});

test("normalizeSettings rejects malformed values without dropping valid fields", () => {
  const normalized = normalizeSettings({
    projectRoot: 42,
    artifacts: "target/release",
    selectedDeviceID: 123,
    selectedDeviceName: "Austin MacBook 2",
    lanDiscovery: "false",
    daemonRole: "invalid",
    syncedDevices: {
      "worker-1": false,
      "worker-2": "nope",
      "worker-3": true
    },
    deviceCapabilities: {
      "worker-1": {
        docker: false,
        dangerous: true,
        tests: "nope",
        commands: true
      },
      "worker-2": "nope",
      "worker-3": {}
    },
    capabilities: {
      builds: false,
      tests: "nope",
      commands: true,
      dangerous: true
    }
  });

  assert.equal(normalized.projectRoot, "");
  assert.equal(normalized.artifacts, "target/release");
  assert.equal(normalized.selectedDeviceID, "");
  assert.equal(normalized.selectedDeviceName, "Austin MacBook 2");
  assert.equal(normalized.lanDiscovery, true);
  assert.equal(normalized.daemonRole, "orchestrator");
  assert.deepEqual(normalized.syncedDevices, {
    "worker-1": false,
    "worker-3": true
  });
  assert.deepEqual(normalized.deviceCapabilities, {
    "worker-1": {
      docker: false,
      commands: true
    }
  });
  assert.equal(Object.hasOwn(normalized.deviceCapabilities["worker-1"], "dangerous"), false);
  assert.equal(normalized.capabilities.builds, false);
  assert.equal(normalized.capabilities.tests, true);
  assert.equal(normalized.capabilities.commands, true);
  assert.equal(Object.hasOwn(normalized.capabilities, "dangerous"), false);
});

test("loadSettings recovers from a corrupt settings file", async (t) => {
  const root = await tempDirectory(t);
  await fs.mkdir(root, { recursive: true });
  await fs.writeFile(settingsPath({ userDataPath: root }), "{not json");

  assert.deepEqual(await loadSettings({ userDataPath: root }), defaultSettings());
});

async function tempDirectory(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "computehop-control-settings-"));
  t.after(async () => {
    await fs.rm(root, { recursive: true, force: true });
  });
  return root;
}
