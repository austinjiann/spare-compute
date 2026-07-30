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
  settings: loadSettings()
};
let refreshInFlight = false;
let jobsRefreshInFlight = false;
let runInFlight = false;
const pendingActions = new Set();

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
  ["video", "Video"]
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

bindCheckbox("lanDiscovery", document.getElementById("lan-discovery"));
bindCheckbox("askBeforeRun", document.getElementById("ask-before-run"));
bindSetting("aiProvider", document.getElementById("ai-provider"));
bindSetting("daemonRole", document.getElementById("daemon-role"));

renderCapabilities();
renderPlanPreview();
renderRunControls();
renderDaemonCard();
renderJobs();
void loadAppInfo();
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
      deviceID: selected.id,
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
    list.append(emptyJobsRow("Choose This Mac or a connected worker."));
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
  state.selectedJobDeviceID = job.deviceID || selectedDevice().id;
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
      deviceID: job.deviceID || selectedDevice().id
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
      deviceID: job.deviceID || selectedDevice().id,
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
  const devices = [...localDevices, ...remoteDevices].filter((device) => {
    const key = device.id || device.name;
    if (seen.has(key)) {
      return false;
    }
    seen.add(key);
    return true;
  });
  if (!devices.some((device) => device.id === state.selectedDeviceID)) {
    state.selectedDeviceID = "local";
  }
  return devices;
}

function renderDevices() {
  const list = document.getElementById("device-list");
  list.replaceChildren();

  state.devices.forEach((device) => {
    const row = document.createElement("div");
    row.className = "device-row";
    row.classList.toggle("selected", device.id === state.selectedDeviceID);
    row.addEventListener("click", () => {
      state.selectedDeviceID = device.id;
      state.selectedJobID = null;
      state.selectedJobDeviceID = device.id;
      state.selectedJobLogText = "";
      state.selectedJobLogTruncated = false;
      state.selectedJobLogFailed = false;
      state.jobs = [];
      renderDevices();
      renderRunControls();
      renderJobs();
      void refreshJobs();
    });

    const icon = document.createElement("span");
    icon.className = `device-icon ${deviceKind(device)}`;

    const copy = document.createElement("span");
    copy.className = "device-copy";
    copy.innerHTML = `<strong>${escapeHTML(device.name)}</strong><small>${deviceLabel(device)}</small>`;

    const meta = document.createElement("span");
    meta.className = "device-meta";
    meta.textContent = device.id === "local" ? "Here" : availabilityLabel(device);

    const action = deviceActionButton(device);

    row.append(icon, copy, meta, action);
    list.append(row);
  });
  renderRunControls();
}

function deviceActionButton(device) {
  const action = document.createElement("button");
  action.className = "row-button";

  if (device.id === "local") {
    action.textContent = device.id === state.selectedDeviceID ? "Selected" : "Use";
    action.disabled = device.id === state.selectedDeviceID;
    action.addEventListener("click", (event) => {
      event.stopPropagation();
      state.selectedDeviceID = device.id;
      state.selectedJobID = null;
      state.selectedJobDeviceID = device.id;
      state.selectedJobLogText = "";
      state.selectedJobLogTruncated = false;
      state.selectedJobLogFailed = false;
      state.jobs = [];
      renderDevices();
      renderRunControls();
      renderJobs();
      void refreshJobs();
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
    if (state.selectedDeviceID === device.id) {
      state.selectedDeviceID = "local";
    }
    await refreshDevices();
  });
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
  renderPlanPreview();
  renderRunControls();
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
    projectRoot: ""
  };
  state.plannedTask = planned;
  renderPlanPreview();
  await startPlannedJob(planned, selected);
}

