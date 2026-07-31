const assert = require("node:assert/strict");
const test = require("node:test");
const { shouldAutoStartDaemon } = require("./daemon-autostart");

test("shouldAutoStartDaemon starts once after runtime and settings are ready", () => {
  assert.equal(shouldAutoStartDaemon({
    daemonAvailable: false,
    startingDaemon: false,
    autoStartAttempted: false,
    runtimeLoaded: true,
    settingsHydrated: true,
    settings: { lanDiscovery: true }
  }), true);
});

test("shouldAutoStartDaemon waits for runtime and settings", () => {
  assert.equal(shouldAutoStartDaemon({
    daemonAvailable: false,
    startingDaemon: false,
    autoStartAttempted: false,
    runtimeLoaded: false,
    settingsHydrated: true,
    settings: { lanDiscovery: true }
  }), false);
  assert.equal(shouldAutoStartDaemon({
    daemonAvailable: false,
    startingDaemon: false,
    autoStartAttempted: false,
    runtimeLoaded: true,
    settingsHydrated: false,
    settings: { lanDiscovery: true }
  }), false);
});

test("shouldAutoStartDaemon skips when daemon is already handled", () => {
  assert.equal(shouldAutoStartDaemon({
    daemonAvailable: true,
    startingDaemon: false,
    autoStartAttempted: false,
    runtimeLoaded: true,
    settingsHydrated: true,
    settings: { lanDiscovery: true }
  }), false);
  assert.equal(shouldAutoStartDaemon({
    daemonAvailable: false,
    startingDaemon: true,
    autoStartAttempted: false,
    runtimeLoaded: true,
    settingsHydrated: true,
    settings: { lanDiscovery: true }
  }), false);
  assert.equal(shouldAutoStartDaemon({
    daemonAvailable: false,
    startingDaemon: false,
    autoStartAttempted: true,
    runtimeLoaded: true,
    settingsHydrated: true,
    settings: { lanDiscovery: true }
  }), false);
});

test("shouldAutoStartDaemon respects disabled LAN discovery", () => {
  assert.equal(shouldAutoStartDaemon({
    daemonAvailable: false,
    startingDaemon: false,
    autoStartAttempted: false,
    runtimeLoaded: true,
    settingsHydrated: true,
    settings: { lanDiscovery: false }
  }), false);
});
