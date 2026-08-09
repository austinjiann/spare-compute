const assert = require("node:assert/strict");
const test = require("node:test");
const {
  launchAgentConfigFromPlist,
  launchAgentPlistPath,
  launchAgentStatus,
  roleFromPlist
} = require("./launch-agent-status");

test("launchAgentStatus is unsupported outside macOS", async () => {
  const status = await launchAgentStatus({ platform: "linux" });
  assert.equal(status.supported, false);
  assert.equal(status.status, "unsupported");
  assert.equal(status.installed, false);
  assert.equal(status.loaded, false);
});

test("launchAgentStatus reports a missing macOS launch agent", async () => {
  const status = await launchAgentStatus({
    platform: "darwin",
    homeDir: "/Users/austin",
    uid: 501,
    fs: missingFS(),
    runCommand: async () => {
      throw new Error("No such service");
    }
  });

  assert.equal(status.supported, true);
  assert.equal(status.path, "/Users/austin/Library/LaunchAgents/com.computehop.daemon.plist");
  assert.equal(status.status, "not-installed");
  assert.equal(status.installed, false);
  assert.equal(status.loaded, false);
  assert.match(status.detail, /Not installed/);
});

test("launchAgentStatus reports an installed loaded worker", async () => {
  const status = await launchAgentStatus({
    platform: "darwin",
    homeDir: "/Users/austin",
    uid: 501,
    fs: memoryFS(workerPlist()),
    runCommand: async (_command, args) => {
      assert.deepEqual(args, ["print", "gui/501/com.computehop.daemon"]);
      return { stdout: "state = running\npid = 123\n" };
    }
  });

  assert.equal(status.status, "loaded");
  assert.equal(status.installed, true);
  assert.equal(status.loaded, true);
  assert.equal(status.role, "worker");
  assert.equal(status.deviceName, "Studio Mini");
  assert.equal(status.lanOnly, true);
  assert.equal(status.daemonPath, "/Users/austin/Applications/ComputeHop.app/Contents/Resources/bin/computehopd");
  assert.equal(status.state, "running");
  assert.match(status.detail, /Runs at login as Worker named Studio Mini LAN-only/);
});

test("launchAgentStatus reports installed but stopped launch agents", async () => {
  const status = await launchAgentStatus({
    platform: "darwin",
    homeDir: "/Users/austin",
    uid: 501,
    fs: memoryFS(workerPlist()),
    runCommand: async () => {
      throw new Error("service not loaded");
    }
  });

  assert.equal(status.status, "installed-stopped");
  assert.equal(status.installed, true);
  assert.equal(status.loaded, false);
  assert.equal(status.role, "worker");
  assert.match(status.detail, /Installed as Worker named Studio Mini LAN-only/);
});

test("launchAgentStatus reports launch agents that point at an older daemon path", async () => {
  const status = await launchAgentStatus({
    platform: "darwin",
    homeDir: "/Users/austin",
    uid: 501,
    expectedDaemonPath: "/Users/austin/Applications/ComputeHop.app/Contents/Resources/bin/computehopd",
    fs: memoryFS(plistWithDaemon({
      daemonPath: "/Users/austin/Applications/ComputeHop Control Center.app/Contents/Resources/bin/computehopd",
      role: "worker"
    })),
    runCommand: async () => ({ stdout: "state = running\npid = 123\n" })
  });

  assert.equal(status.status, "needs-update");
  assert.equal(status.installed, true);
  assert.equal(status.loaded, true);
  assert.equal(status.needsUpdate, true);
  assert.equal(status.role, "worker");
  assert.match(status.detail, /older app location/);
});

test("launchAgentStatus reports launch agents with stale role and device name", async () => {
  const status = await launchAgentStatus({
    platform: "darwin",
    homeDir: "/Users/austin",
    uid: 501,
    expectedRole: "orchestrator",
    expectedDeviceName: "Austin MacBook",
    fs: memoryFS(workerPlist()),
    runCommand: async () => ({ stdout: "state = running\npid = 123\n" })
  });

  assert.equal(status.status, "needs-update");
  assert.equal(status.needsUpdate, true);
  assert.equal(status.roleNeedsUpdate, true);
  assert.equal(status.deviceNameNeedsUpdate, true);
  assert.equal(status.role, "worker");
  assert.equal(status.expectedRole, "orchestrator");
  assert.equal(status.deviceName, "Studio Mini");
  assert.equal(status.expectedDeviceName, "Austin MacBook");
  assert.match(status.detail, /different role/);
});

test("roleFromPlist reads the configured daemon role", () => {
  assert.equal(roleFromPlist(workerPlist()), "worker");
  assert.equal(roleFromPlist(orchestratorPlist()), "orchestrator");
  assert.equal(roleFromPlist("<plist></plist>"), "");
  assert.deepEqual(launchAgentConfigFromPlist(workerPlist()), {
    daemonPath: "/Users/austin/Applications/ComputeHop.app/Contents/Resources/bin/computehopd",
    role: "worker",
    deviceName: "Studio Mini",
    lanOnly: true
  });
  assert.equal(launchAgentPlistPath("/tmp/home"), "/tmp/home/Library/LaunchAgents/com.computehop.daemon.plist");
});

function missingFS() {
  return {
    access: async () => {
      throw new Error("missing");
    },
    readFile: async () => {
      throw new Error("missing");
    }
  };
}

function memoryFS(contents) {
  return {
    access: async () => {},
    readFile: async () => contents
  };
}

function workerPlist() {
  return plistWithDaemon({
    daemonPath: "/Users/austin/Applications/ComputeHop.app/Contents/Resources/bin/computehopd",
    role: "worker",
    deviceName: "Studio Mini",
    lanOnly: true
  });
}

function orchestratorPlist() {
  return plistWithRole("orchestrator");
}

function plistWithRole(role) {
  return plistWithDaemon({
    daemonPath: "/Users/austin/Applications/ComputeHop.app/Contents/Resources/bin/computehopd",
    role
  });
}

function plistWithDaemon(config) {
  return `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
  <key>ProgramArguments</key>
  <array>
    <string>${config.daemonPath}</string>
    <string>--role</string>
    <string>${config.role}</string>
    ${config.deviceName ? `<string>--device-name</string>
    <string>${config.deviceName}</string>` : ""}
    ${config.lanOnly ? "<string>--lan-only</string>" : ""}
  </array>
</dict>
</plist>`;
}
