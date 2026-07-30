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
      device.synced !== false &&
      device.role === "worker" &&
      device.connection === "active" &&
      device.availability === "remote"
    );
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
    concreteDeviceID,
    isSingleAutoCandidate,
    singleConnectedWorkerTarget,
    workerRunTargetForAction
  };
}));
