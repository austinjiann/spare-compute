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
    const outputs = Array.isArray(request.outputs)
      ? request.outputs.map((value) => String(value || "").trim()).filter(Boolean)
      : [];
    return plan.requiresProject || outputs.length > 0 ? projectRoot : "";
  }

  function cleanPath(value) {
    return String(value || "").trim();
  }

  return {
    jobWorkingDirectoryForPlan,
    runWorkingDirectory
  };
}));
