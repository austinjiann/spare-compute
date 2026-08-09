(function attachJobFailure(root, factory) {
  const exports = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopJobFailure = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createJobFailure() {
  const executableLabels = {
    bun: "Bun",
    cargo: "Cargo",
    docker: "Docker",
    ffmpeg: "FFmpeg",
    go: "Go",
    make: "Make",
    npm: "npm",
    pnpm: "pnpm",
    python: "Python",
    python3: "Python",
    swift: "Swift",
    yarn: "Yarn"
  };

  function friendlyJobFailure(job: any = {}, options: any = {}) {
    const failure = cleanString(job.failure);
    if (!failure) {
      return "";
    }

    const executable = cleanExecutable(job);
    const tool = toolLabel(executable);
    const target = cleanString(options.targetName) || "the selected computer";

    if (looksLikeMissingExecutable(failure, executable)) {
      return `${tool} is not installed on ${target}, or it is not on PATH. Install it there or choose a different computer.`;
    }

    if (looksLikeMissingWorkingDirectory(failure)) {
      return `The project folder was not available on ${target}. Choose the project again and retry.`;
    }

    const exitCode = processExitCode(failure);
    if (exitCode) {
      return `${commandLabel(job)} exited with code ${exitCode} on ${target}. Open logs for details.`;
    }

    return failure;
  }

  function looksLikeMissingExecutable(message, executable) {
    const value = message.toLowerCase();
    const name = cleanString(executable).toLowerCase();
    return (
      value.includes("executable file not found") ||
      (name && value.includes(`exec: "${name}"`) && value.includes("not found")) ||
      (name && value.includes(`start "${name}"`) && value.includes("not found"))
    );
  }

  function looksLikeMissingWorkingDirectory(message) {
    const value = message.toLowerCase();
    return value.includes("chdir") && value.includes("no such file or directory");
  }

  function processExitCode(message) {
    const match = String(message || "").match(/process exited with code\s+(-?\d+)/i);
    return match ? match[1] : "";
  }

  function cleanExecutable(job: any = {}) {
    const explicit = cleanString(job.executable);
    if (explicit) {
      return explicit;
    }
    return cleanString(job.command).split(/\s+/)[0] || "";
  }

  function toolLabel(executable) {
    const base = basename(executable).toLowerCase();
    return executableLabels[base] || basename(executable) || "That command";
  }

  function commandLabel(job: any = {}) {
    const command = cleanString(job.command);
    if (!command) {
      return "The command";
    }
    return command.length > 64 ? `${command.slice(0, 61)}…` : command;
  }

  function basename(value) {
    const text = cleanString(value);
    const parts = text.split(/[\\/]+/).filter(Boolean);
    return parts.length ? parts[parts.length - 1] : text;
  }

  function cleanString(value) {
    return String(value || "").trim();
  }

  return {
    friendlyJobFailure
  };
}));
