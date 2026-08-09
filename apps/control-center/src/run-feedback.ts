function remotePreparationMessage(request: any = {}) {
  const workingDirectory = String(request.workingDirectory || "").trim();
  const target = displayTarget(request);
  if (!workingDirectory || !target) {
    return "";
  }
  return `Preparing remote run for ${target} from ${workingDirectory}; snapshot/upload may take a moment.`;
}

function displayTarget(request: any = {}) {
  const selector = String(request.deviceSelector || "").trim();
  if (!selector) {
    return "";
  }
  return String(request.deviceName || "").trim() || selector;
}

function friendlyRunError(error: any = {}) {
  const message = cleanString(error?.message || error);
  if (!message) {
    return "ComputeHop request failed.";
  }
  if (!looksLikeWorkerUnavailable(error, message)) {
    return message;
  }
  if (looksLikeNoActiveWorker(message)) {
    return "No connected worker is available. Start ComputeHop on the worker, connect it from Devices, then try again.";
  }
  const worker = unavailableWorkerName(message);
  const subject = worker ? `${worker} is not reachable` : "The worker is not reachable";
  return `${subject}. Start ComputeHop on that computer and keep both computers on the same network, then try again. For different networks, set up VPS connectivity.`;
}

function looksLikeWorkerUnavailable(error, message) {
  const code = cleanString(error?.code).toUpperCase();
  const lower = message.toLowerCase();
  return (
    code === "ERROR_CODE_DEVICE_UNAVAILABLE" ||
    lower.includes("paired worker is unavailable") ||
    lower.includes("remote connectivity path is unavailable") ||
    lower.includes("worker is not reachable")
  );
}

function looksLikeNoActiveWorker(message) {
  const lower = message.toLowerCase();
  return lower.includes("no active paired worker") || lower.includes("no connected worker");
}

function unavailableWorkerName(message) {
  const withoutPrefix = cleanString(message).replace(/^paired worker is unavailable:\s*/i, "");
  const unreachable = withoutPrefix.match(/^(.+?)\s+is not reachable\b/i);
  if (unreachable?.[1]) {
    return cleanWorkerName(unreachable[1]);
  }
  const pathUnavailable = withoutPrefix.match(/^(.+?):\s*remote connectivity path is unavailable\b/i);
  if (pathUnavailable?.[1]) {
    return cleanWorkerName(pathUnavailable[1]);
  }
  return "";
}

function cleanWorkerName(value) {
  const name = cleanString(value)
    .replace(/^worker\s+/i, "")
    .replace(/[.。]+$/g, "");
  if (!name || looksLikeNoActiveWorker(name)) {
    return "";
  }
  return name;
}

function cleanString(value) {
  return String(value || "").trim();
}

module.exports = {
  friendlyRunError,
  remotePreparationMessage
};
