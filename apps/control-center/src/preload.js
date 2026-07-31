const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("computeHop", {
  appInfo: () => ipcRenderer.invoke("app:info"),
  loadSettings: () => ipcRenderer.invoke("settings:load"),
  saveSettings: (settings) => ipcRenderer.invoke("settings:save", settings),
  aiPlannerStatus: () => ipcRenderer.invoke("aiPlanner:status"),
  saveAIPlanner: (request) => ipcRenderer.invoke("aiPlanner:save", request),
  clearAIPlanner: () => ipcRenderer.invoke("aiPlanner:clear"),
  startDaemon: (request) => ipcRenderer.invoke("daemon:start", request),
  daemonStatus: () => ipcRenderer.invoke("daemon:status"),
  launchAgentStatus: (request) => ipcRenderer.invoke("daemon:launchAgentStatus", request),
  installLaunchAgent: (request) => ipcRenderer.invoke("daemon:installLaunchAgent", request),
  listDevices: () => ipcRenderer.invoke("devices:list"),
  connectDevice: (deviceID) => ipcRenderer.invoke("devices:connect", deviceID),
  forgetDevice: (deviceID) => ipcRenderer.invoke("devices:forget", deviceID),
  confirmPairing: (pairingID) => ipcRenderer.invoke("pairings:confirm", pairingID),
  rejectPairing: (pairingID) => ipcRenderer.invoke("pairings:reject", pairingID),
  planTask: (request) => ipcRenderer.invoke("planner:plan", request),
  suggestTasks: (request) => ipcRenderer.invoke("planner:suggest", request),
  startJob: (request) => ipcRenderer.invoke("jobs:start", request),
  stopJob: (runID) => ipcRenderer.invoke("jobs:stop", runID),
  listJobs: (request) => ipcRenderer.invoke("jobs:list", request),
  readJobLogs: (request) => ipcRenderer.invoke("jobs:logs", request),
  cancelJob: (request) => ipcRenderer.invoke("jobs:cancel", request),
  fetchOutputs: (request) => ipcRenderer.invoke("jobs:fetchOutputs", request),
  chooseOutputDestination: (request) => ipcRenderer.invoke("outputs:chooseDestination", request),
  onJobEvent: (handler) => {
    const listener = (_event, payload) => handler(payload);
    ipcRenderer.on("jobs:event", listener);
    return () => ipcRenderer.removeListener("jobs:event", listener);
  },
  chooseProject: () => ipcRenderer.invoke("project:choose"),
  openExternal: (target) => ipcRenderer.invoke("app:openExternal", target)
});
