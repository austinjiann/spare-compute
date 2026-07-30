(function attachWorkPolicy(root, factory) {
  const exports = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopWorkPolicy = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createWorkPolicy() {
  const capabilityLabels = {
    builds: "Builds",
    tests: "Tests",
    docker: "Docker",
    ai: "AI",
    video: "Video",
    commands: "Exact commands"
  };

  function filterAllowedSuggestions(suggestions = [], capabilities = {}) {
    return suggestions.filter((suggestion) => isWorkAllowed(suggestion, capabilities));
  }

  function disallowedWorkMessage(plan, capabilities = {}) {
    const capability = capabilityForWork(plan);
    if (!capability || capabilities[capability] !== false) {
      return "";
    }
    return `${capabilityLabels[capability] || "This work"} is turned off for this computer. Enable it in Allow, or choose a different task.`;
  }

  function isWorkAllowed(work, capabilities = {}) {
    return !disallowedWorkMessage(work, capabilities);
  }

  function capabilityForWork(work) {
    const explicit = normalizeCapability(work?.capability);
    if (explicit) {
      return explicit;
    }
    if (work?.exact === true && isSafeUtilityCommand(work?.command || "")) {
      return "";
    }
    const inferred = capabilityForCommand(work?.command || work?.task || work?.title || "");
    if (inferred) {
      return inferred;
    }
    return work?.exact === true ? "commands" : "";
  }

  function capabilityForCommand(command) {
    const value = String(command || "").trim().toLowerCase();
    if (!value) {
      return "";
    }
    if (/^(docker|podman)(?:\s|$)/.test(value) || /\bcompose\s+build\b/.test(value)) {
      return "docker";
    }
    if (/^(ffmpeg|handbrakecli|blender)(?:\s|$)/.test(value)) {
      return "video";
    }
    if (/^(ollama|comfyui)(?:\s|$)/.test(value) || /\b(stable-diffusion|flux|embeddings?|inference|fine[- ]?tune)\b/.test(value)) {
      return "ai";
    }
    if (
      /^make\s+(pr-check|check|test|lint|fmt|vet)(?:\s|$)/.test(value) ||
      /^(npm|pnpm|yarn|bun)\s+(run\s+)?(test|lint|check|ci)(?:\s|$)/.test(value) ||
      /^go\s+(test|vet)(?:\s|$)/.test(value) ||
      /^swift\s+test(?:\s|$)/.test(value) ||
      /^cargo\s+(test|clippy|check)(?:\s|$)/.test(value) ||
      /^(pytest|ruff|mypy)(?:\s|$)/.test(value) ||
      /^python\s+(-m\s+)?(pytest|ruff|mypy)(?:\s|$)/.test(value)
    ) {
      return "tests";
    }
    if (
      /^make\s+([a-z0-9_.-]+-)?(build|bundle|package)(?:\s|$)/.test(value) ||
      /^(npm|pnpm|yarn|bun)\s+(run\s+)?(build|bundle|package)(?:\s|$)/.test(value) ||
      /^go\s+build(?:\s|$)/.test(value) ||
      /^swift\s+build(?:\s|$)/.test(value) ||
      /^cargo\s+build(?:\s|$)/.test(value)
    ) {
      return "builds";
    }
    return "";
  }

  function isSafeUtilityCommand(command) {
    const value = String(command || "").trim().toLowerCase();
    return (
      /^(?:\/usr\/bin\/|\/bin\/)?(hostname|whoami|date|pwd)$/.test(value) ||
      /^(?:\/usr\/bin\/|\/bin\/)?uname(?:\s+-(a|m|n|r|s))?$/.test(value)
    );
  }

  function normalizeCapability(value) {
    const text = String(value || "").trim();
    return Object.prototype.hasOwnProperty.call(capabilityLabels, text) ? text : "";
  }

  return {
    capabilityForCommand,
    capabilityForWork,
    disallowedWorkMessage,
    filterAllowedSuggestions,
    isSafeUtilityCommand,
    isWorkAllowed
  };
}));
