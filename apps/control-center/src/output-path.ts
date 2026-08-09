(function attachOutputPath(root, factory) {
  const exports = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopOutputPath = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createOutputPath() {
  const maximumOutputs = 64;
  const maximumPathBytes = 4096;
  const unsafeCharacters = /[\\\0<>:"|?*]/;
  const reservedSegments = new Set([".git", ".computehop-results", ".computehop-conflicts"]);

  function normalizeOutputs(outputs) {
    if (!Array.isArray(outputs)) {
      return [];
    }
    return outputs
      .map((value) => String(value || "").trim())
      .filter(Boolean);
  }

  function validatePortableOutputs(outputs) {
    const normalized = normalizeOutputs(outputs);
    if (normalized.length > maximumOutputs) {
      return {
        ok: false,
        outputs: normalized.slice(0, maximumOutputs),
        error: `Bring back at most ${maximumOutputs} files or folders.`
      };
    }

    const seen = new Map();
    for (const output of normalized) {
      const pathError = portableOutputPathError(output);
      if (pathError) {
        return {
          ok: false,
          outputs: normalized,
          error: pathError
        };
      }

      const key = output.toLowerCase();
      const existing = seen.get(key);
      if (existing) {
        return {
          ok: false,
          outputs: normalized,
          error: `Output paths "${existing}" and "${output}" collide on case-insensitive filesystems.`
        };
      }
      seen.set(key, output);
    }

    return {
      ok: true,
      outputs: [...seen.values()],
      error: ""
    };
  }

  function portableOutputPathError(value) {
    const output = String(value || "").trim();
    if (!output) {
      return "";
    }
    if (utf8ByteLength(output) > maximumPathBytes || hasUnpairedSurrogate(output)) {
      return `Output path "${output}" is too long or malformed.`;
    }
    if (
      output.startsWith("/") ||
      output === "." ||
      output === ".." ||
      output.startsWith("../")
    ) {
      return `Output path "${output}" must be relative to the selected project.`;
    }
    if (unsafeCharacters.test(output)) {
      return `Output path "${output}" contains characters that are unsafe across Mac, Windows, and Linux.`;
    }

    const segments = output.split("/");
    if (segments.some((segment) => segment === "" || segment === "." || segment === "..")) {
      return `Output path "${output}" must not contain empty segments or parent directories.`;
    }
    for (const segment of segments) {
      if (segment.endsWith(".") || segment.endsWith(" ")) {
        return `Output path "${output}" contains a segment that ends with a dot or space.`;
      }
      if (reservedSegments.has(segment.toLowerCase())) {
        return `Output path "${output}" uses a reserved ComputeHop or Git folder.`;
      }
      if (windowsReservedName(segment)) {
        return `Output path "${output}" uses a Windows-reserved name.`;
      }
      for (const character of segment) {
        if (character.codePointAt(0) < 0x20) {
          return `Output path "${output}" contains a control character.`;
        }
      }
    }

    return "";
  }

  function utf8ByteLength(value) {
    if (typeof Buffer !== "undefined") {
      return Buffer.byteLength(value, "utf8");
    }
    return new TextEncoder().encode(value).length;
  }

  function hasUnpairedSurrogate(value) {
    for (let index = 0; index < value.length; index += 1) {
      const code = value.charCodeAt(index);
      if (code >= 0xd800 && code <= 0xdbff) {
        const next = value.charCodeAt(index + 1);
        if (!(next >= 0xdc00 && next <= 0xdfff)) {
          return true;
        }
        index += 1;
        continue;
      }
      if (code >= 0xdc00 && code <= 0xdfff) {
        return true;
      }
    }
    return false;
  }

  function windowsReservedName(segment) {
    const base = segment.split(".", 1)[0].toUpperCase();
    if (["CON", "PRN", "AUX", "NUL"].includes(base)) {
      return true;
    }
    return /^(COM|LPT)[1-9]$/.test(base);
  }

  return {
    maximumOutputs,
    normalizeOutputs,
    portableOutputPathError,
    validatePortableOutputs
  };
}));
