const { app, BrowserWindow, dialog, ipcMain, shell } = require("electron");
const { randomUUID } = require("node:crypto");
const path = require("node:path");
const {
  LocalDaemonClient,
  jobStateLabel,
  jobSucceeded,
  jobTerminal
} = require("./local-daemon");
const { startDaemon } = require("./daemon-launcher");
const { planTask } = require("./planner");
const { appRuntimeInfo, normalizeDaemonRole } = require("./runtime-info");

const repoRoot = path.resolve(__dirname, "..", "..", "..");
const activeRuns = new Map();
const logPollMs = 900;
const maximumLogPages = 20;

function createWindow() {
  const win = new BrowserWindow({
    width: 720,
    height: 640,
    minWidth: 560,
    minHeight: 520,
    title: "ComputeHop Control Center",
    backgroundColor: "#00000000",
    transparent: true,
    vibrancy: "under-window",
    visualEffectState: "active",
    titleBarStyle: "hiddenInset",
    trafficLightPosition: { x: 16, y: 18 },
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false
    }
  });

  win.loadFile(path.join(__dirname, "index.html"));
}

app.whenReady().then(() => {
  createWindow();

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow();
    }
  });
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});

ipcMain.handle("devices:list", async () => {
  try {
    const client = new LocalDaemonClient();
    const local = await client.ping();
    const result = await client.listDevices();
    const pairings = await client.listPairings();
    return {
      ok: true,
      error: "",
      localDevice: mapLocalDevice(local),
      devices: mapDevices(result),
      pairings: mapPairings(pairings)
    };
  } catch (error) {
    return {
      ok: false,
      error: readableError(error),
      errorCode: error?.code || "",
      localDevice: null,
      devices: [],
      pairings: []
    };
  }
});

ipcMain.handle("app:info", async () => appRuntimeInfo(process.platform));

ipcMain.handle("daemon:start", async (_event, request) => {
  try {
    return await startDaemon({
      isPackaged: app.isPackaged,
      resourcesPath: process.resourcesPath,
      role: daemonRoleFromRequest(request)
    });
  } catch (error) {
    return { ok: false, error: readableError(error), errorCode: error?.code || "" };
  }
});

ipcMain.handle("devices:connect", async (_event, deviceID) => {
  const client = new LocalDaemonClient();
  const pairing = await client.beginPairing(String(deviceID || "").trim());
  return { pairing: mapPairing(pairing) };
});

ipcMain.handle("devices:forget", async (_event, deviceID) => {
  const client = new LocalDaemonClient();
  const device = await client.unpairDevice(String(deviceID || "").trim());
  return { device: mapTrustedDevice(device) };
});

ipcMain.handle("pairings:confirm", async (_event, pairingID) => {
  const client = new LocalDaemonClient();
  const pairing = await client.confirmPairing(String(pairingID || "").trim());
  return { pairing: mapPairing(pairing) };
});

ipcMain.handle("pairings:reject", async (_event, pairingID) => {
  const client = new LocalDaemonClient();
  const pairing = await client.rejectPairing(String(pairingID || "").trim());
  return { pairing: mapPairing(pairing) };
});

ipcMain.handle("app:openExternal", async (_event, target) => {
  if (typeof target !== "string") {
    return;
  }
  await shell.openExternal(target);
});

ipcMain.handle("project:choose", async () => {
  const result = await dialog.showOpenDialog({
    title: "Choose Project",
    properties: ["openDirectory"]
  });
  if (result.canceled || result.filePaths.length === 0) {
    return null;
  }
  return result.filePaths[0];
});

ipcMain.handle("planner:plan", async (_event, request) => {
  return planTask({
    task: request?.task,
    projectRoot: request?.projectRoot || ""
  });
});

ipcMain.handle("jobs:start", async (event, request) => {
  const jobRequest = normalizeJobRequest(request);
  const argv = splitCommandLine(jobRequest.command);
  if (argv.length === 0) {
    throw new Error("Enter something to run.");
  }

  const runID = randomUUID();
  setImmediate(() => startDaemonJobStream(event.sender, runID, jobRequest, argv));
  return { runID };
});

ipcMain.handle("jobs:list", async (_event, request) => {
  const client = new LocalDaemonClient();
  const deviceSelector = deviceSelectorFromRequest(request);
  const limit = Number(request?.limit || 20);
  const jobs = await client.listJobs({
    limit: Number.isFinite(limit) && limit > 0 ? Math.min(limit, 100) : 20,
    deviceSelector
  });
  return { jobs: jobs.map((job) => mapJob(job, deviceSelector)) };
});

