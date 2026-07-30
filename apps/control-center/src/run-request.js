(function attachRunRequest(root, factory) {
  const exports = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopRunRequest = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createRunRequest() {
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
      deviceName: cleanString(device.name),
      workingDirectory: jobWorkingDirectoryForPlan({
        projectRoot: request.projectRoot,
        plan,
        outputs
      }),
      outputs
    };
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
    return normalizeOutputs(request.outputs);
  }

  function runReadinessError(request = {}) {
    const device = request.device || null;
    const plan = request.plan || {};
    const deviceName = cleanString(device?.name) || "that computer";
    const deviceID = cleanString(device?.id);
    const outputs = jobOutputsForPlan({
      plan,
      outputs: request.outputs
    });

    if (!device || !request.canRun) {
      return "Choose This Mac or a connected worker first.";
    }
    if (deviceID !== "local" && plan.requiresProject && !cleanPath(request.projectRoot)) {
      return `Choose a project before running this on ${deviceName}. ComputeHop needs the folder so it can copy the files to that computer.`;
    }
    if (deviceID !== "local" && outputs.length > 0 && !cleanPath(request.projectRoot)) {
      return "Choose a project before bringing files back from another computer.";
    }
    return cleanString(request.policyError);
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

  function cleanPath(value) {
    return String(value || "").trim();
  }

  function cleanString(value) {
    return String(value || "").trim();
  }

  return {
    jobStartRequestForPlan,
    jobOutputsForPlan,
    jobWorkingDirectoryForPlan,
    runReadinessError,
    runWorkingDirectory
  };
}));
