(function attachSuggestionPlan(root, factory) {
  const exports = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopSuggestionPlan = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createSuggestionPlan() {
  function planFromSuggestion(suggestion = {}, projectRoot = "") {
    const source = cleanString(suggestion.task || suggestion.title || suggestion.label);
    return {
      source,
      title: cleanString(suggestion.title || suggestion.label) || "Planned command",
      command: cleanString(suggestion.command),
      detail: cleanString(suggestion.detail),
      requiresProject: Boolean(suggestion.requiresProject),
      outputs: arrayValues(suggestion.outputs),
      requiredToolIDs: arrayValues(suggestion.requiredToolIDs || suggestion.requiredToolIds),
      targetPlatform: cleanString(suggestion.targetPlatform || suggestion.requiredPlatform),
      targetArchitecture: cleanString(
        suggestion.targetArchitecture ||
        suggestion.requiredArchitecture ||
        suggestion.targetArch ||
        suggestion.requiredArch
      ),
      targetPreference: cleanString(suggestion.targetPreference),
      projectRoot: cleanString(projectRoot),
      detected: arrayValues(suggestion.detected)
    };
  }

  function arrayValues(value) {
    return Array.isArray(value)
      ? value.map((item) => cleanString(item)).filter(Boolean)
      : [];
  }

  function cleanString(value) {
    return String(value || "").trim();
  }

  return {
    planFromSuggestion
  };
}));
