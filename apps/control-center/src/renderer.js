const state = {
  devices: [],
  localDevice: null,
  pairings: [],
  jobs: [],
  selectedDeviceID: "local",
  selectedJobID: null,
  selectedJobDeviceID: "local",
  selectedJobLogText: "",
  selectedJobLogTruncated: false,
  selectedJobLogFailed: false,
  taskSuggestions: [],
  loadingTaskSuggestions: false,
  runtime: {
    platform: "",
    defaultDaemonRole: "orchestrator",
    daemonRoles: [
      { id: "orchestrator", label: "Control Mac" },
      { id: "worker", label: "Worker" }
    ]
  },
  currentRunID: null,
  plannedTask: null,
  daemonAvailable: false,
  daemonError: "",
  startingDaemon: false,
  loadingJobs: false,
  loadingLogs: false,
  runtimeLoaded: false,
  settingsHydrated: false,
  autoStartAttempted: false,
  userSelectedDevice: false,
  aiPlannerStatus: {
    configured: false,
    source: "",
    encrypted: false,
    model: ""
  },
  settings: loadSettings()
};
let refreshInFlight = false;
let jobsRefreshInFlight = false;
let runInFlight = false;
const pendingActions = new Set();
const { addAutomaticWorkerTarget, concreteDeviceID } = window.computeHopDeviceTargets;
const { shouldAutoStartDaemon } = window.computeHopDaemonAutostart;
const { disallowedWorkMessage, filterAllowedSuggestions } = window.computeHopWorkPolicy;
const {
  jobOutputsForPlan,
  jobStartRequestForPlan,
  runReadinessError
} = window.computeHopRunRequest;

function defaultLocalDevice() {
  return state.localDevice || {
    name: "This Mac",
    id: "local",
    connection: "active",
    role: "device",
    availability: "local",
    path: "local",
    address: "",
    updated: ""
  };
}

const capabilities = [
  ["builds", "Builds"],
  ["tests", "Tests"],
  ["docker", "Docker"],
  ["ai", "AI"],
  ["video", "Video"],
  ["commands", "Exact commands"]
];

document.getElementById("refresh-devices").addEventListener("click", refreshDevices);
document.getElementById("refresh-jobs").addEventListener("click", refreshJobs);
document.getElementById("start-daemon").addEventListener("click", startDaemon);
document.getElementById("run-job").addEventListener("click", runSelectedJob);
document.getElementById("test-device").addEventListener("click", testSelectedDevice);
document.getElementById("command-input").addEventListener("keydown", (event) => {
  if (event.key === "Enter") {
    runSelectedJob();
  }
});
document.getElementById("command-input").addEventListener("input", () => {
  state.plannedTask = null;
  renderPlanPreview();
  renderRunControls();
});
document.getElementById("choose-project").addEventListener("click", chooseProject);
document.getElementById("clear-project").addEventListener("click", clearProject);
document.getElementById("save-ai-planner").addEventListener("click", saveAIPlannerConfig);
document.getElementById("clear-ai-planner").addEventListener("click", clearAIPlannerConfig);

bindCheckbox("lanDiscovery", document.getElementById("lan-discovery"));
bindCheckbox("askBeforeRun", document.getElementById("ask-before-run"));
bindSetting("daemonRole", document.getElementById("daemon-role"));
bindSetting("artifacts", document.getElementById("outputs-input"));

renderCapabilities();
renderTaskSuggestions();
renderPlanPreview();
renderRunControls();
renderDaemonCard();
renderJobs();
renderAIPlannerStatus();
applyStoredRunDeviceSelection();
void hydrateSettings();
void loadAppInfo();
void refreshAIPlannerStatus();
void refreshTaskSuggestions();
refreshDevices();
setInterval(refreshDevices, 5000);

window.computeHop.onJobEvent(handleJobEvent);

async function refreshDevices() {
  if (refreshInFlight) {
    return;
  }

  const button = document.getElementById("refresh-devices");
  const error = document.getElementById("device-error");
  const status = document.getElementById("scan-status");

  if (!state.settings.lanDiscovery) {
    state.devices = [defaultLocalDevice()];
    state.pairings = [];
    error.classList.add("hidden");
    status.textContent = "Nearby discovery off";
    state.daemonAvailable = true;
    state.daemonError = "";
    renderDaemonCard();
    renderDevices();
    renderPairings();
    renderRunControls();
    state.jobs = [];
    renderJobs();
    return;
  }

  refreshInFlight = true;
  button.disabled = true;
  button.textContent = "Scanning";
  status.textContent = "Scanning nearby devices";

  try {
    const response = await window.computeHop.listDevices();
    state.localDevice = response.localDevice || null;
    state.devices = mergeDevices([defaultLocalDevice()], response.devices || []);
    state.pairings = response.pairings || [];
    state.daemonAvailable = response.ok;
    state.daemonError = response.ok ? "" : response.error || "Start ComputeHop to discover nearby devices.";
    error.classList.add("hidden");
    error.textContent = "";
    status.textContent = response.ok ? scanSummary(state.devices) : "Discovery unavailable";
  } catch (err) {
    state.localDevice = null;
    state.devices = [defaultLocalDevice()];
    state.pairings = [];
    state.daemonAvailable = false;
    state.daemonError = err.message || "Start ComputeHop to discover nearby devices.";
    error.classList.add("hidden");
    error.textContent = "";
    status.textContent = "Discovery unavailable";
  } finally {
    renderDaemonCard();
    renderDevices();
    renderPairings();
    button.disabled = false;
    button.textContent = "Refresh";
    refreshInFlight = false;
    if (state.daemonAvailable) {
      void refreshJobs();
    }
    maybeAutoStartDaemon();
  }
}

async function startDaemon() {
  if (state.startingDaemon) {
    return;
  }
  const button = document.getElementById("start-daemon");
  const error = document.getElementById("device-error");
  state.startingDaemon = true;
  button.disabled = true;
  button.textContent = "Starting";
  renderDaemonCard();
  try {
    const result = await window.computeHop.startDaemon({ role: state.settings.daemonRole });
    if (!result.ok) {
      throw new Error(result.error || "Could not start ComputeHop.");
    }
    state.daemonAvailable = true;
    state.daemonError = "";
    error.classList.add("hidden");
    await refreshDevices();
  } catch (err) {
    state.daemonAvailable = false;
    state.daemonError = err.message || "Could not start ComputeHop.";
    error.classList.remove("hidden");
    error.textContent = state.daemonError;
  } finally {
    state.startingDaemon = false;
    button.disabled = false;
    button.textContent = "Start";
    renderDaemonCard();
  }
}

