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
    const existingByID = new Map();

    for (const job of existingJobs) {
      if (job?.id && !existingByID.has(job.id)) {
        existingByID.set(job.id, job);
      }
    }

    for (const job of fetchedJobs) {
      if (!job?.id || seen.has(job.id)) {
        continue;
      }
      seen.add(job.id);
      merged.push(mergeJobMetadata(job, existingByID.get(job.id)));
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

  function mergeJobMetadata(fetched, existing) {
    if (!existing) {
      return fetched;
    }
    return {
      ...fetched,
      deviceName: fetched.deviceName || existing.deviceName || "",
      deviceID: fetched.deviceID || existing.deviceID || "",
      workingDirectory: fetched.workingDirectory || existing.workingDirectory || ""
    };
  }

  return {
    mergeJobRefresh
  };
}));