async function startPlannedJob(planned, selected) {
  const output = document.getElementById("job-output");
  const button = document.getElementById("run-job");
  const outputs = declaredOutputs();
  const readinessError = validateRunReadiness(selected, planned, outputs);
  if (readinessError) {
    showJobOutput(readinessError, false);
    return;
  }

  runInFlight = true;
  state.currentRunID = null;
  button.disabled = true;
  button.textContent = "Starting";
  output.classList.remove("hidden");
  output.classList.remove("success", "failure");
  output.textContent = `Running ${planned.command} on ${selected.name}…`;

  try {
    const result = await window.computeHop.startJob({
      command: planned.command,
      deviceID: selected.id,
      workingDirectory: state.settings.projectRoot || "",
      outputs
    });
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
  if (!selected || !canRunOn(selected)) {
    return "Choose This Mac or a connected worker first.";
  }
  if (selected.id !== "local" && planned.requiresProject && !state.settings.projectRoot) {
    return `Choose a project before running this on ${selected.name}. ComputeHop needs the folder so it can copy the files to that computer.`;
  }
  if (selected.id !== "local" && outputs.length > 0 && !state.settings.projectRoot) {
    return "Choose a project before bringing files back from another computer.";
  }
  return "";
}

function declaredOutputs() {
  const input = document.getElementById("outputs-input");
  const seen = new Set();
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
      showJobOutput(response.error || "Could not plan that task.", false);
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
  detail.textContent = plan.detail || "";
  command.textContent = plan.command || "";
}

async function stopCurrentJob() {
  if (!state.currentRunID) {
    return;
  }
  const button = document.getElementById("run-job");
  button.disabled = true;
  button.textContent = "Stopping";
  await window.computeHop.stopJob(state.currentRunID);
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
  const runButton = document.getElementById("run-job");
  const testButton = document.getElementById("test-device");
  const task = document.getElementById("command-input").value.trim();

  target.textContent = selected ? `on ${selected.name}` : "choose a device";
  projectLabel.textContent = state.settings.projectRoot
    ? shortPath(state.settings.projectRoot)
    : "No project";
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

  capabilities.forEach(([id, title]) => {
    const label = document.createElement("label");
    label.className = "check-card";

    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.checked = state.settings.capabilities[id] !== false;
    checkbox.addEventListener("change", () => {
      state.settings.capabilities[id] = checkbox.checked;
      saveSettings();
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
  const defaults = {
    projectRoot: "",
    artifacts: "",
    ignoreHeavyFolders: true,
    lanDiscovery: true,
    remoteRelay: false,
    askBeforeRun: true,
    daemonRole: "orchestrator",
    aiProvider: "off",
    syncedDevices: {},
    capabilities: {
      builds: true,
      tests: true,
      docker: true,
      ai: true,
      video: true,
      commands: false
    }
  };

  try {
    return {
      ...defaults,
      ...JSON.parse(localStorage.getItem("computehop.controlCenter") || "{}")
    };
  } catch {
    return defaults;
  }
}

function saveSettings() {
  localStorage.setItem("computehop.controlCenter", JSON.stringify(state.settings));
}

function selectedDevice() {
  return state.devices.find((device) => device.id === state.selectedDeviceID) || defaultLocalDevice();
}

function canRunOn(device) {
  if (device.id === "local") {
    return true;
  }
  return device.role === "worker" && availabilityLabel(device) === "Connected";
}

function isPairable(device) {
  return device.id !== "local" && device.connection === "not connected" && device.availability === "nearby";
}

function isUnpaired(device) {
  return device.id !== "local" && (device.trustState === "unpaired" || device.connection === "not connected");
}

function deviceLabel(device) {
  if (device.id === "local") {
    return device.role === "worker" ? "This computer · worker" : "This computer";
  }
  const type = deviceType(device);
  if (device.role) {
    return `${type} · ${device.role.toLowerCase()}`;
  }
  return type;
}

function availabilityLabel(device) {
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
  const nearby = devices.filter((device) => device.id !== "local" && availabilityLabel(device) !== "Offline").length;
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

function escapeHTML(value) {
  return String(value || "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}