function renderDaemonCard() {
  const card = document.getElementById("daemon-card");
  const button = document.getElementById("start-daemon");
  const role = document.getElementById("daemon-role");
  card.classList.toggle("hidden", state.daemonAvailable || !state.settings.lanDiscovery);
  button.disabled = state.startingDaemon;
  button.textContent = state.startingDaemon ? "Starting" : "Start";
  role.disabled = state.startingDaemon;
}

async function loadAppInfo() {
  if (!window.computeHop.appInfo) {
    return;
  }
  try {
    const info = await window.computeHop.appInfo();
    state.runtime = {
      ...state.runtime,
      ...info,
      daemonRoles: Array.isArray(info?.daemonRoles) && info.daemonRoles.length > 0
        ? info.daemonRoles
        : state.runtime.daemonRoles
    };
    applyDaemonRoleOptions();
  } catch {
    applyDaemonRoleOptions();
  } finally {
    state.runtimeLoaded = true;
    maybeAutoStartDaemon();
  }
}

function applyDaemonRoleOptions() {
  const role = document.getElementById("daemon-role");
  const roles = state.runtime.daemonRoles;
  role.replaceChildren(...roles.map((candidate) => {
    const option = document.createElement("option");
    option.value = candidate.id;
    option.textContent = candidate.label;
    return option;
  }));
  if (!roles.some((candidate) => candidate.id === state.settings.daemonRole)) {
    state.settings.daemonRole = state.runtime.defaultDaemonRole || roles[0].id;
    saveSettings();
  }
  role.value = state.settings.daemonRole;
  renderDaemonCard();
}

async function refreshJobs() {
  if (jobsRefreshInFlight || !state.daemonAvailable) {
    return;
  }

  const selected = selectedDevice();
  if (!selected || !canRunOn(selected)) {
    state.jobs = [];
    renderJobs();
    return;
  }

  jobsRefreshInFlight = true;
  state.loadingJobs = true;
  renderJobs();
  try {
    const response = await window.computeHop.listJobs({
      deviceID: concreteDeviceID(selected),
      limit: 12
    });
    state.jobs = (response.jobs || []).filter(Boolean);
    const stillSelected = state.jobs.some((job) => job.id === state.selectedJobID);
    if (!stillSelected) {
      state.selectedJobID = null;
      state.selectedJobLogText = "";
      state.selectedJobLogTruncated = false;
      state.selectedJobLogFailed = false;
    }
    document.getElementById("job-error").classList.add("hidden");
  } catch (err) {
    state.jobs = [];
    const error = document.getElementById("job-error");
    error.classList.remove("hidden");
    error.textContent = err.message || "Could not load jobs.";
  } finally {
    state.loadingJobs = false;
    jobsRefreshInFlight = false;
    renderJobs();
  }
}

function renderJobs() {
  const list = document.getElementById("job-list");
  const refresh = document.getElementById("refresh-jobs");
  list.replaceChildren();
  refresh.disabled = state.loadingJobs || !state.daemonAvailable;
  refresh.textContent = state.loadingJobs ? "Loading" : "Refresh";

  if (!state.daemonAvailable) {
    list.append(emptyJobsRow("Start ComputeHop to see jobs."));
    renderSelectedJobLog();
    return;
  }

  const selected = selectedDevice();
  if (!selected || !canRunOn(selected)) {
    list.append(emptyJobsRow(jobsUnavailableMessage(selected)));
    renderSelectedJobLog();
    return;
  }

  if (state.jobs.length === 0) {
    list.append(emptyJobsRow("No jobs yet."));
    renderSelectedJobLog();
    return;
  }

  state.jobs.forEach((job) => {
    const row = document.createElement("div");
    row.className = "job-row";
    row.classList.toggle("selected", job.id === state.selectedJobID);

    const copy = document.createElement("span");
    copy.className = "job-copy";
    copy.innerHTML = `
      <strong>${escapeHTML(jobTitle(job))}</strong>
      <small>${escapeHTML(jobDetail(job))}</small>
    `;

    const actions = document.createElement("span");
    actions.className = "job-actions";

    const logs = document.createElement("button");
    logs.className = "row-button muted";
    logs.textContent = "Logs";
    logs.disabled = state.loadingLogs;
    logs.addEventListener("click", (event) => {
      event.stopPropagation();
      void showJobLogs(job);
    });
    actions.append(logs);

    if (job.canFetchOutputs) {
      const outputs = document.createElement("button");
      outputs.className = "row-button";
      outputs.textContent = "Outputs";
      outputs.addEventListener("click", (event) => {
        event.stopPropagation();
        void fetchJobOutputs(job);
      });
      actions.append(outputs);
    }

    if (job.canCancel) {
      const cancel = document.createElement("button");
      cancel.className = "row-button muted";
      cancel.textContent = "Stop";
      cancel.addEventListener("click", (event) => {
        event.stopPropagation();
        void cancelListedJob(job);
      });
      actions.append(cancel);
    }

    row.addEventListener("click", () => {
      void showJobLogs(job);
    });
    row.append(copy, actions);
    list.append(row);
  });

  renderSelectedJobLog();
}

function emptyJobsRow(message) {
  const row = document.createElement("div");
  row.className = "empty-row";
  row.textContent = message;
  return row;
}

async function showJobLogs(job) {
  state.selectedJobID = job.id;
  state.selectedJobDeviceID = job.deviceID || concreteDeviceID(selectedDevice());
  state.loadingLogs = true;
  renderJobs();
  try {
    const response = await window.computeHop.readJobLogs({
      jobID: job.id,
      deviceID: state.selectedJobDeviceID
    });
    if (response.job) {
      upsertJob(response.job);
    }
    state.selectedJobLogText = response.text || noLogsMessage(response.job || job);
    state.selectedJobLogTruncated = Boolean(response.truncated);
    state.selectedJobLogFailed = false;
    renderSelectedJobLog();
  } catch (err) {
    state.selectedJobLogText = err.message || "Could not load logs.";
    state.selectedJobLogTruncated = false;
    state.selectedJobLogFailed = true;
    renderSelectedJobLog();
  } finally {
    state.loadingLogs = false;
    renderJobs();
  }
}

async function cancelListedJob(job) {
  try {
    const response = await window.computeHop.cancelJob({
      jobID: job.id,
      deviceID: job.deviceID || concreteDeviceID(selectedDevice())
    });
    if (response.job) {
      upsertJob(response.job);
    }
    showJobOutput(`Stopped ${job.shortID || job.id}.`, true);
  } catch (err) {
    showJobOutput(err.message || "Could not stop job.", false);
  } finally {
    renderJobs();
    void refreshJobs();
  }
}

