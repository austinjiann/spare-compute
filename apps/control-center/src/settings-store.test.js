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
    lanDiscovery: false,
    daemonRole: "worker",
    aiProvider: "openai",
    capabilities: {
      tests: false,
      commands: true
    }
  }, { userDataPath: root });

  assert.equal(saved.projectRoot, "/Users/austin/project");
  assert.equal(saved.artifacts, "dist, report.pdf");
  assert.equal(saved.lanDiscovery, false);
  assert.equal(saved.daemonRole, "worker");
  assert.equal(saved.aiProvider, "openai");
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
    lanDiscovery: "false",
    daemonRole: "invalid",
    aiProvider: "invalid",
    syncedDevices: [],
    capabilities: {
      builds: false,
      tests: "nope",
      commands: true
    }
  });

  assert.equal(normalized.projectRoot, "");
  assert.equal(normalized.artifacts, "target/release");
  assert.equal(normalized.lanDiscovery, true);
  assert.equal(normalized.daemonRole, "orchestrator");
  assert.equal(normalized.aiProvider, "off");
  assert.deepEqual(normalized.syncedDevices, {});
  assert.equal(normalized.capabilities.builds, false);
  assert.equal(normalized.capabilities.tests, true);
  assert.equal(normalized.capabilities.commands, true);
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
