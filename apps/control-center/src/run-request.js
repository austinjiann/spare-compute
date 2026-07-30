(function attachRunRequest(root, factory) {
  const outputPath = typeof module === "object" && module.exports
    ? require("./output-path")
    : root.computeHopOutputPath;
  const deviceTargets = typeof module === "object" && module.exports
    ? require("./device-targets")
    : root.computeHopDeviceTargets;
  const capabilityCatalog = typeof module === "object" && module.exports
    ? require("./capability-catalog")
    : root.computeHopCapabilityCatalog;
  const exports = factory(outputPath, deviceTargets, capabilityCatalog);
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopRunRequest = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createRunRequest(outputPath = {}, deviceTargets = {}, capabilityCatalog = {}) {
  const validatePortableOutputs = outputPath?.validatePortableOutputs || fallbackValidatePortableOutputs;
  const missingToolIDsForPlan = deviceTargets?.missingToolIDsForPlan || (() => []);
  const requiredToolIDsForPlan = deviceTargets?.requiredToolIDsForPlan || (() => []);

  function runWorkingDirectory(jobRequest) {
    return cleanPath(jobRequest?.workingDirectory);
  }

  function jobStartRequestForPlan(request = {}) {
    const plan = request.plan || {};
    const device = request.device || {};
    const outputs = jobOutputsForPlan({
      plan,
      outputs: request.outputs
    });
    return {
      command: cleanString(plan.command),
      deviceID: cleanString(device.id),
      deviceName: jobDeviceName(device),
      workingDirectory: jobWorkingDirectoryForPlan({
        projectRoot: request.projectRoot,
        plan,
        outputs
      }),
      outputs,
      requiredToolIDs: requiredToolIDsForPlan(plan)
    };
  }

  function jobDeviceName(device = {}) {
    if (cleanString(device.id) === "auto") {
      return cleanString(device.workerName) || stripAutoDetail(cleanString(device.detail)) || cleanString(device.name);
    }
    return cleanString(device.name);
  }

  function jobWorkingDirectoryForPlan(request = {}) {
    const projectRoot = cleanPath(request.projectRoot);
    if (!projectRoot) {
      return "";
    }
    const plan = request.plan || {};
    const outputs = jobOutputsForPlan(request);
    return plan.requiresProject || outputs.length > 0 ? projectRoot : "";
  }

  function jobOutputsForPlan(request = {}) {
    const plan = request.plan || {};
    if (plan.ignoreDeclaredOutputs) {
      return [];
    }
    return normalizeOutputs([
      ...arrayValues(plan.outputs),
      ...arrayValues(request.outputs)
    ]);
  }

  function runReadinessError(request = {}) {
    return runReadinessBlocker(request).message;
  }

  function runReadinessBlocker(request = {}) {
    const device = request.device || null;
    const plan = request.plan || {};
    const deviceName = cleanString(device?.name) || "that computer";
    const deviceID = cleanString(device?.id);
    const outputs = jobOutputsForPlan({
      plan,
      outputs: request.outputs
    });

    if (request.daemonAvailable === false) {
      return block("Start ComputeHop before running jobs.", "start-daemon", "Start");
    }
    if (device?.unavailableSelection) {
      return offlineWorkerBlock(deviceName);
    }
    if (!device || !request.canRun) {
      const deviceBlocker = selectedDeviceBlocker(device);
      if (deviceBlocker.message) {
        return deviceBlocker;
      }
      return block("Choose This Mac or a connected worker first.");
    }
    if (cleanString(plan.targetPreference) === "worker" && deviceID === "local") {
      return block(
        "This task was asked to run on another computer. Connect a worker or choose a worker from Devices first.",
        cleanString(request.workerTargetActionKind) || "refresh",
        cleanString(request.workerTargetActionLabel) || "Refresh",
        cleanString(request.workerTargetDeviceID)
      );
    }
    if (cleanString(plan.targetPreference) === "local" && deviceID !== "local") {
      return block("This task was asked to run here. Switch the run target to This Mac first.");
    }
    const platformBlocker = targetPlatformBlocker(device, plan);
    if (platformBlocker.message) {
      return platformBlocker;
    }
    const architectureBlocker = targetArchitectureBlocker(device, plan);
    if (architectureBlocker.message) {
      return architectureBlocker;
    }
    const toolBlocker = missingToolBlocker(device, plan);
    if (toolBlocker.message) {
      return toolBlocker;
    }
    if (plan.requiresProject && !cleanPath(request.projectRoot)) {
      if (deviceID !== "local") {
        return block(
          `Choose a project before running this on ${deviceName}. ComputeHop needs the folder so it can copy the files to that computer.`,
          "choose-project",
          "Choose project"
        );
      }
      return block(
        "Choose a project before running this. ComputeHop needs the folder so it can run from the right place.",
        "choose-project",
        "Choose project"
      );
    }
    const outputValidation = outputValidationForPlan(request);
    if (!outputValidation.ok) {
      return block(outputValidation.error);
    }
    if (outputs.length > 0 && !cleanPath(request.projectRoot)) {
      if (deviceID !== "local") {
        return block(
          "Choose a project before bringing files back from another computer.",
          "choose-project",
          "Choose project"
        );
      }
      return block(
        "Choose a project before bringing files back. ComputeHop needs the folder those outputs belong to.",
        "choose-project",
        "Choose project"
      );
    }
    return block(
      cleanString(request.policyError),
      request.policyError ? cleanString(request.policyActionKind) : "",
      request.policyError ? cleanString(request.policyActionLabel) : ""
    );
  }

  function selectedDeviceBlocker(device = {}) {
    const selected = device || {};
    const deviceName = cleanString(selected.name) || "that computer";
    if (!isRemoteWorker(selected)) {
      return block("");
    }
    if (selected.synced === false) {
      return block(`${deviceName} is paused for tasks. Enable it in Devices, or switch to This Mac.`, "enable-device", "Enable");
    }
    const availability = cleanString(selected.availability).toLowerCase();
    const connection = cleanString(selected.connection).toLowerCase();
    const trustState = cleanString(selected.trustState).toLowerCase();
    if (availability === "connecting") {
      return block(`${deviceName} is still connecting. Wait a moment, then try again.`, "refresh", "Refresh");
    }
    if ((trustState === "unpaired" || connection === "not connected") && availability === "nearby") {
      return block(`${deviceName} is nearby but not connected. Connect it from Devices first, or switch to This Mac.`, "connect-device", "Connect");
    }
    if (
      availability === "offline" ||
      connection === "offline" ||
      connection === "not connected" ||
      connection === "remote access off"
    ) {
      return offlineWorkerBlock(deviceName);
    }
    return block(`${deviceName} cannot run tasks yet. Choose a connected worker or switch to This Mac.`);
  }

  function targetPlatformBlocker(device = {}, plan = {}) {
    const target = normalizeTargetPlatform(plan.targetPlatform || plan.requiredPlatform);
    if (!target) {
      return block("");
    }
    const actual = normalizeTargetPlatform(device.platform || device.os);
    if (!actual || actual === target) {
      return block("");
    }
    const label = platformLabel(target);
    return block(`This task needs ${label}. Choose a ${label} computer first.`);
  }

  function targetArchitectureBlocker(device = {}, plan = {}) {
    const target = normalizeTargetArchitecture(plan.targetArchitecture || plan.requiredArchitecture || plan.targetArch || plan.requiredArch);
    if (!target) {
      return block("");
    }
    const actual = normalizeTargetArchitecture(device.arch || device.architecture);
    if (!actual || actual === target) {
      return block("");
    }
    const label = architectureLabel(target);
    return block(`This task needs ${label}. Choose ${architectureArticle(label)} ${label} computer first.`);
  }

  function missingToolBlocker(device = {}, plan = {}) {
    const missing = missingToolIDsForPlan(device, plan);
    if (!Array.isArray(missing) || missing.length === 0) {
      return block("");
    }
    const deviceName = cleanString(device?.name) || "that computer";
    return block(`${deviceName} does not report ${toolListLabel(missing)}. Choose another computer or install it there.`);
  }

  function offlineWorkerBlock(deviceName) {
    return block(
      `${deviceName} is not reachable. Open ComputeHop on that computer and keep both computers on the same network, then try again. For different networks, set up VPS connectivity.`,
      "refresh",
      "Refresh"
    );
  }

  function isRemoteWorker(device = {}) {
    const id = cleanString(device.id);
    return id && id !== "local" && id !== "auto" && cleanString(device.role) === "worker";
  }

  function block(message, actionKind = "", actionLabel = "", deviceID = "") {
    const result = {
      message: cleanString(message),
      actionKind: cleanString(actionKind),
      actionLabel: cleanString(actionLabel)
    };
    const targetDeviceID = cleanString(deviceID);
    if (targetDeviceID) {
      result.deviceID = targetDeviceID;
    }
    return result;
  }

  function normalizeOutputs(outputs) {
    if (!Array.isArray(outputs)) {
      return [];
    }
    const seen = new Set();
    const normalized = [];
    outputs.forEach((value) => {
      const output = String(value || "").trim();
      if (!output || seen.has(output)) {
        return;
      }
      seen.add(output);
      normalized.push(output);
    });
    return normalized;
  }

  function outputValidationForPlan(request = {}) {
    const plan = request.plan || {};
    if (plan.ignoreDeclaredOutputs) {
      return { ok: true, outputs: [], error: "" };
    }
    return validatePortableOutputs([
      ...arrayValues(plan.outputs),
      ...arrayValues(request.outputs)
    ]);
  }

  function arrayValues(value) {
    return Array.isArray(value) ? value : [];
  }

  function cleanPath(value) {
    return String(value || "").trim();
  }

  function cleanString(value) {
    return String(value || "").trim();
  }

  function stripAutoDetail(value) {
    return value.replace(/^uses\s+/i, "").trim();
  }

  function normalizeTargetPlatform(value) {
    const platform = cleanString(value).toLowerCase();
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

  function platformLabel(platform) {
    switch (normalizeTargetPlatform(platform)) {
      case "darwin":
        return "macOS";
      case "windows":
        return "Windows";
      case "linux":
        return "Linux";
      default:
        return "that OS";
    }
  }

  function normalizeTargetArchitecture(value) {
    const architecture = cleanString(value).toLowerCase();
    if (["arm64", "aarch64", "apple-silicon", "apple silicon"].includes(architecture)) {
      return "arm64";
    }
    if (["amd64", "x64", "x86_64", "x86-64", "intel"].includes(architecture)) {
      return "amd64";
    }
    return "";
  }

  function architectureLabel(architecture) {
    switch (normalizeTargetArchitecture(architecture)) {
      case "arm64":
        return "arm64";
      case "amd64":
        return "x64";
      default:
        return "that architecture";
    }
  }

  function architectureArticle(label) {
    return /^[aeiou]/i.test(cleanString(label)) || cleanString(label).toLowerCase() === "x64" ? "an" : "a";
  }

  function toolListLabel(values) {
    if (typeof capabilityCatalog.toolListLabel === "function") {
      return capabilityCatalog.toolListLabel(values);
    }
    const labels = values.map((value) => cleanString(value)).filter(Boolean);
    if (labels.length === 0) {
      return "the needed tool";
    }
    if (labels.length === 1) {
      return labels[0];
    }
    return `${labels.slice(0, -1).join(", ")} and ${labels[labels.length - 1]}`;
  }

  function fallbackValidatePortableOutputs(outputs) {
    return {
      ok: true,
      outputs: normalizeOutputs(outputs),
      error: ""
    };
  }

  return {
    jobDeviceName,
    jobStartRequestForPlan,
    jobOutputsForPlan,
    jobWorkingDirectoryForPlan,
    outputValidationForPlan,
    runReadinessBlocker,
    runReadinessError,
    runWorkingDirectory
  };
}));