async function fetchJobOutputs(job) {
  try {
    const destination = await window.computeHop.chooseOutputDestination({
      defaultPath: state.settings.projectRoot || ""
    });
    if (!destination) {
      return;
    }
    const result = await window.computeHop.fetchOutputs({
      jobID: job.id,
      deviceID: job.deviceID || concreteDeviceID(selectedDevice()),
      destination
    });
    const restored = Number(result.restoredFileCount || 0);
    const conflicts = Number(result.conflictFileCount || 0);
    const conflictText = conflicts > 0 ? ` ${conflicts} conflict${conflicts === 1 ? "" : "s"} kept aside.` : "";
    showJobOutput(`Saved ${restored} output${restored === 1 ? "" : "s"} to ${result.destination || destination}.${conflictText}`, true);
  } catch (err) {
    showJobOutput(friendlyOutputError(job, err), false);
  }
}

function renderSelectedJobLog() {
  const output = document.getElementById("job-log");
  if (!state.selectedJobID) {
    output.classList.add("hidden");
    output.textContent = "";
    return;
  }

  output.classList.remove("hidden", "failure", "success");
  if (state.selectedJobLogFailed) {
    output.classList.add("failure");
  }
  if (state.selectedJobLogText) {
    output.textContent = state.selectedJobLogTruncated
      ? `${state.selectedJobLogText}\n\n…truncated`
      : state.selectedJobLogText;
    output.scrollTop = output.scrollHeight;
    return;
  }

  if (state.loadingLogs) {
    output.textContent = "Loading logs…";
    return;
  }

  const job = state.jobs.find((value) => value.id === state.selectedJobID);
  output.textContent = job ? noLogsMessage(job) : "";
}

function upsertJob(job) {
  if (!job || !job.id) {
    return;
  }
  const index = state.jobs.findIndex((value) => value.id === job.id);
  if (index >= 0) {
    state.jobs[index] = job;
  } else {
    state.jobs.unshift(job);
  }
}

function jobTitle(job) {
  const parts = String(job.command || "Task").split(/\s+/);
  const executable = parts.shift() || "Task";
  const name = executable.split("/").filter(Boolean).pop() || executable;
  return [name, ...parts].join(" ");
}

function jobDetail(job) {
  const bits = [job.state || "unknown"];
  if (job.progress) {
    bits.push(job.progress);
  }
  if (job.outputs?.length) {
    bits.push(`returns ${job.outputs.join(", ")}`);
  }
  if (job.failure) {
    bits.push(job.failure);
  }
  return bits.join(" · ");
}

function noLogsMessage(job) {
  if (!job) {
    return "No logs loaded.";
  }
  if (job.terminal) {
    return `No output was captured for ${job.shortID || job.id}.`;
  }
  return `No output captured yet for ${job.shortID || job.id}.`;
}

function friendlyOutputError(job, err) {
  const message = String(err?.message || "Could not fetch outputs.");
  const lower = message.toLowerCase();
  if (lower.includes("job artifacts are not ready") && lower.includes("succeeded")) {
    return `No declared outputs are available for ${job.shortID || job.id}. Add files to “Bring back” before running.`;
  }
  if (lower.includes("job artifacts are not ready")) {
    return `Outputs for ${job.shortID || job.id} are not ready yet. Wait for the job to succeed.`;
  }
  if (lower.includes("artifacts not found")) {
    return `Outputs were not found for ${job.shortID || job.id}.`;
  }
  return message;
}

function mergeDevices(localDevices, remoteDevices) {
  const seen = new Set();
  const deduped = [...localDevices, ...remoteDevices].filter((device) => {
    const key = device.id || device.name;
    if (seen.has(key)) {
      return false;
    }
    seen.add(key);
    return true;
  });
  const configured = deduped.map((device) => ({
    ...device,
    synced: isDeviceSynced(device)
  }));
  const result = addAutomaticWorkerTarget(configured, state.selectedDeviceID, {
    preferAutomaticWorker: !state.userSelectedDevice,
    preserveUnavailableSelection: state.userSelectedDevice
  });
  const devices = result.devices;
  state.selectedDeviceID = result.selectedDeviceID;
  if (!state.userSelectedDevice && !devices.some((device) => device.id === state.selectedDeviceID)) {
    state.selectedDeviceID = "local";
  }
  rememberAvailableSelectedDeviceName(devices);
  return devices;
}

function rememberAvailableSelectedDeviceName(devices) {
  if (!state.userSelectedDevice) {
    return;
  }
  const selected = devices.find((device) => device.id === state.selectedDeviceID);
  if (!selected) {
    return;
  }
  const nextName = runDeviceSelectionName(state.selectedDeviceID, selected);
  if (nextName && state.settings.selectedDeviceName !== nextName) {
    state.settings.selectedDeviceName = nextName;
    saveSettings();
  }
}

function devicesForDisplay() {
  const devices = [...state.devices];
  const selectedID = state.selectedDeviceID;
  if (!selectedID || selectedID === "local" || devices.some((device) => device.id === selectedID)) {
    return devices;
  }

  const placeholder = unavailableSelectedDevice();
  const localIndex = devices.findIndex((device) => device.id === "local");
  if (localIndex >= 0) {
    devices.splice(localIndex + 1, 0, placeholder);
  } else {
    devices.unshift(placeholder);
  }
  return devices;
}

function unavailableSelectedDevice() {
  const id = state.selectedDeviceID || "local";
  const isAuto = id === "auto";
  return {
    id,
    name: state.settings.selectedDeviceName || (isAuto ? "Auto worker" : "Selected worker"),
    detail: isAuto ? "Waiting for a connected worker" : "Waiting for this worker",
    role: "worker",
    connection: "offline",
    availability: "offline",
    trustState: "paired",
    path: "pending",
    synced: true,
    unavailableSelection: true
  };
}

function renderDevices() {
  const list = document.getElementById("device-list");
  list.replaceChildren();

  devicesForDisplay().forEach((device) => {
    const row = document.createElement("div");
    row.className = "device-row";
    row.classList.toggle("selected", device.id === state.selectedDeviceID);
    row.addEventListener("click", () => {
      if (!canSelectDeviceForRun(device)) {
        return;
      }
      selectRunDevice(device);
    });

    const icon = document.createElement("span");
    icon.className = `device-icon ${deviceKind(device)}`;

    const copy = document.createElement("span");
    copy.className = "device-copy";
    copy.innerHTML = `<strong>${escapeHTML(device.name)}</strong><small>${deviceLabel(device)}</small>`;

    const meta = document.createElement("span");
    meta.className = "device-meta";
    meta.textContent = device.id === "local" ? "Here" : device.id === "auto" ? "Auto" : availabilityLabel(device);

    const action = deviceActionButton(device);

    row.append(icon, copy, meta, action);
    list.append(row);
  });
  renderCapabilities();
  renderRunControls();
}

