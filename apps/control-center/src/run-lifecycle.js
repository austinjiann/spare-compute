async function stopActiveRun(activeRuns, runID, options = {}) {
  const id = String(runID || "");
  const record = activeRuns.get(id);
  if (!record) {
    return { stopped: false, cancelled: false };
  }

  record.stopped = true;
  if (record.abortController && typeof record.abortController.abort === "function") {
    record.abortController.abort();
  }

  if (!record.jobID) {
    return { stopped: true, cancelled: false };
  }

  try {
    await record.client.cancelJob(record.jobID, {
      deviceSelector: record.deviceSelector
    });
    return { stopped: true, cancelled: true };
  } catch (error) {
    if (typeof options.onCancelError === "function") {
      options.onCancelError(record, id, error);
    }
    return { stopped: true, cancelled: false, error };
  }
}

function detachActiveRun(activeRuns, runID) {
  const id = String(runID || "");
  const record = activeRuns.get(id);
  if (!record) {
    return { detached: false, submitted: false };
  }

  record.detached = true;
  if (record.abortController && typeof record.abortController.abort === "function") {
    record.abortController.abort();
  }
  activeRuns.delete(id);
  return { detached: true, submitted: Boolean(record.jobID) };
}

function detachRunsForWebContents(activeRuns, webContents) {
  return detachRunIDs(activeRuns, runIDsForWebContents(activeRuns, webContents));
}

function detachAllRuns(activeRuns) {
  return detachRunIDs(activeRuns, [...activeRuns.keys()]);
}

function detachRunIDs(activeRuns, runIDs) {
  const results = [];
  for (const runID of runIDs) {
    results.push(detachActiveRun(activeRuns, runID));
  }
  return {
    detached: results.filter((result) => result.detached).length,
    submitted: results.filter((result) => result.submitted).length,
    results
  };
}

async function stopRunsForWebContents(activeRuns, webContents, options = {}) {
  return stopRunIDs(activeRuns, runIDsForWebContents(activeRuns, webContents), options);
}

async function stopAllRuns(activeRuns, options = {}) {
  return stopRunIDs(activeRuns, [...activeRuns.keys()], options);
}

async function stopRunIDs(activeRuns, runIDs, options = {}) {
  const results = [];
  for (const runID of runIDs) {
    results.push(await stopActiveRun(activeRuns, runID, options));
  }
  return {
    stopped: results.filter((result) => result.stopped).length,
    cancelled: results.filter((result) => result.cancelled).length,
    failed: results.filter((result) => result.error).length,
    results
  };
}

function runIDsForWebContents(activeRuns, webContents) {
  if (!webContents) {
    return [];
  }
  const targetID = webContentsID(webContents);
  const ids = [];
  for (const [runID, record] of activeRuns.entries()) {
    if (record.webContents === webContents) {
      ids.push(runID);
      continue;
    }
    if (targetID !== "" && webContentsID(record.webContents) === targetID) {
      ids.push(runID);
    }
  }
  return ids;
}

function webContentsID(webContents) {
  const id = webContents?.id;
  return id === undefined || id === null ? "" : String(id);
}

module.exports = {
  detachActiveRun,
  detachAllRuns,
  detachRunsForWebContents,
  runIDsForWebContents,
  stopActiveRun,
  stopAllRuns,
  stopRunsForWebContents
};
