const state = {
  devices: [],
  settings: loadSettings()
};
let refreshInFlight = false;

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

bindCheckbox("lanDiscovery", document.getElementById("lan-discovery"));
bindCheckbox("askBeforeRun", document.getElementById("ask-before-run"));
bindSetting("aiProvider", document.getElementById("ai-provider"));

renderCapabilities();
refreshDevices();
setInterval(refreshDevices, 5000);

async function refreshDevices() {
  if (refreshInFlight) {
    return;
  }

  const button = document.getElementById("refresh-devices");
  const error = document.getElementById("device-error");
  const status = document.getElementById("scan-status");

  if (!state.settings.lanDiscovery) {
    state.devices = defaultDevices;
    error.classList.add("hidden");
    status.textContent = "Nearby discovery off";
    renderDevices();
    return;
  }

  refreshInFlight = true;
  button.disabled = true;
  button.textContent = "Scanning";
  status.textContent = "Scanning nearby devices";

  try {
    const response = await window.computeHop.listDevices();
    state.devices = mergeDevices(defaultDevices, response.devices || []);
    error.classList.toggle("hidden", response.ok);
    error.textContent = response.ok ? "" : "Start ComputeHop to discover nearby devices.";
    status.textContent = response.ok ? scanSummary(state.devices) : "Discovery unavailable";
  } catch (err) {
    state.devices = defaultDevices;
    error.classList.remove("hidden");
    error.textContent = "Start ComputeHop to discover nearby devices.";
    status.textContent = "Discovery unavailable";
  } finally {
    renderDevices();
    button.disabled = false;
    button.textContent = "Refresh";
    refreshInFlight = false;
  }
}

function mergeDevices(localDevices, remoteDevices) {
  const seen = new Set();
  return [...localDevices, ...remoteDevices].filter((device) => {
    const key = device.id || device.name;
    if (seen.has(key)) {
      return false;
    }
    seen.add(key);
    return true;
  });
}

function renderDevices() {
  const list = document.getElementById("device-list");
  list.replaceChildren();

  state.devices.forEach((device) => {
    const row = document.createElement("label");
    row.className = "device-row";

    const icon = document.createElement("span");
    icon.className = `device-icon ${deviceKind(device)}`;

    const copy = document.createElement("span");
    copy.className = "device-copy";
    copy.innerHTML = `<strong>${escapeHTML(device.name)}</strong><small>${deviceLabel(device)}</small>`;

    const meta = document.createElement("span");
    meta.className = "device-meta";
    meta.textContent = device.id === "local" ? "Here" : availabilityLabel(device);

    const toggle = document.createElement("input");
    toggle.type = "checkbox";
    toggle.checked = state.settings.syncedDevices[device.id] !== false;
    toggle.addEventListener("change", () => {
      state.settings.syncedDevices[device.id] = toggle.checked;
      saveSettings();
    });

    row.append(icon, copy, meta, toggle);
    list.append(row);
  });
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

function statusClass(device) {
  if (device.id === "local") {
    return "available";
  }
  if (device.trust === "active" || device.address !== "—") {
    return "available";
  }
  return "offline";
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
  if (device.connection === "active" || device.availability === "nearby" || device.availability === "remote") {
    return "Connected";
  }
  if (device.connection === "not connected") {
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