function selectRunDevice(deviceID) {
  const device = typeof deviceID === "object" ? deviceID : state.devices.find((candidate) => candidate.id === deviceID);
  const id = typeof deviceID === "object" ? deviceID.id : deviceID;
  state.userSelectedDevice = true;
  setRunDeviceSelection(id, device);
  state.selectedJobID = null;
  state.selectedJobLogText = "";
  state.selectedJobLogTruncated = false;
  state.selectedJobLogFailed = false;
  state.jobs = [];
  saveSettings();
  renderDevices();
  renderJobs();
  void refreshTaskSuggestions();
  void refreshJobs();
}

function setRunDeviceSelection(deviceID, device) {
  const id = String(deviceID || "local").trim() || "local";
  state.selectedDeviceID = id;
  state.settings.selectedDeviceID = id;
  state.settings.selectedDeviceName = runDeviceSelectionName(id, device);
  state.selectedJobDeviceID = id;
}

function runDeviceSelectionName(deviceID, device) {
  const id = String(deviceID || "local").trim();
  if (device?.name) {
    return String(device.name);
  }
  if (id === "local") {
    return "This Mac";
  }
  if (id === "auto") {
    return "Auto worker";
  }
  return state.settings.selectedDeviceName || "Selected worker";
}

function deviceActionButton(device) {
  const action = document.createElement("button");
  action.className = "row-button";

  if (device.unavailableSelection) {
    action.textContent = "Use Mac";
    action.addEventListener("click", (event) => {
      event.stopPropagation();
      selectRunDevice(defaultLocalDevice());
    });
    return action;
  }

  if (device.id === "local" || device.id === "auto") {
    action.textContent = device.id === state.selectedDeviceID ? "Selected" : "Use";
    action.disabled = device.id === state.selectedDeviceID;
    action.addEventListener("click", (event) => {
      event.stopPropagation();
      selectRunDevice(device);
    });
    return action;
  }

  const key = `device:${device.id}`;
  action.disabled = pendingActions.has(key);
  if (isPairable(device)) {
    action.textContent = pendingActions.has(key) ? "Connecting" : "Connect";
    action.addEventListener("click", (event) => {
      event.stopPropagation();
      void connectDevice(device);
    });
    return action;
  }

  if (isUnpaired(device)) {
    action.textContent = availabilityLabel(device) === "Nearby" ? "Connect" : "Unavailable";
    action.disabled = true;
    return action;
  }

  if (isSyncManagedDevice(device)) {
    const actions = document.createElement("span");
    actions.className = "device-actions";

    const sync = document.createElement("button");
    sync.className = "row-button";
    sync.textContent = device.synced === false ? "Enable" : "Disable";
    sync.addEventListener("click", (event) => {
      event.stopPropagation();
      toggleDeviceSync(device);
    });

    const forget = document.createElement("button");
    forget.className = "row-button muted";
    forget.textContent = pendingActions.has(key) ? "Forgetting" : "Forget";
    forget.disabled = pendingActions.has(key);
    forget.addEventListener("click", (event) => {
      event.stopPropagation();
      void forgetDevice(device);
    });

    actions.append(sync, forget);
    return actions;
  }

  action.textContent = pendingActions.has(key) ? "Forgetting" : "Forget";
  action.classList.add("muted");
  action.addEventListener("click", (event) => {
    event.stopPropagation();
    void forgetDevice(device);
  });
  return action;
}

function renderPairings() {
  const list = document.getElementById("pairing-list");
  const activePairings = state.pairings.filter((pairing) => pairing.state === "waiting" || pairing.state === "failed");
  list.replaceChildren();
  list.classList.toggle("hidden", activePairings.length === 0);

  activePairings.forEach((pairing) => {
    const card = document.createElement("div");
    card.className = "pairing-card";

    const copy = document.createElement("div");
    copy.className = "pairing-copy";
    const status = pairing.localConfirmed
      ? "Waiting for the other computer"
      : "Compare this code on both computers";
    copy.innerHTML = `
      <strong>${escapeHTML(pairing.peerName)}</strong>
      <code>${escapeHTML(pairing.verificationCode)}</code>
      <small>${escapeHTML(status)}</small>
    `;

    const actions = document.createElement("div");
    actions.className = "pairing-actions";

    const reject = document.createElement("button");
    reject.className = "row-button muted";
    reject.textContent = "Reject";
    reject.disabled = pendingActions.has(`pairing:${pairing.id}`);
    reject.addEventListener("click", () => {
      void rejectPairing(pairing);
    });

    actions.append(reject);
    if (!pairing.localConfirmed) {
      const confirm = document.createElement("button");
      confirm.className = "row-button primary";
      confirm.textContent = "Confirm";
      confirm.disabled = pendingActions.has(`pairing:${pairing.id}`);
      confirm.addEventListener("click", () => {
        void confirmPairing(pairing);
      });
      actions.append(confirm);
    }

    card.append(copy, actions);
    list.append(card);
  });
}

async function connectDevice(device) {
  await performDeviceAction(`device:${device.id}`, async () => {
    await window.computeHop.connectDevice(device.id);
    await refreshDevices();
  });
}

async function forgetDevice(device) {
  await performDeviceAction(`device:${device.id}`, async () => {
    await window.computeHop.forgetDevice(device.id);
    delete state.settings.syncedDevices[device.id];
    if (state.settings.deviceCapabilities) {
      delete state.settings.deviceCapabilities[device.id];
    }
    if (state.selectedDeviceID === device.id) {
      setRunDeviceSelection("local", defaultLocalDevice());
    }
    saveSettings();
    await refreshDevices();
  });
}

function toggleDeviceSync(device) {
  if (!isSyncManagedDevice(device)) {
    return;
  }
  const enabled = device.synced === false;
  state.settings.syncedDevices[device.id] = enabled;
  if (!enabled && (state.selectedDeviceID === device.id || state.selectedDeviceID === "auto")) {
    setRunDeviceSelection("local", defaultLocalDevice());
    state.selectedJobID = null;
    state.selectedJobLogText = "";
    state.selectedJobLogTruncated = false;
    state.selectedJobLogFailed = false;
    state.jobs = [];
  }
  saveSettings();
  recomputeDeviceTargets();
  renderJobs();
  if (state.daemonAvailable) {
    void refreshJobs();
  }
}

function recomputeDeviceTargets() {
  const remoteDevices = state.devices.filter((device) => device.id !== "local" && device.id !== "auto");
  state.devices = mergeDevices([defaultLocalDevice()], remoteDevices);
  renderDevices();
  renderRunControls();
}

