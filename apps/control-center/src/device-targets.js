(function attachDeviceTargets(root, factory) {
  const exports = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopDeviceTargets = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createDeviceTargets() {
  const automaticWorkerID = "auto";

  function addAutomaticWorkerTarget(devices = [], selectedDeviceID = "local", options = {}) {
    const baseDevices = devices.filter((device) => device?.id !== automaticWorkerID);
    const workers = baseDevices.filter(isSingleAutoCandidate);
    const result = workers.length === 1
      ? insertAfterLocal(baseDevices, automaticWorkerTarget(workers[0]))
      : baseDevices;

    let selected = result.some((device) => device.id === selectedDeviceID)
      ? selectedDeviceID
      : "local";
    if (
      selected === "local" &&
      options.preserveUnavailableSelection &&
      selectedDeviceID &&
      selectedDeviceID !== "local"
    ) {
      selected = selectedDeviceID;
    }
    if (workers.length === 1 && options.preferAutomaticWorker && selected === "local") {
      selected = automaticWorkerID;
    }

    return {
      devices: result,
      selectedDeviceID: selected
    };
  }

  function isSingleAutoCandidate(device) {
    return (
      device &&
      device.id !== "local" &&
      device.id !== automaticWorkerID &&
      device.synced !== false &&
      device.role === "worker" &&
      device.connection === "active" &&
      (device.availability === "remote" || device.availability === "nearby")
    );
  }

  function compatibleWorkerForPlan(devices = [], plan = {}, options = {}) {
    const platform = normalizeTargetPlatform(plan.targetPlatform || plan.requiredPlatform);
    const architecture = normalizeTargetArchitecture(plan.targetArchitecture || plan.requiredArchitecture || plan.targetArch || plan.requiredArch);
    const allowed = typeof options.isWorkerAllowed === "function" ? options.isWorkerAllowed : () => true;
    const shouldTry = platform || architecture || options.requireAllowedMatch;
    if (!shouldTry) {
      return null;
    }
    const workers = devices.filter((device) => (
      isSingleAutoCandidate(device) &&
      workerMatchesPlatform(device, platform) &&
      workerMatchesArchitecture(device, architecture) &&
      allowed(device)
    ));
    return workers.length === 1 ? workers[0] : null;
  }

  function workerMatchesPlatform(device = {}, targetPlatform = "") {
    const target = normalizeTargetPlatform(targetPlatform);
    if (!target) {
      return true;
    }
    return normalizeDevicePlatform(device.platform || device.os) === target;
  }

  function workerMatchesArchitecture(device = {}, targetArchitecture = "") {
    const target = normalizeTargetArchitecture(targetArchitecture);
    if (!target) {
      return true;
    }
    return normalizeDeviceArchitecture(device.arch || device.architecture) === target;
  }

  function automaticWorkerTarget(worker) {
    return {
      id: automaticWorkerID,
      name: "Auto worker",
      detail: `Uses ${worker.name || "the connected worker"}`,
      role: "worker",
      connection: "active",
      availability: "remote",
      trustState: "paired",
      path: "auto",
      workerID: worker.id || "",
      workerName: worker.name || "",
      address: "",
      updated: worker.updated || "",
      automatic: true
    };
  }

  function concreteDeviceID(device) {
    if (!device) {
      return "local";
    }
    if (device.id === automaticWorkerID && device.workerID) {
      return device.workerID;
    }
    return device.id || "local";
  }

  function singleConnectedWorkerTarget(devices = []) {
    const workers = devices.filter(isSingleAutoCandidate);
    if (workers.length !== 1) {
      return null;
    }
    return automaticWorkerTarget(workers[0]);
  }

  function workerRunTargetForAction(devices = [], workerID = "") {
    const id = String(workerID || "").trim();
    if (!id) {
      return null;
    }
    const worker = devices.find((device) => device?.id === id && isSingleAutoCandidate(device));
    if (!worker) {
      return null;
    }
    const automatic = devices.find((device) => (
      device?.id === automaticWorkerID &&
      device.workerID === id
    ));
    return automatic || worker;
  }

  function insertAfterLocal(devices, target) {
    const localIndex = devices.findIndex((device) => device.id === "local");
    if (localIndex < 0) {
      return [target, ...devices];
    }
    return [
      ...devices.slice(0, localIndex + 1),
      target,
      ...devices.slice(localIndex + 1)
    ];
  }

  return {
    addAutomaticWorkerTarget,
    automaticWorkerID,
    compatibleWorkerForPlan,
    concreteDeviceID,
    isSingleAutoCandidate,
    singleConnectedWorkerTarget,
    workerMatchesArchitecture,
    workerMatchesPlatform,
    workerRunTargetForAction
  };

  function normalizeTargetPlatform(value) {
    const platform = String(value || "").trim().toLowerCase();
    if (["darwin", "mac", "macos", "osx"].includes(platform)) {
      return "darwin";
    }
    if (["windows", "win32", "win"].includes(platform)) {
      return "windows";
    }
    if (platform === "linux") {
      return "linux";
    }
    return "";
  }

  function normalizeDevicePlatform(value) {
    const platform = String(value || "").trim().toLowerCase();
    if (platform === "darwin" || platform === "macos") {
      return "darwin";
    }
    if (platform === "windows" || platform === "win32") {
      return "windows";
    }
    if (platform === "linux") {
      return "linux";
    }
    return "";
  }

  function normalizeTargetArchitecture(value) {
    const architecture = String(value || "").trim().toLowerCase();
    if (["arm64", "aarch64", "apple-silicon", "apple silicon"].includes(architecture)) {
      return "arm64";
    }
    if (["amd64", "x64", "x86_64", "x86-64", "intel"].includes(architecture)) {
      return "amd64";
    }
    return "";
  }

  function normalizeDeviceArchitecture(value) {
    return normalizeTargetArchitecture(value);
  }
}));
