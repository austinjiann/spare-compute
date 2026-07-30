(function attachDeviceStatus(root, factory) {
  const exports = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopDeviceStatus = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createDeviceStatus() {
  function deviceLabel(device = {}) {
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

    const connection = connectionDetail(device);
    return connection ? `${type} · ${connection}` : type;
  }

  function availabilityLabel(device = {}) {
    if (device.unavailableSelection) {
      return "Waiting";
    }
    if (isSyncManagedDevice(device) && device.synced === false) {
      return "Off";
    }
    if (device.connection === "active" || device.availability === "remote") {
      return "Connected";
    }
    if (device.availability === "connecting") {
      return "Connecting";
    }
    if (device.availability === "nearby") {
      return "Nearby";
    }
    return "Offline";
  }

  function connectionDetail(device = {}) {
    if (availabilityLabel(device) === "Connected") {
      return connectedDetail(device);
    }
    if (device.availability === "connecting") {
      return "reconnecting";
    }
    if (device.availability === "nearby") {
      return device.trustState === "paired" ? "nearby" : "ready to connect";
    }
    if (device.connectionError) {
      return friendlyConnectionError(device.connectionError);
    }
    if (device.role) {
      return roleName(device.role);
    }
    return "";
  }

  function connectedDetail(device = {}) {
    const path = connectionPathLabel(device.path);
    if (path) {
      return `connected over ${path}`;
    }
    return "connected";
  }

  function connectionPathLabel(value) {
    const path = String(value || "").trim().toLowerCase();
    if (!path || path === "unspecified") {
      return "";
    }
    if (path === "lan" || path.includes("lan")) {
      return "LAN";
    }
    if (path === "relay" || path === "turn" || path.includes("relay") || path.includes("turn")) {
      return "relay";
    }
    if (path === "direct" || path === "ice" || path.includes("direct") || path.includes("ice")) {
      return "direct link";
    }
    if (path === "local") {
      return "this Mac";
    }
    return "";
  }

  function friendlyConnectionError(value) {
    const error = String(value || "").trim();
    const lower = error.toLowerCase();
    if (!error) {
      return "";
    }
    if (lower.includes("re-pair")) {
      return "needs reconnect setup";
    }
    if (lower.includes("remote connectivity") && lower.includes("disabled")) {
      return "remote access off";
    }
    if (lower.includes("timeout") || lower.includes("deadline")) {
      return "connection timed out";
    }
    return "offline";
  }

  function deviceKind(device = {}) {
    const platform = platformHint(device);
    const name = `${device.name || ""} ${device.role || ""} ${device.address || ""}`.toLowerCase();
    if (platform === "win32" || platform === "windows") {
      return "desktop";
    }
    if (platform === "linux") {
      return name.includes("server") || name.includes("nas") || name.includes("home") ? "server" : "desktop";
    }
    if (platform === "darwin" && (name.includes("mac mini") || name.includes("mini") || name.includes("studio"))) {
      return "desktop";
    }
    if (device.id === "local" || platform === "darwin" || name.includes("macbook") || name.includes("laptop")) {
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

  function deviceType(device = {}) {
    const kind = deviceKind(device);
    const platform = platformHint(device);
    if (kind === "server") {
      return platform === "linux" ? "Linux server" : "Server";
    }
    if (platform === "darwin") {
      return kind === "laptop" ? "MacBook" : "Mac";
    }
    if (platform === "win32" || platform === "windows") {
      return "Windows PC";
    }
    if (platform === "linux") {
      return "Linux computer";
    }
    switch (kind) {
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

  function platformHint(device = {}) {
    return String(device.platform || device.os || "").trim().toLowerCase();
  }

  function isSyncManagedDevice(device = {}) {
    return (
      device &&
      device.id !== "local" &&
      device.id !== "auto" &&
      device.role === "worker" &&
      device.trustState === "paired"
    );
  }

  function roleName(role) {
    if (role === "worker") {
      return "worker";
    }
    if (role === "orchestrator") {
      return "Control Mac";
    }
    return "";
  }

  function workerReadinessSummary(request = {}) {
    const devices = Array.isArray(request.devices) ? request.devices : [];
    const pairings = Array.isArray(request.pairings) ? request.pairings : [];
    const selectedDeviceID = String(request.selectedDeviceID || "").trim();

    if (request.daemonAvailable === false) {
      return summary(
        "daemon-off",
        "ComputeHop is off",
        "Start it to find workers and run tasks.",
        "Start",
        "start-daemon"
      );
    }

    if (request.lanDiscovery === false) {
      return summary(
        "discovery-off",
        "Nearby discovery off",
        "Turn on discovery to find workers on this network.",
        "Turn on",
        "enable-discovery"
      );
    }

    const selected = devices.find((device) => device.id === selectedDeviceID);
    if (isRunnableWorker(selected)) {
      return summary(
        "ready",
        "Worker ready",
        `${selected.name || "Selected worker"} can run tasks.`,
        "Test",
        "test-worker"
      );
    }

    const connectedWorkers = devices.filter(isRunnableWorker);
    if (connectedWorkers.length === 1) {
      const worker = connectedWorkers[0];
      return summary(
        "ready",
        "Worker ready",
        `${worker.name || "A worker"} can run tasks.`,
        "Test",
        "test-worker",
        worker.id
      );
    }
    if (connectedWorkers.length > 1) {
      return summary(
        "choose-worker",
        "Workers ready",
        "Choose the computer you want to use.",
        "",
        ""
      );
    }

    const selectedDisabled = isDisabledWorker(selected) ? selected : null;
    if (selectedDisabled) {
      return summary(
        "disabled",
        "Worker paused",
        `${selectedDisabled.name || "Selected worker"} is disabled for tasks.`,
        "Enable",
        "enable-device"
      );
    }

    const disabledWorkers = devices.filter(isDisabledWorker);
    if (disabledWorkers.length === 1) {
      const worker = disabledWorkers[0];
      return summary(
        "disabled",
        "Worker paused",
        `${worker.name || "A worker"} is disabled for tasks.`,
        "Enable",
        "enable-device",
        worker.id
      );
    }
    if (disabledWorkers.length > 1) {
      return summary(
        "disabled",
        "Workers paused",
        "Enable the computer you want to use below.",
        "",
        ""
      );
    }

    const waitingPairing = pairings.find((pairing) => pairing.state === "waiting");
    if (waitingPairing) {
      const peer = waitingPairing.peerName || "the other computer";
      return summary(
        "pairing",
        "Pairing waiting",
        waitingPairing.localConfirmed
          ? `Waiting for ${peer}.`
          : `Confirm the code for ${peer} below.`,
        "",
        ""
      );
    }

    const nearbyWorkers = devices.filter(isPairableWorker);
    if (nearbyWorkers.length === 1) {
      const worker = nearbyWorkers[0];
      return summary(
        "nearby",
        "Worker nearby",
        `Connect to ${worker.name || "this worker"}.`,
        "Connect",
        "connect-device",
        worker.id
      );
    }
    if (nearbyWorkers.length > 1) {
      return summary(
        "nearby",
        "Workers nearby",
        "Choose one below to connect.",
        "",
        ""
      );
    }

    const offlineWorkers = devices.filter(isOfflineTrustedWorker);
    if (offlineWorkers.length > 0) {
      const first = offlineWorkers[0];
      return summary(
        "offline",
        "Worker offline",
        `Open ComputeHop on ${first.name || "the worker"} or put both computers on the same network.`,
        "Refresh",
        "refresh"
      );
    }

    return summary(
      "none",
      "No workers yet",
      "Open ComputeHop on another computer on this network.",
      "Refresh",
      "refresh"
    );
  }

  function isRunnableWorker(device = {}) {
    return (
      device &&
      device.id !== "local" &&
      device.id !== "auto" &&
      device.role === "worker" &&
      device.synced !== false &&
      device.trustState === "paired" &&
      availabilityLabel(device) === "Connected"
    );
  }

  function isPairableWorker(device = {}) {
    return (
      device &&
      device.id !== "local" &&
      device.id !== "auto" &&
      device.role === "worker" &&
      availabilityLabel(device) === "Nearby" &&
      (device.trustState === "unpaired" || device.connection === "not connected")
    );
  }

  function isOfflineTrustedWorker(device = {}) {
    return (
      device &&
      device.id !== "local" &&
      device.id !== "auto" &&
      device.role === "worker" &&
      device.trustState === "paired" &&
      availabilityLabel(device) === "Offline"
    );
  }

  function isDisabledWorker(device = {}) {
    return (
      device &&
      device.id !== "local" &&
      device.id !== "auto" &&
      device.role === "worker" &&
      device.trustState === "paired" &&
      device.synced === false
    );
  }

  function summary(kind, title, detail, actionLabel, actionKind, deviceID = "") {
    return {
      kind,
      title,
      detail,
      actionLabel,
      actionKind,
      deviceID
    };
  }

  return {
    availabilityLabel,
    connectionDetail,
    connectionPathLabel,
    deviceKind,
    deviceLabel,
    deviceType,
    friendlyConnectionError,
    isSyncManagedDevice,
    workerReadinessSummary
  };
}));