ipcMain.handle("jobs:logs", async (_event, request) => {
  const client = new LocalDaemonClient();
  const jobID = String(request?.jobID || "").trim();
  if (!jobID) {
    throw new Error("Choose a job first.");
  }
  const deviceSelector = deviceSelectorFromRequest(request);
  const result = await readAllJobLogs(client, jobID, deviceSelector);
  return {
    job: mapJob(result.job, deviceSelector),
    text: result.text,
    truncated: result.truncated
  };
});

ipcMain.handle("jobs:cancel", async (_event, request) => {
  const client = new LocalDaemonClient();
  const jobID = String(request?.jobID || "").trim();
  if (!jobID) {
    throw new Error("Choose a job first.");
  }
  const deviceSelector = deviceSelectorFromRequest(request);
  const job = await client.cancelJob(jobID, { deviceSelector });
  return { job: mapJob(job, deviceSelector) };
});

ipcMain.handle("jobs:fetchOutputs", async (_event, request) => {
  const client = new LocalDaemonClient();
  const jobID = String(request?.jobID || "").trim();
  const destination = String(request?.destination || "").trim();
  if (!jobID) {
    throw new Error("Choose a job first.");
  }
  if (!destination) {
    throw new Error("Choose where to save outputs.");
  }
  const deviceSelector = deviceSelectorFromRequest(request);
  return client.fetchArtifacts(jobID, { deviceSelector, destination });
});

ipcMain.handle("outputs:chooseDestination", async (_event, request) => {
  const result = await dialog.showOpenDialog({
    title: "Choose Output Folder",
    buttonLabel: "Save Outputs",
    defaultPath: request?.defaultPath || app.getPath("documents"),
    properties: ["openDirectory", "createDirectory"]
  });
  if (result.canceled || result.filePaths.length === 0) {
    return null;
  }
  return result.filePaths[0];
});

ipcMain.handle("jobs:stop", async (_event, runID) => {
  const record = activeRuns.get(String(runID));
  if (!record) {
    return { stopped: false };
  }

  record.stopped = true;
  record.abortController.abort();

  if (!record.jobID) {
    return { stopped: true, cancelled: false };
  }

  try {
    await record.client.cancelJob(record.jobID, {
      deviceSelector: record.deviceSelector
    });
  } catch (error) {
    sendRunEvent(record.webContents, String(runID), {
      type: "output",
      stream: "stderr",
      text: `\nCancel failed: ${readableError(error)}\n`
    });
  }

  return { stopped: true, cancelled: true };
});

function startDaemonJobStream(webContents, runID, jobRequest, argv) {
  const client = new LocalDaemonClient();
  const abortController = new AbortController();
  const deviceSelector = jobRequest.deviceID === "local" ? "" : jobRequest.deviceID;

  activeRuns.set(runID, {
    abortController,
    client,
    deviceSelector,
    jobID: null,
    stopped: false,
    webContents
  });
  sendRunEvent(webContents, runID, { type: "started" });
  void runDaemonJobStream(runID, jobRequest, argv);
}

async function runDaemonJobStream(runID, jobRequest, argv) {
  const record = activeRuns.get(runID);
  if (!record) {
    return;
  }
  try {
    const submitted = await record.client.submitJob(
      {
        executable: argv[0],
        arguments: argv.slice(1),
        workingDirectory: runWorkingDirectory(jobRequest, record.deviceSelector),
        outputs: jobRequest.outputs,
        deviceSelector: record.deviceSelector
      },
      { signal: record.abortController.signal }
    );
    if (!submitted?.id) {
      throw new Error("ComputeHop daemon returned an empty job.");
    }

    record.jobID = submitted.id;
    sendRunEvent(record.webContents, runID, {
      type: "job",
      jobID: submitted.id,
      state: jobStateLabel(submitted),
      job: mapJob(submitted, record.deviceSelector)
    });

    let afterSequence = 0;
    for (;;) {
      if (record.stopped) {
        throw stoppedError();
      }
      const page = await record.client.readJobLogs(submitted.id, {
        afterSequence,
        deviceSelector: record.deviceSelector,
        limit: 32,
        signal: record.abortController.signal
      });
      for (const item of page.records || []) {
        const sequence = Number(item.sequence || 0);
        if (sequence > afterSequence) {
          afterSequence = sequence;
        }
        sendRunEvent(record.webContents, runID, {
          type: "output",
          stream: item.stream === "JOB_LOG_STREAM_STDERR" ? "stderr" : "stdout",
          text: Buffer.from(item.data || []).toString("utf8")
        });
      }
      if (page.job && jobTerminal(page.job)) {
        sendRunEvent(record.webContents, runID, {
          type: "finished",
          ok: jobSucceeded(page.job),
          text: `Job ${jobStateLabel(page.job)}.`
        });
        return;
      }
      if (page.hasMore) {
        continue;
      }
      await delay(logPollMs, record.abortController.signal);
    }
  } catch (error) {
    const current = activeRuns.get(runID);
    const stopped = current?.stopped || error.code === "ABORTED";
    sendRunEvent(record.webContents, runID, {
      type: "finished",
      ok: false,
      stopped,
      text: stopped ? "Stopped." : readableError(error)
    });
  } finally {
    activeRuns.delete(runID);
  }
}

