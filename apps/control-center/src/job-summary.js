const { formatCommandLine } = require("./command-line");
const {
  jobStateLabel,
  jobSucceeded,
  jobTerminal
} = require("./local-daemon");
const { jobDeviceIDForSelector } = require("./job-routing");

function mapJob(value, deviceID = "") {
  if (!value) {
    return null;
  }
  const spec = value.spec || {};
  const args = spec.arguments || [];
  const command = formatCommandLine([spec.executable, ...args]) || "Task";
  const outputs = spec.outputs || [];
  return {
    id: value.id || "",
    shortID: String(value.id || "").slice(0, 8),
    command,
    executable: spec.executable || "",
    arguments: args,
    workingDirectory: String(spec.workingDirectory || "").trim(),
    outputs,
    state: jobStateLabel(value),
    terminal: jobTerminal(value),
    succeeded: jobSucceeded(value),
    canCancel: !jobTerminal(value),
    canFetchOutputs: jobSucceeded(value) && outputs.length > 0,
    progress: progressLabel(value.progress),
    failure: value.failure?.message || "",
    updated: timestampLabel(value.updatedAtUnixNano),
    created: timestampLabel(value.createdAtUnixNano),
    deviceID: jobDeviceIDForSelector(deviceID)
  };
}

function progressLabel(progress) {
  if (!progress) {
    return "";
  }
  const phase = String(progress.phase || "")
    .replace(/^JOB_PROGRESS_PHASE_/, "")
    .toLowerCase();
  const completed = Number(progress.completedBytes || 0);
  const total = Number(progress.totalBytes || 0);
  if (total > 0) {
    return `${phase || "progress"} ${Math.round((completed / total) * 100)}%`;
  }
  return phase === "unspecified" ? "" : phase;
}

function timestampLabel(value) {
  if (!value) {
    return "";
  }
  const numeric = Number(value);
  if (!Number.isFinite(numeric) || numeric <= 0) {
    return "";
  }
  return new Date(Math.floor(numeric / 1_000_000)).toISOString();
}

module.exports = {
  mapJob,
  progressLabel,
  timestampLabel
};
