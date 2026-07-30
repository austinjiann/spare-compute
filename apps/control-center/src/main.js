const { app, BrowserWindow, dialog, ipcMain, safeStorage, shell } = require("electron");
const { randomUUID } = require("node:crypto");
const path = require("node:path");
const {
  LocalDaemonClient,
  jobStateLabel,
  jobSucceeded,
  jobTerminal
} = require("./local-daemon");
const { startDaemon } = require("./daemon-launcher");
const { installLaunchAgent, resolveDaemonExecutable } = require("./launch-agent-service");
const { launchAgentStatus } = require("./launch-agent-status");
const { splitCommandLine } = require("./command-line");
const {
  planControlCenterTask,
  suggestControlCenterTasks
} = require("./planner-service");
const { appRuntimeInfo, normalizeDaemonRole } = require("./runtime-info");
const { friendlyRunError, remotePreparationMessage } = require("./run-feedback");
const { jobOutputsForPlan, outputValidationForPlan, runWorkingDirectory } = require("./run-request");
const { jobUpdateSignature, nextJobUpdate } = require("./run-progress");
const {
  detachActiveRun,
  detachAllRuns,
  detachRunsForWebContents,
  stopActiveRun,
} = require("./run-lifecycle");
const {
  deviceSelectorFromDeviceID,
  followupDeviceSelector,
  jobDeviceIDForSelector
} = require("./job-routing");
const {
  mapDevices,
  mapLocalDevice,
  mapPairing,
  mapPairings,
  mapTrustedDevice
} = require("./device-mapping");
const { mapJob, progressLabel } = require("./job-summary");
const {
  loadSettings: loadControlCenterSettings,
  saveSettings: saveControlCenterSettings
} = require("./settings-store");
const {
  clearAIPlannerCredentials,
  credentialsStatus,
  loadAIPlannerCredentials,
  plannerConfigFromCredentials,
  saveAIPlannerCredentials
} = require("./ai-credentials");

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

  win.on("close", () => {
    detachRunsForWebContents(activeRuns, win.webContents);
  });
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