async function confirmPairing(pairing) {
  await performDeviceAction(`pairing:${pairing.id}`, async () => {
    await window.computeHop.confirmPairing(pairing.id);
    await refreshDevices();
  });
}

async function rejectPairing(pairing) {
  await performDeviceAction(`pairing:${pairing.id}`, async () => {
    await window.computeHop.rejectPairing(pairing.id);
    await refreshDevices();
  });
}

async function performDeviceAction(key, action) {
  const error = document.getElementById("device-error");
  pendingActions.add(key);
  renderDevices();
  renderPairings();
  try {
    await action();
    error.classList.add("hidden");
  } catch (err) {
    error.classList.remove("hidden");
    error.textContent = err.message || "Device action failed.";
  } finally {
    pendingActions.delete(key);
    renderDevices();
    renderPairings();
  }
}

async function chooseProject() {
  const selected = await window.computeHop.chooseProject();
  if (!selected) {
    return;
  }
  state.settings.projectRoot = selected;
  state.plannedTask = null;
  saveSettings();
  await refreshTaskSuggestions();
  renderPlanPreview();
  renderRunControls();
}

function clearProject() {
  if (!state.settings.projectRoot) {
    return;
  }
  state.settings.projectRoot = "";
  state.plannedTask = null;
  state.taskSuggestions = [];
  state.loadingTaskSuggestions = false;
  saveSettings();
  renderTaskSuggestions();
  renderPlanPreview();
  renderRunControls();
}

async function refreshTaskSuggestions() {
  if (!window.computeHop.suggestTasks) {
    state.taskSuggestions = [];
    renderTaskSuggestions();
    return;
  }

  const projectRoot = state.settings.projectRoot || "";
  if (!projectRoot) {
    state.taskSuggestions = [];
    state.loadingTaskSuggestions = false;
    renderTaskSuggestions();
    return;
  }

  const expectedProjectRoot = projectRoot;
  state.loadingTaskSuggestions = true;
  renderTaskSuggestions();
  try {
    const response = await window.computeHop.suggestTasks({ projectRoot });
    if ((state.settings.projectRoot || "") !== expectedProjectRoot) {
      return;
    }
    state.taskSuggestions = allowedSuggestions(response?.suggestions || []);
  } catch {
    if ((state.settings.projectRoot || "") === expectedProjectRoot) {
      state.taskSuggestions = [];
    }
  } finally {
    if ((state.settings.projectRoot || "") === expectedProjectRoot) {
      state.loadingTaskSuggestions = false;
      renderTaskSuggestions();
    }
  }
}

async function runSelectedJob() {
  if (runInFlight) {
    await stopCurrentJob();
    return;
  }

  const selected = selectedDevice();
  const task = document.getElementById("command-input").value.trim();

  if (!task) {
    showJobOutput("Enter something to run.", false);
    return;
  }

  const planned = await plannedCommandFor(task);
  if (!planned) {
    return;
  }

  await startPlannedJob(planned, selected);
}

async function testSelectedDevice() {
  if (runInFlight) {
    return;
  }

  const selected = selectedDevice();
  const planned = {
    source: "test connection",
    title: "Test connection",
    command: "hostname",
    detail: "Runs on the selected computer and prints its hostname.",
    requiresProject: false,
    projectRoot: "",
    ignoreDeclaredOutputs: true
  };
  state.plannedTask = planned;
  renderPlanPreview();
  await startPlannedJob(planned, selected);
}

async function startPlannedJob(planned, selected) {
  const output = document.getElementById("job-output");
  const button = document.getElementById("run-job");
  const outputs = jobOutputsForPlan({ plan: planned, outputs: declaredOutputs() });
  const readinessError = validateRunReadiness(selected, planned, outputs);
  if (readinessError) {
    showJobOutput(readinessError, false);
    return;
  }

  runInFlight = true;
  state.currentRunID = null;
  button.disabled = true;
  button.textContent = "Starting";
  const jobRequest = jobStartRequestForPlan({
    plan: planned,
    device: selected,
    projectRoot: state.settings.projectRoot,
    outputs
  });
  output.classList.remove("hidden");
  output.classList.remove("success", "failure");
  output.textContent = `Running ${planned.command} on ${jobRequest.deviceName || selected.name}…`;

  try {
    const result = await window.computeHop.startJob(jobRequest);
    state.currentRunID = result.runID;
    button.disabled = false;
    renderRunControls();
  } catch (error) {
    showJobOutput(error.message || "Run failed.", false);
    runInFlight = false;
    state.currentRunID = null;
    renderRunControls();
  }
}

function validateRunReadiness(selected, planned, outputs = []) {
  const policyError = disallowedWorkMessage(planned, capabilitiesForSelectedDevice());
  return runReadinessError({
    device: selected,
    canRun: Boolean(selected && canRunOn(selected)),
    plan: planned,
    projectRoot: state.settings.projectRoot,
    outputs,
    policyError
  });
}

function declaredOutputs() {
  const input = document.getElementById("outputs-input");
  const seen = new Set();
  if (state.settings.artifacts !== input.value) {
    state.settings.artifacts = input.value;
    saveSettings();
  }
  return input.value
    .split(",")
    .map((value) => value.trim())
    .filter(Boolean)
    .filter((value) => {
      if (seen.has(value)) {
        return false;
      }
      seen.add(value);
      return true;
    });
}

async function plannedCommandFor(task) {
  if (state.settings.askBeforeRun) {
    if (planMatchesInput(task)) {
      return state.plannedTask;
    }
    await previewPlan(task);
    return null;
  }

  const result = await createPlan(task);
  if (!result) {
    return null;
  }
  state.plannedTask = result;
  renderPlanPreview();
  return result;
}

async function previewPlan(task) {
  const plan = await createPlan(task);
  if (!plan) {
    return;
  }
  state.plannedTask = plan;
  renderPlanPreview();
  renderRunControls();
}

async function createPlan(task) {
  try {
    const response = await window.computeHop.planTask({
      task,
      projectRoot: state.settings.projectRoot || ""
    });
    if (!response.ok) {
      showJobOutput(plannerErrorMessage(response), false);
      state.plannedTask = null;
      renderPlanPreview();
      renderRunControls();
      return null;
    }
    return {
      source: task,
      ...response.plan,
      projectRoot: state.settings.projectRoot || ""
    };
  } catch (error) {
    showJobOutput(error.message || "Could not plan that task.", false);
    state.plannedTask = null;
    renderPlanPreview();
    renderRunControls();
    return null;
  }
}

function plannerErrorMessage(response) {
  const base = response?.error || "Could not plan that task.";
  const aiError = response?.aiPlanner?.attempted && response.aiPlanner.error
    ? ` AI planner: ${response.aiPlanner.error}`
    : "";
  return `${base}${aiError}`;
}

