(function attachCapabilityCatalog(root, factory) {
  const exports = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopCapabilityCatalog = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createCapabilityCatalog() {
  const catalogSchemaVersion = 1;

  const toolDescriptors = [
    ["blender", "Blender", "video"],
    ["bun", "Bun", "build"],
    ["cargo", "Cargo", "build"],
    ["cmake", "CMake", "build"],
    ["docker", "Docker", "container"],
    ["docker-compose", "Docker Compose", "container"],
    ["echo", "Echo", "utility"],
    ["ffmpeg", "FFmpeg", "video"],
    ["git", "Git", "build"],
    ["go", "Go", "build"],
    ["hostname", "Hostname", "utility"],
    ["make", "Make", "build"],
    ["mypy", "mypy", "test"],
    ["ninja", "Ninja", "build"],
    ["node", "Node.js", "build"],
    ["npm", "npm", "build"],
    ["ollama", "Ollama", "ai"],
    ["pnpm", "pnpm", "build"],
    ["podman", "Podman", "container"],
    ["poetry", "Poetry", "build"],
    ["printf", "printf", "utility"],
    ["pytest", "pytest", "test"],
    ["python", "Python", "build"],
    ["python3", "Python 3", "build"],
    ["ruff", "Ruff", "test"],
    ["sh", "Shell", "utility"],
    ["sleep", "Sleep", "utility"],
    ["swift", "Swift", "build"],
    ["uv", "uv", "build"],
    ["xcodebuild", "Xcode", "build"],
    ["yarn", "Yarn", "build"]
  ].map(([id, label, category]) => ({ id, label, category, schemaVersion: catalogSchemaVersion }));

  const descriptorByID = new Map(toolDescriptors.map((descriptor) => [descriptor.id, descriptor]));

  function commonToolIDs() {
    return toolDescriptors.map((descriptor) => descriptor.id);
  }

  function toolLabel(toolID) {
    const id = String(toolID || "").trim().toLowerCase();
    return descriptorByID.get(id)?.label || id || "unknown tool";
  }

  function toolListLabel(toolIDs) {
    const labels = normalizeToolIDs(toolIDs).map(toolLabel);
    if (labels.length === 0) {
      return "the needed tools";
    }
    if (labels.length === 1) {
      return labels[0];
    }
    if (labels.length === 2) {
      return `${labels[0]} and ${labels[1]}`;
    }
    return `${labels.slice(0, -1).join(", ")}, and ${labels[labels.length - 1]}`;
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

  return {
    catalogSchemaVersion,
    commonToolIDs,
    normalizeToolIDs,
    toolDescriptors,
    toolLabel,
    toolListLabel
  };
}));
