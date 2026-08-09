const { jobOutputsForPlan, outputValidationForPlan } = require("./run-request");

function normalizeJobRequest(request) {
  if (!request || typeof request !== "object") {
    throw new Error("Job request is required.");
  }
  const outputValidation = outputValidationForPlan({ outputs: request.outputs });
  if (!outputValidation.ok) {
    throw new Error(outputValidation.error);
  }
  const executor = normalizeExecutor(request.executor);
  return {
    command: String(request.command || "").trim(),
    deviceID: String(request.deviceID || "local").trim(),
    deviceName: String(request.deviceName || "").trim(),
    workingDirectory: String(request.workingDirectory || "").trim(),
    outputs: jobOutputsForPlan({ outputs: request.outputs }),
    requiredToolIDs: normalizeToolIDs(request.requiredToolIDs || request.requiredToolIds),
    executor,
    containerImage: normalizeContainerImage(request.containerImage, executor)
  };
}

function normalizeExecutor(value) {
  const executor = String(value || "").trim().toLowerCase();
  return executor === "container" ? "container" : "native";
}

function normalizeContainerImage(value, executor) {
  const image = String(value || "").trim();
  return executor === "container" ? image : "";
}

function normalizeToolIDs(values) {
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

module.exports = {
  normalizeExecutor,
  normalizeJobRequest,
  normalizeToolIDs
};