function renderPlanPreview() {
  const preview = document.getElementById("plan-preview");
  const title = document.getElementById("plan-title");
  const detail = document.getElementById("plan-detail");
  const command = document.getElementById("plan-command");
  const plan = state.plannedTask;

  preview.classList.toggle("hidden", !plan);
  if (!plan) {
    title.textContent = "Plan";
    detail.textContent = "";
    command.textContent = "";
    return;
  }
  title.textContent = plan.title || "Planned command";
  detail.textContent = [
    plan.detail || "",
    Array.isArray(plan.outputs) && plan.outputs.length > 0
      ? `Returns ${plan.outputs.join(", ")}`
      : ""
  ].filter(Boolean).join(" · ");
  command.textContent = plan.command || "";
}

function renderTaskSuggestions() {
  const container = document.getElementById("task-suggestions");
  container.replaceChildren();
  const suggestions = state.taskSuggestions || [];
  container.classList.toggle("hidden", !state.loadingTaskSuggestions && suggestions.length === 0);

  if (state.loadingTaskSuggestions) {
    const pill = document.createElement("span");
    pill.className = "suggestion-status";
    pill.textContent = "Finding project tasks…";
    container.append(pill);
    return;
  }

  suggestions.forEach((suggestion) => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "suggestion-chip";
    button.textContent = suggestion.label || suggestion.title || suggestion.task;
    button.title = suggestion.command || "";
    button.addEventListener("click", () => {
      applyTaskSuggestion(suggestion);
    });
    container.append(button);
  });
}

function applyTaskSuggestion(suggestion) {
  const input = document.getElementById("command-input");
  const source = suggestion.task || suggestion.title || "";
  input.value = source;
  state.plannedTask = {
    source,
    title: suggestion.title || suggestion.label || "Planned command",
    command: suggestion.command || "",
    detail: suggestion.detail || "",
    requiresProject: Boolean(suggestion.requiresProject),
    outputs: Array.isArray(suggestion.outputs) ? suggestion.outputs : [],
    projectRoot: state.settings.projectRoot || "",
    detected: suggestion.detected || []
  };
  renderPlanPreview();
  renderRunControls();
  input.focus();
}

async function stopCurrentJob() {
  const runID = state.currentRunID;
  if (!runID) {
    return;
  }
  const button = document.getElementById("run-job");
  button.disabled = true;
  button.textContent = "Stopping";
  try {
    const result = await window.computeHop.stopJob(runID);
    if (result?.stopped === false && state.currentRunID === runID) {
      runInFlight = false;
      state.currentRunID = null;
      appendJobOutput("\nRun already finished or is no longer tracked.\n");
      renderRunControls();
      void refreshJobs();
    }
  } catch (error) {
    if (state.currentRunID === runID) {
      appendJobOutput(`\nStop failed: ${error.message || "Could not stop job."}\n`);
      button.disabled = false;
      renderRunControls();
    }
  }
}

function handleJobEvent(event) {
  if (!event || event.runID !== state.currentRunID) {
    return;
  }

  if (event.type === "started") {
    appendJobOutput("\n");
    renderRunControls();
    return;
  }

  if (event.type === "output") {
    appendJobOutput(event.text || "");
    return;
  }

  if (event.type === "job") {
    if (event.job) {
      upsertJob(event.job);
      renderJobs();
    }
    appendJobOutput(`\nJob ${event.jobID}\n`);
    return;
  }

  if (event.type === "finished") {
    if (event.job) {
      upsertJob(event.job);
      renderJobs();
    }
    if (event.text) {
      appendJobOutput(`\n${event.text}`);
    }
    const output = document.getElementById("job-output");
    output.classList.toggle("success", Boolean(event.ok));
    output.classList.toggle("failure", !event.ok);
    runInFlight = false;
    state.currentRunID = null;
    renderRunControls();
    void refreshJobs();
  }
}

function appendJobOutput(text) {
  const output = document.getElementById("job-output");
  output.classList.remove("hidden");
  output.textContent += text;
  output.scrollTop = output.scrollHeight;
}

function showJobOutput(message, ok) {
  const output = document.getElementById("job-output");
  output.classList.remove("hidden", "success", "failure");
  output.classList.add(ok ? "success" : "failure");
  output.textContent = message;
}

function renderRunControls() {
  const selected = selectedDevice();
  const target = document.getElementById("run-target");
  const projectLabel = document.getElementById("project-label");
  const clearProjectButton = document.getElementById("clear-project");
  const runButton = document.getElementById("run-job");
  const testButton = document.getElementById("test-device");
  const task = document.getElementById("command-input").value.trim();

  target.textContent = runTargetLabel(selected);
  projectLabel.textContent = state.settings.projectRoot
    ? shortPath(state.settings.projectRoot)
    : "No project";
  clearProjectButton.classList.toggle("hidden", !state.settings.projectRoot);
  if (runInFlight) {
    runButton.textContent = "Stop";
  } else if (state.settings.askBeforeRun && task && !planMatchesInput(task)) {
    runButton.textContent = "Plan";
  } else {
    runButton.textContent = "Run";
  }
  testButton.textContent = selected && selected.id === "local" ? "Test Mac" : "Test worker";
  testButton.disabled = runInFlight || !selected || !canRunOn(selected);
  runButton.disabled = !runInFlight && (!selected || !canRunOn(selected));
}

function jobsUnavailableMessage(device) {
  if (device?.unavailableSelection) {
    return `${device.name} is not available yet. Keep the worker app open, or switch to This Mac.`;
  }
  return "Choose This Mac or a connected worker.";
}

function planMatchesInput(task) {
  return (
    state.plannedTask &&
    state.plannedTask.source === task &&
    state.plannedTask.projectRoot === (state.settings.projectRoot || "") &&
    state.plannedTask.command
  );
}

function renderCapabilities() {
  const grid = document.getElementById("capability-grid");
  grid.replaceChildren();
  const selectedCapabilities = capabilitiesForSelectedDevice();

  capabilities.forEach(([id, title]) => {
    const label = document.createElement("label");
    label.className = "check-card";

    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.checked = selectedCapabilities[id] !== false;
    checkbox.addEventListener("change", () => {
      setSelectedDeviceCapability(id, checkbox.checked);
      saveSettings();
      void refreshTaskSuggestions();
      renderRunControls();
    });

    const copy = document.createElement("span");
    copy.innerHTML = `<strong>${escapeHTML(title)}</strong>`;

    label.append(checkbox, copy);
    grid.append(label);
  });
}

function bindSetting(key, input) {
  input.value = state.settings[key] || "";
  input.addEventListener("change", () => {
    state.settings[key] = input.value;
    saveSettings();
  });
}

