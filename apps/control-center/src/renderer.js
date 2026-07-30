const state = {
  devices: [],
  pairings: [],
  selectedDeviceID: "local",
  currentRunID: null,
  plannedTask: null,
  settings: loadSettings()
};
let refreshInFlight = false;
let runInFlight = false;
const pendingActions = new Set();

const defaultDevices = [
  {
    name: "This Mac",
    id: "local",
    connection: "active",
    role: "orchestrator",
    availability: "nearby",
    path: "local",
    address: "local",
    updated: ""
  }
];

const capabilities = [
  ["builds", "Builds"],
  ["tests", "Tests"],
  ["docker", "Docker"],
  ["ai", "AI"],
  ["video", "Video"]
];

document.getElementById("refresh-devices").addEventListener("click", refreshDevices);
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

renderCapabilities();
renderPlanPreview();
renderRunControls();
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
    state.devices = defaultDevices;
    state.pairings = [];
    error.classList.add("hidden");
    status.textContent = "Nearby discovery off";
    renderDevices();
    renderPairings();
    renderRunControls();
    return;
  }

  refreshInFlight = true;
  button.disabled = true;
  button.textContent = "Scanning";
  status.textContent = "Scanning nearby devices";

  try {
    const response = await window.computeHop.listDevices();
    state.devices = mergeDevices(defaultDevices, response.devices || []);
    state.pairings = response.pairings || [];
    error.classList.toggle("hidden", response.ok);
    error.textContent = response.ok ? "" : "Start ComputeHop to discover nearby devices.";
    status.textContent = response.ok ? scanSummary(state.devices) : "Discovery unavailable";
  } catch (err) {
    state.devices = defaultDevices;
    state.pairings = [];
    error.classList.remove("hidden");
    error.textContent = "Start ComputeHop to discover nearby devices.";
    status.textContent = "Discovery unavailable";
  } finally {
    renderDevices();
    renderPairings();
    button.disabled = false;
    button.textContent = "Refresh";
    refreshInFlight = false;
  }
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
      renderDevices();
      renderRunControls();
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
      renderDevices();
      renderRunControls();
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
    command: "/bin/hostname",
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
  const readinessError = validateRunReadiness(selected, planned);
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
      workingDirectory: state.settings.projectRoot || ""
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

function validateRunReadiness(selected, planned) {
  if (!selected || !canRunOn(selected)) {
    return "Choose This Mac or a connected worker first.";
  }
  if (selected.id !== "local" && planned.requiresProject && !state.settings.projectRoot) {
    return `Choose a project before running this on ${selected.name}. ComputeHop needs the folder so it can copy the files to that computer.`;
  }
  return "";
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
  return state.devices.find((device) => device.id === state.selectedDeviceID) || defaultDevices[0];
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
    return "This computer";
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