function sendRunEvent(webContents, runID, payload) {
  if (webContents.isDestroyed()) {
    activeRuns.delete(runID);
    return;
  }
  webContents.send("jobs:event", { runID, ...payload });
}

async function readAllJobLogs(client, jobID, deviceSelector) {
  let afterSequence = 0;
  let text = "";
  let job = null;
  let truncated = false;

  for (let pageIndex = 0; pageIndex < maximumLogPages; pageIndex += 1) {
    const page = await client.readJobLogs(jobID, {
      afterSequence,
      deviceSelector,
      limit: 100
    });
    job = page.job || job;
    for (const item of page.records || []) {
      const sequence = Number(item.sequence || 0);
      if (sequence > afterSequence) {
        afterSequence = sequence;
      }
      text += Buffer.from(item.data || []).toString("utf8");
    }
    if (!page.hasMore) {
      return { job, text, truncated };
    }
  }

  truncated = true;
  return { job, text, truncated };
}

function deviceSelectorFromRequest(request) {
  const deviceID = String(request?.deviceID || "").trim();
  return deviceID === "local" ? "" : deviceID;
}

function daemonRoleFromRequest(request) {
  return normalizeDaemonRole(request?.role, process.platform);
}

function mapJob(value, deviceSelector = "") {
  if (!value) {
    return null;
  }
  const spec = value.spec || {};
  const args = spec.arguments || [];
  const command = [spec.executable, ...args].filter(Boolean).join(" ") || "Task";
  const outputs = spec.outputs || [];
  return {
    id: value.id || "",
    shortID: String(value.id || "").slice(0, 8),
    command,
    executable: spec.executable || "",
    arguments: args,
    outputs,
    state: jobStateLabel(value),
    terminal: jobTerminal(value),
    succeeded: jobSucceeded(value),
    canCancel: !jobTerminal(value),
    canFetchOutputs: jobSucceeded(value) && outputs.length > 0,
    progress: progressLabel(value.progress),
    failure: value.failure?.message || "",
    updated: timestampLabel(value.updatedAtUnixNano),
    created: timestampLabel(value.createdAtUnixNano),
    deviceID: deviceSelector || "local"
  };
}

function progressLabel(progress) {
  if (!progress) {
    return "";
  }
  const phase = String(progress.phase || "")
    .replace(/^JOB_PROGRESS_PHASE_/, "")
    .toLowerCase();
  const completed = Number(progress.completedBytes || 0);
  const total = Number(progress.totalBytes || 0);
  if (total > 0) {
    return `${phase || "progress"} ${Math.round((completed / total) * 100)}%`;
  }
  return phase === "unspecified" ? "" : phase;
}

function mapDevices(result) {
  const devices = [];
  const seen = new Set();

  for (const trusted of result.trustedDevices || []) {
    const device = mapTrustedDevice(trusted);
    const id = device.id;
    if (!id || seen.has(id)) {
      continue;
    }
    seen.add(id);
    devices.push(device);
  }

  for (const nearby of result.devices || []) {
    const id = nearby.presenceId || nearby.instance || nearby.name;
    if (!id || seen.has(id)) {
      continue;
    }
    seen.add(id);
    devices.push({
      name: nearby.name || "Computer",
      id,
      connection: nearby.trustState === "DEVICE_TRUST_STATE_PAIRED" ? "paired" : "not connected",
      role: roleLabel(nearby.role),
      availability: nearby.endpointReady ? "nearby" : "offline",
      trustState: trustLabel(nearby.trustState),
      path: "lan",
      address: [nearby.addresses || [], nearby.port ? [String(nearby.port)] : []].flat().filter(Boolean).join(":"),
      updated: timestampLabel(nearby.lastSeenAtUnixNano)
    });
  }

  return devices;
}

function mapLocalDevice(ping) {
  if (!ping) {
    return null;
  }
  return {
    name: ping.deviceName || "This Mac",
    id: "local",
    deviceID: ping.deviceId || "",
    connection: "active",
    role: roleLabel(ping.role),
    availability: "local",
    trustState: "paired",
    path: "local",
    address: "",
    updated: ""
  };
}

function mapTrustedDevice(trusted) {
  if (!trusted) {
    return null;
  }
  return {
    name: trusted.name || "Computer",
    id: trusted.deviceId || trusted.pairId || trusted.name || "",
    pairID: trusted.pairId || "",
    connection: trusted.trustState === "DEVICE_TRUST_STATE_PAIRED" ? connectionLabel(trusted) : "unpaired",
    role: roleLabel(trusted.role),
    availability: availabilityFromConnectivity(trusted.connectivityState),
    trustState: trustLabel(trusted.trustState),
    path: trusted.connectivityPath || "",
    address: "",
    updated: timestampLabel(trusted.connectivityUpdatedAtUnixNano || trusted.updatedAtUnixNano)
  };
}

