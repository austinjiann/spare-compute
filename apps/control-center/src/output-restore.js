(function attachOutputRestore(root, factory) {
  const exports = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopOutputRestore = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createOutputRestore() {
  function outputRestoreDefaultPath(job, settings = {}) {
    return cleanPath(job?.workingDirectory) || cleanPath(settings.projectRoot);
  }

  function shouldOfferOutputRestore(request = {}) {
    const job = request.job || null;
    return Boolean(
      request.ok &&
      job?.id &&
      job.canFetchOutputs &&
      !request.alreadyOffered
    );
  }

  function cleanPath(value) {
    return String(value || "").trim();
  }

  return {
    outputRestoreDefaultPath,
    shouldOfferOutputRestore
  };
}));