async function hydrateSettings() {
  if (!window.computeHop.loadSettings) {
    state.settingsHydrated = true;
    maybeAutoStartDaemon();
    return;
  }

  try {
    const response = await window.computeHop.loadSettings();
    state.settings = mergeSettings(state.settings, response.settings || {});
    applyStoredRunDeviceSelection();
    persistLocalSettings();
    renderSettingsControls();
    renderCapabilities();
    await refreshTaskSuggestions();
    renderPlanPreview();
    renderRunControls();
    applyDaemonRoleOptions();
    await refreshAIPlannerStatus();
    await refreshDevices();
  } catch {
    // Keep the localStorage/default bootstrap settings if app-side settings
    // cannot be read. Saves still retry through the normal save path.
  } finally {
    state.settingsHydrated = true;
    maybeAutoStartDaemon();
  }
}

function maybeAutoStartDaemon() {
  if (!shouldAutoStartDaemon(state)) {
    return;
  }
  state.autoStartAttempted = true;
  void startDaemon();
}

function renderSettingsControls() {
  document.getElementById("lan-discovery").checked = state.settings.lanDiscovery !== false;
  document.getElementById("ask-before-run").checked = state.settings.askBeforeRun !== false;
  document.getElementById("daemon-role").value = state.settings.daemonRole || "orchestrator";
  document.getElementById("outputs-input").value = state.settings.artifacts || "";
}

function applyStoredRunDeviceSelection() {
  const selected = String(state.settings.selectedDeviceID || "").trim();
  if (!selected) {
    state.userSelectedDevice = false;
    return;
  }
  state.selectedDeviceID = selected;
  state.selectedJobDeviceID = selected;
  state.userSelectedDevice = true;
}

async function refreshAIPlannerStatus() {
  if (!window.computeHop.aiPlannerStatus) {
    renderAIPlannerStatus();
    return;
  }
  try {
    const response = await window.computeHop.aiPlannerStatus();
    state.aiPlannerStatus = normalizeAIPlannerStatus(response?.status);
    const model = document.getElementById("ai-model");
    if (!model.value && state.aiPlannerStatus.model) {
      model.value = state.aiPlannerStatus.model;
    }
  } catch {
    state.aiPlannerStatus = {
      configured: false,
      source: "",
      encrypted: false,
      model: ""
    };
  }
  renderAIPlannerStatus();
}

async function saveAIPlannerConfig() {
  if (!window.computeHop.saveAIPlanner) {
    showJobOutput("This build cannot save AI planner settings.", false);
    return;
  }
  const save = document.getElementById("save-ai-planner");
  save.disabled = true;
  save.textContent = "Saving";
  try {
    const response = await window.computeHop.saveAIPlanner({
      openAIAPIKey: document.getElementById("ai-api-key").value,
      model: document.getElementById("ai-model").value
    });
    state.aiPlannerStatus = normalizeAIPlannerStatus(response?.status);
    document.getElementById("ai-api-key").value = "";
    renderAIPlannerStatus();
  } catch (error) {
    showJobOutput(error.message || "Could not save AI planner settings.", false);
  } finally {
    save.disabled = false;
    save.textContent = "Save";
  }
}

async function clearAIPlannerConfig() {
  if (!window.computeHop.clearAIPlanner) {
    return;
  }
  const clear = document.getElementById("clear-ai-planner");
  clear.disabled = true;
  clear.textContent = "Clearing";
  try {
    const response = await window.computeHop.clearAIPlanner();
    state.aiPlannerStatus = normalizeAIPlannerStatus(response?.status);
    document.getElementById("ai-api-key").value = "";
    document.getElementById("ai-model").value = state.aiPlannerStatus.model || "";
    renderAIPlannerStatus();
  } catch (error) {
    showJobOutput(error.message || "Could not clear AI planner settings.", false);
  } finally {
    clear.disabled = false;
    clear.textContent = "Clear";
  }
}

function renderAIPlannerStatus() {
  const status = document.getElementById("ai-planner-status");
  const detail = document.getElementById("ai-planner-detail");
  const current = normalizeAIPlannerStatus(state.aiPlannerStatus);
  if (current.configured) {
    status.textContent = current.source === "environment" ? "On from env" : "On";
    const storage = current.source === "environment"
      ? "Using OPENAI_API_KEY from the environment."
      : current.encrypted
        ? "API key saved with OS-backed encryption."
        : "API key saved without OS-backed encryption on this system.";
    detail.textContent = `${storage} Local planning still runs first.${current.model ? ` Model: ${current.model}.` : ""}`;
    return;
  }
  status.textContent = "Off";
  detail.textContent = `Local planning works without an API key.${current.model ? ` Model saved for future use: ${current.model}.` : ""}`;
}

function normalizeAIPlannerStatus(status = {}) {
  return {
    configured: Boolean(status?.configured),
    source: String(status?.source || ""),
    encrypted: Boolean(status?.encrypted),
    model: String(status?.model || "").trim()
  };
}

function bindCheckbox(key, input) {
  input.checked = state.settings[key] !== false;
  input.addEventListener("change", () => {
    state.settings[key] = input.checked;
    saveSettings();
    if (key === "lanDiscovery") {
      refreshDevices();
    }
    if (key === "askBeforeRun") {
      state.plannedTask = null;
      renderPlanPreview();
      renderRunControls();
    }
  });
}

function loadSettings() {
  try {
    return mergeSettings(defaultSettings(), JSON.parse(localStorage.getItem("computehop.controlCenter") || "{}"));
  } catch {
    return defaultSettings();
  }
}

function defaultSettings() {
  return {
    projectRoot: "",
    artifacts: "",
    selectedDeviceID: "",
    selectedDeviceName: "",
    lanDiscovery: true,
    askBeforeRun: true,
    daemonRole: "orchestrator",
    syncedDevices: {},
    capabilities: defaultCapabilities(),
    deviceCapabilities: {}
  };
}

function defaultCapabilities() {
  return {
    builds: true,
    tests: true,
    docker: true,
    ai: true,
    video: true,
    commands: false
  };
}

function mergeSettings(base, incoming) {
  const defaults = defaultSettings();
  const next = {
    ...defaults,
    ...base,
    ...(incoming && typeof incoming === "object" ? incoming : {})
  };
  next.capabilities = {
    ...defaults.capabilities,
    ...(base && typeof base.capabilities === "object" ? base.capabilities : {}),
    ...(incoming && typeof incoming.capabilities === "object" ? incoming.capabilities : {})
  };
  next.selectedDeviceID = typeof next.selectedDeviceID === "string" ? next.selectedDeviceID : "";
  next.selectedDeviceName = typeof next.selectedDeviceName === "string" ? next.selectedDeviceName : "";
  next.syncedDevices = {
    ...booleanMap(base?.syncedDevices),
    ...booleanMap(incoming?.syncedDevices)
  };
  next.deviceCapabilities = {
    ...capabilityMapByDevice(base?.deviceCapabilities),
    ...capabilityMapByDevice(incoming?.deviceCapabilities)
  };
  return next;
}