app.on("before-quit", () => {
  detachAllRuns(activeRuns);
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

ipcMain.handle("daemon:status", async () => {
  try {
    const client = new LocalDaemonClient();
    const local = await client.ping();
    return {
      ok: true,
      error: "",
      errorCode: "",
      localDevice: mapLocalDevice(local)
    };
  } catch (error) {
    return {
      ok: false,
      error: readableError(error),
      errorCode: error?.code || "",
      localDevice: null
    };
  }
});

ipcMain.handle("app:info", async () => appRuntimeInfo(process.platform));

ipcMain.handle("settings:load", async () => {
  return {
    settings: await loadControlCenterSettings({ userDataPath: app.getPath("userData") })
  };
});

ipcMain.handle("settings:save", async (_event, settings) => {
  return {
    settings: await saveControlCenterSettings(settings, { userDataPath: app.getPath("userData") })
  };
});

ipcMain.handle("aiPlanner:status", async () => {
  const credentials = await loadAIPlannerCredentials({
    userDataPath: app.getPath("userData"),
    safeStorage
  });
  return { status: credentialsStatus(credentials) };
});

ipcMain.handle("aiPlanner:save", async (_event, request) => {
  const credentials = await saveAIPlannerCredentials({
    provider: request?.provider,
    apiKey: request?.apiKey,
    openAIAPIKey: request?.openAIAPIKey,
    baseURL: request?.baseURL,
    model: request?.model
  }, {
    userDataPath: app.getPath("userData"),
    safeStorage,
    preserveExistingAPIKey: true
  });
  return { status: credentialsStatus(credentials) };
});

ipcMain.handle("aiPlanner:clear", async () => {
  await clearAIPlannerCredentials({
    userDataPath: app.getPath("userData")
  });
  return { status: credentialsStatus({}) };
});

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

ipcMain.handle("daemon:launchAgentStatus", async (_event, request) => {
  return { status: await launchAgentStatusForCurrentApp(request) };
});

ipcMain.handle("daemon:installLaunchAgent", async (_event, request) => {
  const status = await launchAgentStatusForCurrentApp(request);
  const daemonRunning = await localDaemonIsRunning();
  try {
    return await installLaunchAgent({
      role: daemonRoleFromRequest(request),
      deviceName: String(request?.deviceName || "").trim(),
      lanOnly: Boolean(request?.lanOnly),
      isPackaged: app.isPackaged,
      resourcesPath: process.resourcesPath,
      controlCenterRoot: path.resolve(__dirname, ".."),
      currentDaemonRunning: daemonRunning,
      status
    });
  } catch (error) {
    return {
      ok: false,
      error: readableError(error),
      status: await safeLaunchAgentStatus(status, request)
    };
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
  const credentials = await loadAIPlannerCredentials({
    userDataPath: app.getPath("userData"),
    safeStorage
  });
  return planControlCenterTask({
    task: request?.task,
    projectRoot: request?.projectRoot || ""
  }, {
    config: plannerConfigFromCredentials(credentials)
  });
});

ipcMain.handle("planner:suggest", async (_event, request) => {
  return suggestControlCenterTasks({
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
  const deviceSelector = jobOperationDeviceSelectorFromRequest(request);
  const result = await readAllJobLogs(client, jobID, deviceSelector);
  return {
    job: mapJob(result.job, jobDeviceIDFromRequest(request)),
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
  const deviceSelector = jobOperationDeviceSelectorFromRequest(request);
  const job = await client.cancelJob(jobID, { deviceSelector });
  return { job: mapJob(job, jobDeviceIDFromRequest(request)) };
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
  const deviceSelector = jobOperationDeviceSelectorFromRequest(request);
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
  return stopActiveRun(activeRuns, String(runID), {
    onCancelError: (record, id, error) => sendRunEvent(record.webContents, id, {
      type: "output",
      stream: "stderr",
      text: `\nCancel failed: ${readableError(error)}\n`
    })
  });
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
    const submitDeviceSelector = record.deviceSelector;
    const workingDirectory = runWorkingDirectory(jobRequest, record.deviceSelector);
    const preparationMessage = remotePreparationMessage({
      deviceName: jobRequest.deviceName,
      deviceSelector: submitDeviceSelector,
      workingDirectory
    });
    if (preparationMessage) {
      sendRunEvent(record.webContents, runID, {
        type: "output",
        stream: "stderr",
        text: `${preparationMessage}\n`
      });
    }
    const preparationProgress = startPreparationProgressPolling({
      record,
      runID,
      jobRequest,
      argv,
      workingDirectory,
      deviceSelector: submitDeviceSelector
    });

    let submitted;
    try {
      submitted = await record.client.submitJob(
        {
          executable: argv[0],
          arguments: argv.slice(1),
          workingDirectory,
          outputs: jobRequest.outputs,
          requiredToolIDs: jobRequest.requiredToolIDs,
          deviceSelector: submitDeviceSelector,
          jobID: runID
        },
        { signal: record.abortController.signal }
      );
    } finally {
      preparationProgress.stop();
      await preparationProgress.done;
    }
    if (!submitted?.id) {
      throw new Error("ComputeHop daemon returned an empty job.");
    }

    const submittedJob = mapRunJob(submitted, submitDeviceSelector, jobRequest);
    let lastJobUpdateSignature = jobUpdateSignature(submittedJob);
    record.jobID = submitted.id;
    record.deviceSelector = followupDeviceSelector(submitDeviceSelector);
    if (submitted.id !== runID) {
      sendRunEvent(record.webContents, runID, {
        type: "job-remove",
        jobID: runID
      });
    }
    sendRunEvent(record.webContents, runID, {
      type: "job",
      jobID: submitted.id,
      state: jobStateLabel(submitted),
      job: submittedJob
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
          job: mapRunJob(page.job, submitDeviceSelector, jobRequest),
          text: `Job ${jobStateLabel(page.job)}.`
        });
        return;
      }
      if (page.job) {
        const currentJob = mapRunJob(page.job, submitDeviceSelector, jobRequest);
        const update = nextJobUpdate(lastJobUpdateSignature, currentJob);
        lastJobUpdateSignature = update.signature || lastJobUpdateSignature;
        if (update.changed) {
          sendRunEvent(record.webContents, runID, {
            type: "job-update",
            state: currentJob.state,
            job: currentJob
          });
        }
      }
      if (page.hasMore) {
        continue;
      }
      await delay(logPollMs, record.abortController.signal);
    }
  } catch (error) {
    const current = activeRuns.get(runID);
    if (!current) {
      return;
    }
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

function startPreparationProgressPolling({
  record,
  runID,
  jobRequest,
  argv,
  workingDirectory,
  deviceSelector
}) {
  let stopped = false;
  let lastProgress = "";
  let lastState = "";
  const pollAbortController = new AbortController();
  const abortPoll = () => {
    pollAbortController.abort();
  };
  record.abortController.signal.addEventListener("abort", abortPoll, { once: true });

  const sendPreparingJob = (progress = "") => {
    const state = "preparing";
    if (state === lastState && progress === lastProgress) {
      return;
    }
    lastState = state;
    lastProgress = progress;
    sendRunEvent(record.webContents, runID, {
      type: "job-update",
      state,
      job: preparationJob({
        runID,
        jobRequest,
        argv,
        workingDirectory,
        deviceSelector,
        progress
      })
    });
  };

  const done = (async () => {
    try {
      sendPreparingJob("");
      for (;;) {
        if (stopped || record.stopped) {
          return;
        }
        try {
          const progress = await record.client.getJobProgress(runID, {
            deviceSelector,
            signal: pollAbortController.signal,
            timeoutMs: 3_000
          });
          const label = progressLabel(progress);
          if (label) {
            sendPreparingJob(label);
          }
        } catch (error) {
          if (error?.code === "ABORTED" || record.stopped || stopped) {
            return;
          }
        }
        try {
          await delay(500, pollAbortController.signal);
        } catch (error) {
          if (error?.code === "ABORTED") {
            return;
          }
          throw error;
        }
      }
    } finally {
      record.abortController.signal.removeEventListener("abort", abortPoll);
    }
  })();

  return {
    stop: () => {
      stopped = true;
      pollAbortController.abort();
    },
    done
  };
}

function preparationJob({
  runID,
  jobRequest,
  argv,
  workingDirectory,
  deviceSelector,
  progress
}) {
  const command = String(jobRequest.command || "").trim() || argv.join(" ");
  return {
    id: runID,
    shortID: runID.slice(0, 8),
    command: command || "Task",
    executable: argv[0] || "",
    arguments: argv.slice(1),
    workingDirectory,
    outputs: jobRequest.outputs || [],
    requiredToolIDs: jobRequest.requiredToolIDs || [],
    state: "preparing",
    terminal: false,
    succeeded: false,
    canCancel: false,
    canFetchOutputs: false,
    progress,
    failure: "",
    updated: "",
    created: "",
    deviceID: jobDeviceIDForSelector(jobRequest.deviceID || deviceSelector || "local"),
    deviceName: String(jobRequest.deviceName || "").trim()
  };
}

function sendRunEvent(webContents, runID, payload) {
  if (webContents.isDestroyed()) {
    detachActiveRun(activeRuns, runID);
    return;
  }
  webContents.send("jobs:event", { runID, ...payload });
}

function mapRunJob(value, deviceSelector, jobRequest) {
  return mapJob(value, deviceSelector, { deviceName: jobRequest?.deviceName || "" });
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
  return deviceSelectorFromDeviceID(deviceID);
}

function jobOperationDeviceSelectorFromRequest(request) {
  return followupDeviceSelector(deviceSelectorFromRequest(request));
}

function jobDeviceIDFromRequest(request) {
  const deviceID = String(request?.deviceID || "").trim();
  return jobDeviceIDForSelector(deviceID || "local");
}

function daemonRoleFromRequest(request) {
  return normalizeDaemonRole(request?.role, process.platform);
}

async function localDaemonIsRunning() {
  try {
    const client = new LocalDaemonClient();
    await client.ping();
    return true;
  } catch {
    return false;
  }
}

async function safeLaunchAgentStatus(fallback, request) {
  try {
    return await launchAgentStatusForCurrentApp(request);
  } catch {
    return fallback;
  }
}

async function launchAgentStatusForCurrentApp(request = {}) {
  const expectedDaemonPath = await preferredLaunchAgentDaemonPath();
  return launchAgentStatus({
    ...(expectedDaemonPath ? { expectedDaemonPath } : {}),
    expectedRole: normalizeDaemonRole(request?.role, process.platform),
    expectedDeviceName: String(request?.deviceName || "").trim()
  });
}

async function preferredLaunchAgentDaemonPath() {
  try {
    return await resolveDaemonExecutable({
      resourcesPath: process.resourcesPath,
      controlCenterRoot: path.resolve(__dirname, "..")
    });
  } catch {
    return "";
  }
}

function readableError(error) {
  return friendlyRunError(error);
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
  const outputValidation = outputValidationForPlan({ outputs: request.outputs });
  if (!outputValidation.ok) {
    throw new Error(outputValidation.error);
  }
  return {
    command: String(request.command || "").trim(),
    deviceID: String(request.deviceID || "local").trim(),
    deviceName: String(request.deviceName || "").trim(),
    workingDirectory: String(request.workingDirectory || "").trim(),
    outputs: jobOutputsForPlan({ outputs: request.outputs }),
    requiredToolIDs: normalizeToolIDs(request.requiredToolIDs || request.requiredToolIds)
  };
}

function normalizeToolIDs(values) {
  if (!Array.isArray(values)) {
    return [];
  }
  const seen = new Set();
  return values
    .map((value) => String(value || "").trim().toLowerCase())
    .filter((value) => value && !/\s|=/.test(value))
    .sort()
    .filter((value) => {
      if (seen.has(value)) {
        return false;
      }
      seen.add(value);
      return true;
    });
}
