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

  return {
    jobOutputsForPlan,
    jobWorkingDirectoryForPlan,
    runWorkingDirectory
  };
}));
