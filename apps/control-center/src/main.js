const { app, BrowserWindow, dialog, ipcMain, shell } = require("electron");
const { randomUUID } = require("node:crypto");
const path = require("node:path");
const {
  LocalDaemonClient,
  jobStateLabel,
  jobSucceeded,
  jobTerminal
} = require("./local-daemon");

const repoRoot = path.resolve(__dirname, "..", "..", "..");
const activeRuns = new Map();
const logPollMs = 900;

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
    const result = await client.listDevices();
    return { ok: true, error: "", devices: mapDevices(result) };
  } catch (error) {
    return { ok: false, error: readableError(error), devices: [] };
  }
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
      state: jobStateLabel(submitted)
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

function mapDevices(result) {
  const devices = [];
  const seen = new Set();

  for (const trusted of result.trustedDevices || []) {
    const id = trusted.deviceId || trusted.pairId || trusted.name;
    if (!id || seen.has(id)) {
      continue;
    }
    seen.add(id);
    devices.push({
      name: trusted.name || "Computer",
      id,
      connection: trusted.trustState === "DEVICE_TRUST_STATE_PAIRED" ? connectionLabel(trusted) : "unpaired",
      role: roleLabel(trusted.role),
      availability: availabilityFromConnectivity(trusted.connectivityState),
      path: trusted.connectivityPath || "",
      address: "",
      updated: timestampLabel(trusted.connectivityUpdatedAtUnixNano || trusted.updatedAtUnixNano)
    });
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
      path: "lan",
      address: [nearby.addresses || [], nearby.port ? [String(nearby.port)] : []].flat().filter(Boolean).join(":"),
      updated: timestampLabel(nearby.lastSeenAtUnixNano)
    });
  }

  return devices;
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
    workingDirectory: String(request.workingDirectory || "").trim()
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

function parseDevices(stdout) {
  if (!stdout) {
    return [];
  }

  const lines = stdout
    .split(/\r?\n/)
    .map((line) => line.trimEnd())
    .filter(Boolean);

  if (lines.length <= 1) {
    return [];
  }

  return lines
    .slice(1)
    .map((line) => parseDeviceLine(line))
    .filter(Boolean);
}

function parseDeviceLine(line) {
  if (
    line === "Next:" ||
    line.startsWith("- ") ||
    line.startsWith("No connected or nearby devices.") ||
    line.startsWith("LAN discovery")
  ) {
    return null;
  }

  const columns = line.split(/\s{2,}/).map((column) => column.trim());
  if (columns.length >= 8) {
    return {
      name: columns[0],
      id: columns[1],
      connection: columns[2],
      role: columns[3],
      availability: columns[4],
      path: columns[5],
      address: columns[6],
      updated: columns[7]
    };
  }

  if (columns.length >= 6) {
    return {
      name: columns[0],
      id: columns[1],
      connection: columns[2],
      role: columns[3],
      availability: columns[2],
      path: "",
      address: columns[4],
      updated: columns[5]
    };
  }

  return null;
}