function booleanMap(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }
  return Object.fromEntries(
    Object.entries(value).filter((entry) => typeof entry[1] === "boolean")
  );
}

function capabilityMapByDevice(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }
  return Object.fromEntries(
    Object.entries(value)
      .map(([deviceID, capabilitiesForDevice]) => [deviceID, capabilityMap(capabilitiesForDevice)])
      .filter((entry) => Object.keys(entry[1]).length > 0)
  );
}

function capabilityMap(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }
  return Object.fromEntries(
    Object.keys(defaultCapabilities())
      .filter((key) => typeof value[key] === "boolean")
      .map((key) => [key, value[key]])
  );
}

function persistLocalSettings() {
  localStorage.setItem("computehop.controlCenter", JSON.stringify(state.settings));
}

function saveSettings() {
  persistLocalSettings();
  if (!window.computeHop.saveSettings) {
    return;
  }

  window.computeHop.saveSettings(state.settings).catch(() => {
    // The app-side JSON store is best-effort from the renderer's point of view.
    // localStorage keeps the UI usable and the next explicit change retries.
  });
}

function allowedSuggestions(suggestions) {
  return filterAllowedSuggestions(suggestions, capabilitiesForSelectedDevice());
}

function selectedDevice() {
  const selected = state.devices.find((device) => device.id === state.selectedDeviceID);
  if (selected) {
    return selected;
  }
  if (!state.selectedDeviceID || state.selectedDeviceID === "local") {
    return defaultLocalDevice();
  }
  return unavailableSelectedDevice();
}

function selectedCapabilityDeviceID() {
  const device = selectedDevice();
  if (!device) {
    return state.selectedDeviceID || "local";
  }
  if (device.id === "auto") {
    return device.workerID || device.id;
  }
  return device.id || "local";
}

function capabilitiesForSelectedDevice() {
  return capabilitiesForDeviceID(selectedCapabilityDeviceID());
}

function capabilitiesForDeviceID(deviceID) {
  const fallback = state.settings.capabilities || {};
  const deviceCapabilities = state.settings.deviceCapabilities || {};
  return {
    ...defaultCapabilities(),
    ...capabilityMap(fallback),
    ...capabilityMap(deviceCapabilities[deviceID])
  };
}

function setSelectedDeviceCapability(capability, enabled) {
  const deviceID = selectedCapabilityDeviceID();
  state.settings.deviceCapabilities = {
    ...(state.settings.deviceCapabilities || {}),
    [deviceID]: {
      ...capabilitiesForDeviceID(deviceID),
      [capability]: enabled
    }
  };
}

function canRunOn(device) {
  if (!device || device.unavailableSelection) {
    return false;
  }
  if (isSyncManagedDevice(device) && device.synced === false) {
    return false;
  }
  if (device.id === "local") {
    return true;
  }
  if (device.id === "auto") {
    return true;
  }
  return device.role === "worker" && availabilityLabel(device) === "Connected";
}

function canSelectDeviceForRun(device) {
  if (device?.unavailableSelection) {
    return false;
  }
  return device?.id === "local" || device?.id === "auto" || canRunOn(device);
}

function isPairable(device) {
  return device.id !== "local" && device.id !== "auto" && device.connection === "not connected" && device.availability === "nearby";
}

function isUnpaired(device) {
  return device.id !== "local" && device.id !== "auto" && (device.trustState === "unpaired" || device.connection === "not connected");
}

function deviceLabel(device) {
  if (device.unavailableSelection) {
    return device.detail || "Waiting for this worker";
  }
  if (device.id === "local") {
    return device.role === "worker" ? "This computer · worker" : "This computer";
  }
  if (device.id === "auto") {
    return device.detail || "Uses the single connected worker";
  }
  const type = deviceType(device);
  if (isSyncManagedDevice(device) && device.synced === false) {
    return `${type} · disabled`;
  }
  if (device.role) {
    return `${type} · ${device.role.toLowerCase()}`;
  }
  return type;
}

function availabilityLabel(device) {
  if (device.unavailableSelection) {
    return "Waiting";
  }
  if (isSyncManagedDevice(device) && device.synced === false) {
    return "Off";
  }
  if (device.connection === "not connected") {
    return "Nearby";
  }
  if (device.connection === "active" || device.availability === "remote") {
    return "Connected";
  }
  if (device.availability === "nearby") {
    return "Nearby";
  }
  if (device.availability === "connecting") {
    return "Connecting";
  }
  return "Offline";
}

function scanSummary(devices) {
  const nearby = devices.filter((device) => device.id !== "local" && device.id !== "auto" && availabilityLabel(device) !== "Offline").length;
  if (nearby === 0) {
    return "No nearby workers yet";
  }
  return `${nearby} nearby device${nearby === 1 ? "" : "s"}`;
}

function deviceKind(device) {
  const name = `${device.name} ${device.role} ${device.address}`.toLowerCase();
  if (device.id === "local" || name.includes("macbook") || name.includes("laptop")) {
    return "laptop";
  }
  if (name.includes("server") || name.includes("nas") || name.includes("home")) {
    return "server";
  }
  if (name.includes("pc") || name.includes("desktop") || name.includes("gaming") || name.includes("windows")) {
    return "desktop";
  }
  if (name.includes("mac mini") || name.includes("mini")) {
    return "desktop";
  }
  return device.role === "worker" ? "desktop" : "laptop";
}

function isDeviceSynced(device) {
  if (!isSyncManagedDevice(device)) {
    return true;
  }
  return state.settings.syncedDevices[device.id] !== false;
}

function isSyncManagedDevice(device) {
  return (
    device &&
    device.id !== "local" &&
    device.id !== "auto" &&
    device.role === "worker" &&
    device.trustState === "paired"
  );
}

function shortPath(value) {
  const parts = value.split("/").filter(Boolean);
  if (parts.length === 0) {
    return value;
  }
  return parts[parts.length - 1];
}

function deviceType(device) {
  switch (deviceKind(device)) {
    case "laptop":
      return "MacBook";
    case "server":
      return "Server";
    case "desktop":
      return "Computer";
    default:
      return "Device";
  }
}

function runTargetLabel(device) {
  if (!device) {
    return "choose a device";
  }
  if (device.unavailableSelection) {
    return `waiting for ${device.name}`;
  }
  return `on ${device.name}`;
}

function escapeHTML(value) {
  return String(value || "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}