function mapPairings(pairings) {
  return (pairings || []).map(mapPairing).filter(Boolean);
}

function mapPairing(pairing) {
  if (!pairing) {
    return null;
  }
  return {
    id: pairing.id || "",
    peerDeviceID: pairing.peerDeviceId || "",
    peerName: pairing.peerName || "Computer",
    peerRole: roleLabel(pairing.peerRole),
    verificationCode: pairing.verificationCode || "",
    direction: pairing.direction === "PAIRING_DIRECTION_INBOUND" ? "inbound" : "outbound",
    state: pairingStateLabel(pairing.state),
    localConfirmed: Boolean(pairing.localConfirmed),
    remoteConfirmed: Boolean(pairing.remoteConfirmed),
    expiresAt: timestampLabel(pairing.expiresAtUnixNano),
    failure: pairing.failure || ""
  };
}

function roleLabel(role) {
  if (role === "DEVICE_ROLE_WORKER") {
    return "worker";
  }
  if (role === "DEVICE_ROLE_ORCHESTRATOR") {
    return "orchestrator";
  }
  return "device";
}

function connectionLabel(device) {
  return device.connectivityState === "CONNECTIVITY_STATE_CONNECTED" ? "active" : "not connected";
}

function trustLabel(state) {
  if (state === "DEVICE_TRUST_STATE_PAIRED") {
    return "paired";
  }
  if (state === "DEVICE_TRUST_STATE_REVOKED") {
    return "revoked";
  }
  return "unpaired";
}

function pairingStateLabel(state) {
  switch (state) {
    case "PAIRING_STATE_WAITING":
      return "waiting";
    case "PAIRING_STATE_PAIRED":
      return "paired";
    case "PAIRING_STATE_REJECTED":
      return "rejected";
    case "PAIRING_STATE_EXPIRED":
      return "expired";
    case "PAIRING_STATE_FAILED":
      return "failed";
    default:
      return "unknown";
  }
}

function availabilityFromConnectivity(state) {
  switch (state) {
    case "CONNECTIVITY_STATE_CONNECTED":
      return "remote";
    case "CONNECTIVITY_STATE_CONNECTING":
      return "connecting";
    default:
      return "offline";
  }
}

function timestampLabel(value) {
  if (!value) {
    return "";
  }
  const numeric = Number(value);
  if (!Number.isFinite(numeric) || numeric <= 0) {
    return "";
  }
  return new Date(Math.floor(numeric / 1_000_000)).toISOString();
}

function readableError(error) {
  return error?.message || "ComputeHop request failed.";
}

function runWorkingDirectory(jobRequest, deviceSelector) {
  if (jobRequest.workingDirectory) {
    return jobRequest.workingDirectory;
  }
  return deviceSelector ? "" : repoRoot;
}

function delay(ms, signal) {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(stoppedError());
      return;
    }
    const timer = setTimeout(resolve, ms);
    if (signal) {
      signal.addEventListener(
        "abort",
        () => {
          clearTimeout(timer);
          reject(stoppedError());
        },
        { once: true }
      );
    }
  });
}

function stoppedError() {
  const error = new Error("Stopped.");
  error.code = "ABORTED";
  return error;
}

function normalizeJobRequest(request) {
  if (!request || typeof request !== "object") {
    throw new Error("Job request is required.");
  }
  return {
    command: String(request.command || "").trim(),
    deviceID: String(request.deviceID || "local").trim(),
    workingDirectory: String(request.workingDirectory || "").trim(),
    outputs: Array.isArray(request.outputs)
      ? request.outputs.map((value) => String(value || "").trim()).filter(Boolean)
      : []
  };
}

function splitCommandLine(input) {
  const tokens = [];
  let current = "";
  let quote = null;
  let escaping = false;

  for (const char of input) {
    if (escaping) {
      current += char;
      escaping = false;
      continue;
    }
    if (char === "\\") {
      escaping = true;
      continue;
    }
    if (quote) {
      if (char === quote) {
        quote = null;
      } else {
        current += char;
      }
      continue;
    }
    if (char === "'" || char === '"') {
      quote = char;
      continue;
    }
    if (/\s/.test(char)) {
      if (current !== "") {
        tokens.push(current);
        current = "";
      }
      continue;
    }
    current += char;
  }

  if (escaping) {
    throw new Error("Command ends with an unfinished escape.");
  }
  if (quote) {
    throw new Error("Command has an unfinished quote.");
  }
  if (current !== "") {
    tokens.push(current);
  }
  return tokens;
}
