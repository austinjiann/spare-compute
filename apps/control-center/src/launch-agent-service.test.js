const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const {
  installLaunchAgent,
  labelFromPlist,
  launchAgentPlist,
  parentAppDaemonPath,
  resolveDaemonExecutable
} = require("./launch-agent-service");

test("installLaunchAgent writes and starts a per-user launch agent", async (t) => {
  const home = await tempDirectory(t);
  const daemonPath = await executable(t, home);
  const calls = [];
  let bootstrapped = false;

  const result = await installLaunchAgent({
    platform: "darwin",
    homeDir: home,
    uid: 501,
    daemonPath,
    role: "worker",
    runCommand: async (command, args) => {
      calls.push([command, args]);
      if (args[0] === "print" && !bootstrapped) {
        throw new Error("service not loaded");
      }
      if (args[0] === "bootstrap") {
        bootstrapped = true;
      }
      return { stdout: "state = running\n" };
    }
  });

  assert.equal(result.ok, true);
  assert.equal(result.started, true);
  assert.equal(result.status.loaded, true);
  assert.equal(result.status.role, "worker");
  assert.deepEqual(calls.map((call) => call[1][0]), ["print", "bootstrap", "kickstart", "print"]);

  const plist = await fs.readFile(
    path.join(home, "Library", "LaunchAgents", "com.computehop.daemon.plist"),
    "utf8"
  );
  assert.equal(labelFromPlist(plist), "com.computehop.daemon");
  assert.match(plist, /<string>--role<\/string>\s*<string>worker<\/string>/);
  assert.match(plist, /<key>KeepAlive<\/key>\s*<true\/>/);
});

test("installLaunchAgent installs for next login when a session daemon is already running", async (t) => {
  const home = await tempDirectory(t);
  const daemonPath = await executable(t, home);
  const calls = [];

  const result = await installLaunchAgent({
    platform: "darwin",
    homeDir: home,
    uid: 501,
    daemonPath,
    role: "orchestrator",
    currentDaemonRunning: true,
    runCommand: async (command, args) => {
      calls.push([command, args]);
      throw new Error("service not loaded");
    }
  });

  assert.equal(result.ok, true);
  assert.equal(result.started, false);
  assert.equal(result.skippedStart, true);
  assert.equal(result.status.installed, true);
  assert.equal(result.status.loaded, false);
  assert.equal(result.status.role, "orchestrator");
  assert.deepEqual(calls.map((call) => call[1][0]), ["print"]);
});

test("installLaunchAgent replaces only ComputeHop launch agents", async (t) => {
  const home = await tempDirectory(t);
  const daemonPath = await executable(t, home);
  const launchAgents = path.join(home, "Library", "LaunchAgents");
  await fs.mkdir(launchAgents, { recursive: true });
  await fs.writeFile(
    path.join(launchAgents, "com.computehop.daemon.plist"),
    launchAgentPlist({
      daemonPath,
      role: "worker",
      logPath: "/tmp/computehop.log",
      workingDirectory: home
    })
  );

  await assert.doesNotReject(() => installLaunchAgent({
    platform: "darwin",
    homeDir: home,
    uid: 501,
    daemonPath,
    role: "worker",
    runCommand: async () => ({ stdout: "state = running\n" })
  }));

  await fs.writeFile(
    path.join(launchAgents, "com.computehop.daemon.plist"),
    "<plist><dict><key>Label</key><string>com.example.other</string></dict></plist>"
  );

  await assert.rejects(
    () => installLaunchAgent({
      platform: "darwin",
      homeDir: home,
      uid: 501,
      daemonPath,
      role: "worker",
      runCommand: async () => ({ stdout: "" })
    }),
    /Refusing to replace/
  );
});

test("installLaunchAgent refuses unsupported platforms and missing daemons", async (t) => {
  const home = await tempDirectory(t);

  await assert.rejects(
    () => installLaunchAgent({
      platform: "linux",
      homeDir: home,
      uid: 501,
      daemonPath: "/tmp/computehopd"
    }),
    /macOS only/
  );

  await assert.rejects(
    () => installLaunchAgent({
      platform: "darwin",
      homeDir: home,
      uid: 501,
      daemonPath: path.join(home, "missing"),
      runCommand: async () => ({ stdout: "" })
    }),
    /No bundled ComputeHop daemon/
  );
});

test("resolveDaemonExecutable prefers explicit and resource daemon paths", async (t) => {
  const root = await tempDirectory(t);
  const explicit = await executable(t, root);
  assert.equal(await resolveDaemonExecutable({ daemonPath: explicit }, fs, "darwin"), explicit);

  const resources = path.join(root, "Resources");
  const resourceDaemon = path.join(resources, "bin", "computehopd");
  await fs.mkdir(path.dirname(resourceDaemon), { recursive: true });
  await fs.writeFile(resourceDaemon, "");
  await fs.chmod(resourceDaemon, 0o755);

  assert.equal(
    await resolveDaemonExecutable({ resourcesPath: resources }, fs, "darwin"),
    resourceDaemon
  );
});

test("resolveDaemonExecutable prefers parent app daemon for embedded Control Center", async (t) => {
  const root = await tempDirectory(t);
  const parentResources = path.join(root, "ComputeHop.app", "Contents", "Resources");
  const controlCenterResources = path.join(
    parentResources,
    "ComputeHop Control Center.app",
    "Contents",
    "Resources"
  );
  const parentDaemon = path.join(parentResources, "bin", "computehopd");
  const nestedDaemon = path.join(controlCenterResources, "bin", "computehopd");
  await fs.mkdir(path.dirname(parentDaemon), { recursive: true });
  await fs.mkdir(path.dirname(nestedDaemon), { recursive: true });
  await fs.writeFile(parentDaemon, "");
  await fs.writeFile(nestedDaemon, "");
  await fs.chmod(parentDaemon, 0o755);
  await fs.chmod(nestedDaemon, 0o755);

  assert.equal(parentAppDaemonPath(controlCenterResources, "darwin"), parentDaemon);
  assert.equal(
    await resolveDaemonExecutable({ resourcesPath: controlCenterResources }, fs, "darwin"),
    parentDaemon
  );
});

test("parentAppDaemonPath ignores ordinary app resource paths", () => {
  assert.equal(
    parentAppDaemonPath("/Applications/ComputeHop Control Center.app/Contents/Resources", "darwin"),
    ""
  );
  assert.equal(parentAppDaemonPath("", "darwin"), "");
});

async function tempDirectory(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "computehop-launch-agent-"));
  t.after(async () => {
    await fs.rm(root, { recursive: true, force: true });
  });
  return root;
}

async function executable(_t, root) {
  const filePath = path.join(root, "computehopd");
  await fs.writeFile(filePath, "");
  await fs.chmod(filePath, 0o755);
  return filePath;
}
