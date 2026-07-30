function remotePreparationMessage(request = {}) {
  const workingDirectory = String(request.workingDirectory || "").trim();
  const target = displayTarget(request);
  if (!workingDirectory || !target) {
    return "";
  }
  return `Preparing remote run for ${target} from ${workingDirectory}; snapshot/upload may take a moment.`;
}

function displayTarget(request = {}) {
  const selector = String(request.deviceSelector || "").trim();
  if (!selector) {
    return "";
  }
  return String(request.deviceName || "").trim() || selector;
}

module.exports = {
  remotePreparationMessage
};
