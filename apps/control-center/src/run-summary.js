(function attachRunSummary(root, factory) {
  const exports = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopRunSummary = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createRunSummary() {
  function runSummaryLines(request = {}) {
    const plan = request.plan || {};
    const device = request.device || {};
    const outputs = normalizeOutputs(request.outputs);
    const projectRoot = cleanString(request.projectRoot);
    const remote = isRemoteDevice(device);
    const lines = [remote ? `Runs on ${deviceName(device)}` : "Runs here"];

    if (projectRoot && (plan.requiresProject || outputs.length > 0)) {
      lines.push(remote ? `Copies ${shortPath(projectRoot)}` : `Uses ${shortPath(projectRoot)}`);
    } else if (remote) {
      lines.push("No project files");
    }

    if (outputs.length > 0) {
      lines.push(`Brings back ${joinList(outputs)}`);
    }

    return lines;
  }

  function initialRunMessage(request = {}) {
    const command = cleanString(request.command) || "task";
    const device = cleanString(request.deviceName) || "This Mac";
    const workingDirectory = cleanString(request.workingDirectory);
    const outputs = normalizeOutputs(request.outputs);
    const lines = [`Running ${command} on ${device}…`];

    if (workingDirectory) {
      lines.push(`Project: ${shortPath(workingDirectory)}`);
    }
    if (outputs.length > 0) {
      lines.push(`Will bring back: ${joinList(outputs)}`);
    }

    return lines.join("\n");
  }

  function deviceName(device = {}) {
    return (
      cleanString(device.workerName) ||
      stripAutoDetail(cleanString(device.detail)) ||
      cleanString(device.name) ||
      "the worker"
    );
  }

  function isRemoteDevice(device = {}) {
    const id = cleanString(device.id);
    return Boolean(id && id !== "local");
  }

  function shortPath(value) {
    const text = cleanString(value);
    if (!text) {
      return "";
    }
    const parts = text.split(/[\\/]+/).filter(Boolean);
    if (parts.length <= 2) {
      return text;
    }
    return parts.slice(-2).join("/");
  }

  function joinList(values) {
    const normalized = normalizeOutputs(values);
    if (normalized.length <= 3) {
      return normalized.join(", ");
    }
    return `${normalized.slice(0, 3).join(", ")} +${normalized.length - 3}`;
  }

  function normalizeOutputs(outputs) {
    if (!Array.isArray(outputs)) {
      return [];
    }
    const seen = new Set();
    const result = [];
    outputs.forEach((value) => {
      const output = cleanString(value);
      if (!output || seen.has(output)) {
        return;
      }
      seen.add(output);
      result.push(output);
    });
    return result;
  }

  function stripAutoDetail(value) {
    return value.replace(/^uses\s+/i, "").trim();
  }

  function cleanString(value) {
    return String(value || "").trim();
  }

  return {
    initialRunMessage,
    runSummaryLines,
    shortPath
  };
}));
