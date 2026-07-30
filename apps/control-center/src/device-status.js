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
    const name = `${device.name || ""} ${device.role || ""} ${device.address || ""}`.toLowerCase();
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

  function deviceType(device = {}) {
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

  return {
    availabilityLabel,
    connectionDetail,
    connectionPathLabel,
    deviceKind,
    deviceLabel,
    deviceType,
    friendlyConnectionError,
    isSyncManagedDevice
  };
}));
