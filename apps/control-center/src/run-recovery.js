(function attachRunRecovery(root, factory) {
  const deviceTargets = typeof module === "object" && module.exports
    ? require("./device-targets")
    : root.computeHopDeviceTargets;
  const exports = factory(deviceTargets);
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopRunRecovery = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createRunRecovery(deviceTargets = {}) {
  const concreteDeviceID = deviceTargets?.concreteDeviceID || fallbackConcreteDeviceID;
  const workerRunTargetForAction = deviceTargets?.workerRunTargetForAction || (() => null);

  function workerTargetActionRequest(plan = {}, devices = [], options = {}) {
    if (cleanString(plan?.targetPreference) !== "worker") {
      return {};
    }
    const isPairable = typeof options.isPairable === "function" ? options.isPairable : defaultIsPairable;
    const pairable = devices.filter(isPairable);
    if (pairable.length !== 1) {
      return {};
    }
    return {
      workerTargetActionKind: "connect-device",
      workerTargetActionLabel: "Connect",
      workerTargetDeviceID: pairable[0].id
    };
  }

  function runControlCanRecover(blocker = {}, options = {}) {
    if (!cleanString(blocker?.message)) {
      return false;
    }
    if (blocker.actionKind === "choose-project") {
      return true;
    }
    if (blocker.actionKind !== "connect-device") {
      return false;
    }
    const device = typeof options.actionDevice === "function"
      ? options.actionDevice(blocker)
      : options.actionDevice;
    const isPairable = typeof options.isPairable === "function" ? options.isPairable : defaultIsPairable;
    return Boolean(device && isPairable(device));
  }

  function pendingRunAfterPairing(plan = {}, worker = {}) {
    const workerID = cleanString(concreteDeviceID(worker));
    if (!workerID || workerID === "local" || workerID === "auto") {
      return null;
    }
    return {
      plan,
      workerID,
      task: cleanString(plan?.source) || cleanString(plan?.title) || cleanString(plan?.command) || "this task"
    };
  }

  function pendingPairingRunTarget(pending = null, devices = [], options = {}) {
    if (!pending?.workerID) {
      return null;
    }
    const isRunnable = typeof options.isRunnable === "function" ? options.isRunnable : defaultIsRunnable;
    const automatic = workerRunTargetForAction(devices, pending.workerID);
    if (automatic && isRunnable(automatic)) {
      return automatic;
    }
    return devices.find((device) => (
      concreteDeviceID(device) === pending.workerID &&
      isRunnable(device)
    )) || null;
  }

  function pendingPairingRunMatchesTarget(pending = null, target = null) {
    return Boolean(pending?.workerID && target && concreteDeviceID(target) === pending.workerID);
  }

  function defaultIsPairable(device = {}) {
    return device.id !== "local" &&
      device.id !== "auto" &&
      device.connection === "not connected" &&
      device.availability === "nearby";
  }

  function defaultIsRunnable(device = {}) {
    if (!device || device.unavailableSelection || device.synced === false) {
      return false;
    }
    if (device.id === "local" || device.id === "auto") {
      return true;
    }
    return device.role === "worker" &&
      device.connection === "active" &&
      (device.availability === "remote" || device.availability === "nearby");
  }

  function fallbackConcreteDeviceID(device = {}) {
    if (!device) {
      return "local";
    }
    if (device.id === "auto" && device.workerID) {
      return device.workerID;
    }
    return device.id || "local";
  }

  function cleanString(value) {
    return String(value || "").trim();
  }

  return {
    pendingPairingRunMatchesTarget,
    pendingPairingRunTarget,
    pendingRunAfterPairing,
    runControlCanRecover,
    workerTargetActionRequest
  };
}));
