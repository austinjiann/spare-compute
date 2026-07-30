(function attachJobList(root, factory) {
  const exports = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopJobList = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createJobList() {
  function mergeJobRefresh(fetchedJobs = [], existingJobs = []) {
    const merged = [];
    const seen = new Set();

    for (const job of fetchedJobs) {
      if (!job?.id || seen.has(job.id)) {
        continue;
      }
      seen.add(job.id);
      merged.push(job);
    }

    for (const job of existingJobs) {
      if (!job?.id || seen.has(job.id) || job.terminal) {
        continue;
      }
      seen.add(job.id);
      merged.push(job);
    }

    return merged;
  }

  return {
    mergeJobRefresh
  };
}));
