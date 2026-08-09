(function attachDeviceTargets(root, factory) {
  const capabilityCatalog = typeof module === "object" && module.exports
    ? require("./capability-catalog")
    : root.computeHopCapabilityCatalog;
  const exports = factory(capabilityCatalog);
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopDeviceTargets = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createDeviceTargets(capabilityCatalog: any = {}) {
  const automaticWorkerID = "auto";

  function addAutomaticWorkerTarget(devices = [], selectedDeviceID = "local", options: any = {}) {
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

  function compatibleWorkerForPlan(devices = [], plan: any = {}, options: any = {}) {
    const platform = normalizeTargetPlatform(plan.targetPlatform || plan.requiredPlatform);
    const architecture = normalizeTargetArchitecture(plan.targetArchitecture || plan.requiredArchitecture || plan.targetArch || plan.requiredArch);
    const allowed = typeof options.isWorkerAllowed === "function" ? options.isWorkerAllowed : () => true;
    const shouldTry = platform || architecture || options.requireAllowedMatch ||
      normalizeTargetPreference(plan.targetPreference) === "worker" ||
      options.preferBestWorker;
    if (!shouldTry) {
      return null;
    }
    const workers = devices.filter((device) => (
      isSingleAutoCandidate(device) &&
      workerMatchesPlatform(device, platform) &&
      workerMatchesArchitecture(device, architecture) &&
      deviceHasRequiredExecutor(device, plan) &&
      deviceHasRequiredTools(device, plan) &&
      allowed(device)
    ));
    return bestWorkerFromCandidates(workers, plan);
  }

  function bestWorkerFromCandidates(workers = [], plan: any = {}) {
    if (workers.length === 0) {
      return null;
    }
    if (workers.length === 1) {
      return workers[0];
    }
    const ranked = workers
      .map((worker) => ({
        worker,
        executorRank: workerExecutorReadinessRank(worker, plan),
        toolRank: workerToolReadinessRank(worker, plan),
        score: workerResourceScore(worker)
      }))
      .sort((left, right) => {
        if (right.executorRank !== left.executorRank) {
          return right.executorRank - left.executorRank;
        }
        if (right.toolRank !== left.toolRank) {
          return right.toolRank - left.toolRank;
        }
        if (right.score !== left.score) {
          return right.score - left.score;
        }
        return stableWorkerKey(left.worker).localeCompare(stableWorkerKey(right.worker));
      });
    return ranked[0].worker;
  }

  function workerMatchesPlatform(device: any = {}, targetPlatform = "") {
    const target = normalizeTargetPlatform(targetPlatform);
    if (!target) {
      return true;
    }
    return normalizeDevicePlatform(device.platform || device.os) === target;
  }

  function workerMatchesArchitecture(device: any = {}, targetArchitecture = "") {
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
      toolIDs: normalizeToolIDs(worker.toolIDs || worker.toolIds),
      supportedExecutors: normalizeExecutors(worker.supportedExecutors || worker.supportedExecutorIds),
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

  function workerTargetAfterPairingConfirmation(devices = [], selectedDeviceID = "local") {
    const selected = devices.find((device) => device?.id === selectedDeviceID);
    if (selected && selected.id !== "local" && isSingleAutoCandidate(selected)) {
      return null;
    }
    return singleConnectedWorkerTarget(devices);
  }

  function workerResourceScore(device: any = {}) {
    const cpuCount = numericHint(device.logicalCPUCount || device.logicalCpuCount || device.cpuCount);
    const memoryGiB = numericHint(device.totalMemoryBytes || device.memoryBytes) / 1024 ** 3;
    return (cpuCount * 1_000) + memoryGiB;
  }

  function deviceHasRequiredTools(device: any = {}, plan: any = {}) {
    return missingToolIDsForPlan(device, plan).length === 0;
  }

  function deviceHasRequiredExecutor(device: any = {}, plan: any = {}) {
    return workerExecutorReadiness(device, plan).compatible;
  }

  function workerExecutorReadiness(device: any = {}, plan: any = {}) {
    const required = requiredExecutorForPlan(plan);
    const reported = normalizeExecutors(device.supportedExecutors || device.supportedExecutorIds || device.executors);
    if (!required) {
      return {
        state: "not-required",
        required: "",
        reported,
        compatible: true
      };
    }
    if (reported.includes(required)) {
      return {
        state: "ready",
        required,
        reported,
        compatible: true
      };
    }
    if (required === "native" && reported.length === 0) {
      return {
        state: "unknown-compatible",
        required,
        reported,
        compatible: true
      };
    }
    return {
      state: reported.length === 0 ? "unknown" : "missing",
      required,
      reported,
      compatible: false
    };
  }

  function workerExecutorReadinessRank(device: any = {}, plan: any = {}) {
    switch (workerExecutorReadiness(device, plan).state) {
      case "ready":
        return 2;
      case "unknown-compatible":
        return 1;
      default:
        return 0;
    }
  }

  function workerToolReadiness(device: any = {}, plan: any = {}) {
    const required = requiredToolIDsForPlan(plan);
    if (required.length === 0) {
      return {
        state: "not-required",
        required,
        missing: [],
        reported: false
      };
    }
    const reported = normalizeToolIDs(device.toolIDs || device.toolIds);
    if (reported.length === 0) {
      return {
        state: "unknown",
        required,
        missing: [],
        reported: false
      };
    }
    const missing = missingToolIDsForPlan(device, plan);
    return {
      state: missing.length > 0 ? "missing" : "ready",
      required,
      missing,
      reported: true
    };
  }

  function workerToolReadinessRank(device: any = {}, plan: any = {}) {
    switch (workerToolReadiness(device, plan).state) {
      case "ready":
        return 2;
      case "unknown":
        return 1;
      default:
        return 0;
    }
  }

  function missingToolIDsForPlan(device: any = {}, plan: any = {}) {
    const reported = normalizeToolIDs(device.toolIDs || device.toolIds);
    if (reported.length === 0) {
      return [];
    }
    const available = new Set(reported);
    return requiredToolIDsForPlan(plan).filter((toolID) => !available.has(toolID));
  }

  function requiredToolIDsForPlan(plan: any = {}) {
    const explicit = normalizeToolIDs(
      plan.requiredToolIDs ||
      plan.requiredToolIds ||
      plan.requiredTools ||
      plan.toolIDs ||
      plan.toolIds ||
      plan.tools
    );
    if (explicit.length > 0) {
      return explicit;
    }
    const executable = commandExecutable(plan.command || "");
    const toolID = toolIDFromExecutable(executable);
    if (!toolID || localScriptExecutable(executable)) {
      return [];
    }
    if (toolID === "docker" && /^\s*docker\s+compose(?:\s|$)/i.test(plan.command || "")) {
      return ["docker"];
    }
    if (toolID === "npm") {
      return ["node", "npm"];
    }
    if (["pnpm", "yarn", "bun"].includes(toolID)) {
      return ["node", toolID];
    }
    return [toolID];
  }

  function requiredExecutorForPlan(plan: any = {}) {
    const explicit = normalizeExecutor(
      plan.executor ||
      plan.requiredExecutor ||
      plan.executionMode ||
      plan.executorMode
    );
    if (explicit) {
      return explicit;
    }
    if (String(plan.containerImage || "").trim()) {
      return "container";
    }
    return "native";
  }

  function commandExecutable(command) {
    const value = String(command || "").trim();
    if (!value) {
      return "";
    }
    const match = value.match(/^"([^"]+)"|'([^']+)'|(\S+)/);
    return match?.[1] || match?.[2] || match?.[3] || "";
  }

  function toolIDFromExecutable(value) {
    const executable = String(value || "").trim().replace(/\\/g, "/").split("/").filter(Boolean).pop() || "";
    return executable.toLowerCase().replace(/\.(exe|cmd|bat)$/i, "");
  }

  function localScriptExecutable(value) {
    return value.startsWith("./") || value.startsWith("../");
  }

  function stableWorkerKey(device: any = {}) {
    return String(device.id || device.name || "").trim();
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
    bestWorkerFromCandidates,
    compatibleWorkerForPlan,
    concreteDeviceID,
    isSingleAutoCandidate,
    singleConnectedWorkerTarget,
    workerResourceScore,
    workerToolReadiness,
    workerMatchesArchitecture,
    workerMatchesPlatform,
    workerRunTargetForAction,
    workerTargetAfterPairingConfirmation,
    deviceHasRequiredExecutor,
    deviceHasRequiredTools,
    requiredExecutorForPlan,
    missingToolIDsForPlan,
    requiredToolIDsForPlan,
    workerExecutorReadiness
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

  function normalizeTargetPreference(value) {
    const target = String(value || "").trim().toLowerCase();
    return target === "worker" || target === "local" ? target : "";
  }

  function numericHint(value) {
    const numeric = Number(value);
    if (!Number.isFinite(numeric) || numeric <= 0) {
      return 0;
    }
    return numeric;
  }

  function normalizeToolIDs(values) {
    if (typeof capabilityCatalog.normalizeToolIDs === "function") {
      return capabilityCatalog.normalizeToolIDs(values);
    }
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

  function normalizeExecutors(values) {
    if (!Array.isArray(values)) {
      return [];
    }
    const seen = new Set();
    return values
      .map((value) => normalizeExecutor(value))
      .filter(Boolean)
      .sort()
      .filter((value) => {
        if (seen.has(value)) {
          return false;
        }
        seen.add(value);
        return true;
      });
  }

  function normalizeExecutor(value) {
    const executor = String(value || "").trim().toLowerCase();
    if (value === 1 || executor === "1" || executor === "native" || executor === "executor_native") {
      return "native";
    }
    if (value === 2 || executor === "2" || executor === "container" || executor === "executor_container") {
      return "container";
    }
    return "";
  }
}));
