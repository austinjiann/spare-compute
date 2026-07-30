(function attachRunRequest(root, factory) {
  const outputPath = typeof module === "object" && module.exports
    ? require("./output-path")
    : root.computeHopOutputPath;
  const exports = factory(outputPath);
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopRunRequest = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createRunRequest(outputPath = {}) {
  const validatePortableOutputs = outputPath?.validatePortableOutputs || fallbackValidatePortableOutputs;

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
      outputs
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
    return block(cleanString(request.policyError));
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

  function block(message, actionKind = "", actionLabel = "") {
    return {
      message: cleanString(message),
      actionKind: cleanString(actionKind),
      actionLabel: cleanString(actionLabel)
    };
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
