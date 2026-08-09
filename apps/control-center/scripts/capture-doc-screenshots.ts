const { app, BrowserWindow, ipcMain } = require("electron");
const fs = require("node:fs/promises");
const path = require("node:path");
const { controlCenterRootForModule, runtimeRootForModule } = require("../src/runtime-paths");

if (!process.versions.electron) {
  throw new Error("Run this script with Electron: npm run screenshots:docs --prefix apps/control-center");
}

const controlCenterRoot = controlCenterRootForModule(__dirname);
const repoRoot = path.resolve(controlCenterRoot, "..", "..");
const sourceDir = path.join(runtimeRootForModule(__dirname), "src");
const outputDir = path.resolve(repoRoot, "docs/assets/setup");

const localDevice = {
  id: "local",
  deviceID: "macbook-local",
  name: "Austin MacBook",
  role: "orchestrator",
  trustState: "paired",
  connection: "active",
  availability: "local",
  path: "local",
  platform: "darwin",
  arch: "arm64",
  logicalCPUCount: 12,
  totalMemoryBytes: 36 * 1024 ** 3,
  supportedExecutors: ["native"],
  toolIDs: ["go", "make", "node", "npm"]
};

const gamingPC = {
  id: "gaming-pc",
  deviceID: "gaming-pc",
  name: "Gaming PC",
  role: "worker",
  trustState: "paired",
  connection: "active",
  availability: "remote",
  path: "lan",
  platform: "windows",
  arch: "amd64",
  logicalCPUCount: 32,
  totalMemoryBytes: 64 * 1024 ** 3,
  supportedExecutors: ["container", "native"],
  toolIDs: ["docker", "go", "make", "node", "npm"],
  synced: true
};

const homeServer = {
  id: "home-server",
  deviceID: "home-server",
  name: "Home Server",
  role: "worker",
  trustState: "unpaired",
  connection: "not connected",
  availability: "nearby",
  path: "lan",
  platform: "linux",
  arch: "arm64",
  logicalCPUCount: 8,
  totalMemoryBytes: 32 * 1024 ** 3,
  supportedExecutors: ["native"],
  toolIDs: ["docker", "go"]
};

const baseSettings = {
  projectRoot: "",
  artifacts: "",
  selectedDeviceID: "",
  selectedDeviceName: "",
  lanDiscovery: true,
  askBeforeRun: true,
  daemonRole: "orchestrator",
  syncedDevices: {},
  capabilities: {
    builds: true,
    tests: true,
    docker: true,
    ai: true,
    video: true,
    commands: false
  },
  deviceCapabilities: {}
};

const scenarios = [
  {
    file: "01-devices.png",
    settings: baseSettings,
    devices: [gamingPC, homeServer],
    pairings: [],
    jobs: [],
    prepare: null
  },
  {
    file: "02-pairing-code.png",
    settings: baseSettings,
    devices: [homeServer],
    pairings: [{
      id: "pairing-home-server",
      peerName: "Home Server",
      verificationCode: "3BA2-VFGN-9TBJ-226W",
      state: "waiting",
      localConfirmed: false,
      remoteConfirmed: false
    }],
    jobs: [],
    prepare: null
  },
  {
    file: "03-plan-task.png",
    settings: {
      ...baseSettings,
      selectedDeviceID: "gaming-pc",
      selectedDeviceName: "Gaming PC",
      projectRoot: "/Users/austin/projects/api"
    },
    devices: [gamingPC],
    pairings: [],
    jobs: [{
      id: "job-ci-1",
      shortID: "a17c9f2",
      command: "make pr-check",
      state: "succeeded",
      terminal: true,
      deviceID: "gaming-pc",
      deviceName: "Gaming PC",
      workingDirectory: "/Users/austin/projects/api"
    }],
    prepare: async (win) => {
      await win.webContents.executeJavaScript(`
        const input = document.getElementById("command-input");
        input.value = "Run CI on the gaming PC";
        input.dispatchEvent(new Event("input", { bubbles: true }));
        document.getElementById("run-job").click();
      `);
      await wait(800);
    }
  }
];

let activeScenario = scenarios[0];

app.commandLine.appendSwitch("disable-gpu");
app.on("window-all-closed", (event) => {
  event.preventDefault();
});

ipcMain.handle("app:info", () => ({
  platform: "darwin",
  arch: "arm64",
  defaultDaemonRole: "orchestrator",
  daemonRoles: [
    { id: "orchestrator", label: "Control Mac" },
    { id: "worker", label: "Worker" }
  ]
}));
ipcMain.handle("settings:load", () => ({ settings: activeScenario.settings }));
ipcMain.handle("settings:save", () => ({ ok: true }));
ipcMain.handle("aiPlanner:status", () => ({
  status: {
    configured: false,
    source: "",
    encrypted: false,
    provider: "openai",
    baseURL: "",
    model: ""
  }
}));
ipcMain.handle("aiPlanner:save", () => ({ status: { configured: true, source: "app", encrypted: true } }));
ipcMain.handle("aiPlanner:clear", () => ({ status: { configured: false } }));
ipcMain.handle("daemon:status", () => ({
  ok: true,
  localDevice
}));
ipcMain.handle("daemon:start", () => ({ ok: true }));
ipcMain.handle("daemon:launchAgentStatus", () => ({
  status: {
    supported: true,
    status: "loaded",
    installed: true,
    loaded: true,
    role: "orchestrator",
    deviceName: "Austin MacBook",
    detail: "ComputeHop starts at login."
  }
}));
ipcMain.handle("daemon:installLaunchAgent", () => ({
  ok: true,
  started: true,
  detail: "ComputeHop starts at login.",
  status: {
    supported: true,
    status: "loaded",
    installed: true,
    loaded: true
  }
}));
ipcMain.handle("devices:list", () => ({
  ok: true,
  error: "",
  localDevice,
  devices: activeScenario.devices,
  pairings: activeScenario.pairings
}));
ipcMain.handle("devices:connect", () => ({ ok: true }));
ipcMain.handle("devices:forget", () => ({ ok: true }));
ipcMain.handle("pairings:confirm", () => ({ ok: true }));
ipcMain.handle("pairings:reject", () => ({ ok: true }));
ipcMain.handle("planner:suggest", () => ({
  suggestions: [{
    label: "Run CI",
    task: "Run CI",
    command: "make pr-check",
    requiresProject: true,
    targetPreference: "worker",
    requiredToolIDs: ["go", "make", "node", "npm"],
    work: "tests"
  }, {
    label: "Build app",
    task: "Build app",
    command: "make release-check",
    requiresProject: true,
    targetPreference: "worker",
    requiredToolIDs: ["go", "make", "node", "npm"],
    work: "builds"
  }]
}));
ipcMain.handle("planner:plan", () => ({
  ok: true,
  plan: {
    source: "Run CI on the gaming PC",
    title: "Run CI",
    detail: "Use the project validation target.",
    command: "make pr-check",
    requiresProject: true,
    targetPreference: "worker",
    requiredToolIDs: ["go", "make", "node", "npm"],
    work: "tests"
  }
}));
ipcMain.handle("jobs:list", () => ({
  jobs: activeScenario.jobs
}));
ipcMain.handle("jobs:start", () => ({
  runID: "doc-run",
  job: activeScenario.jobs[0] || null
}));
ipcMain.handle("jobs:stop", () => ({ stopped: true }));
ipcMain.handle("jobs:logs", () => ({
  text: "go test ./...\\nnpm test\\nWorker archive smoke passed.\\n",
  truncated: false,
  job: activeScenario.jobs[0] || null
}));
ipcMain.handle("jobs:cancel", () => ({
  job: {
    ...(activeScenario.jobs[0] || {}),
    state: "cancelled",
    terminal: true
  }
}));
ipcMain.handle("jobs:fetchOutputs", () => ({
  destination: "/Users/austin/Downloads/ComputeHop Outputs",
  restoredFileCount: 2,
  conflictFileCount: 0
}));
ipcMain.handle("outputs:chooseDestination", () => "/Users/austin/Downloads/ComputeHop Outputs");
ipcMain.handle("project:choose", () => "/Users/austin/projects/api");
ipcMain.handle("app:openExternal", () => true);

app.whenReady().then(async () => {
  await fs.mkdir(outputDir, { recursive: true });
  for (const scenario of scenarios) {
    activeScenario = scenario;
    await captureScenario(scenario);
  }
  app.quit();
}).catch((error) => {
  console.error(error);
  app.exit(1);
});

async function captureScenario(scenario) {
  const win = new BrowserWindow({
    width: 720,
    height: 900,
    show: false,
    backgroundColor: "#2a2d34",
    webPreferences: {
      preload: path.join(sourceDir, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false
    }
  });

  await win.loadFile(path.join(sourceDir, "index.html"));
  await waitForRenderer(win);
  if (scenario.prepare) {
    await scenario.prepare(win);
  }
  await wait(500);
  const image = await win.webContents.capturePage();
  await fs.writeFile(path.join(outputDir, scenario.file), image.toPNG());
  win.destroy();
}

async function waitForRenderer(win) {
  await win.webContents.executeJavaScript(`
    new Promise((resolve) => {
      if (document.readyState === "complete") {
        setTimeout(resolve, 900);
        return;
      }
      window.addEventListener("load", () => setTimeout(resolve, 900), { once: true });
    });
  `);
}

function wait(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
