const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("computeHop", {
  listDevices: () => ipcRenderer.invoke("devices:list"),
  connectDevice: (deviceID) => ipcRenderer.invoke("devices:connect", deviceID),
  forgetDevice: (deviceID) => ipcRenderer.invoke("devices:forget", deviceID),
  confirmPairing: (pairingID) => ipcRenderer.invoke("pairings:confirm", pairingID),
  rejectPairing: (pairingID) => ipcRenderer.invoke("pairings:reject", pairingID),
  planTask: (request) => ipcRenderer.invoke("planner:plan", request),
  startJob: (request) => ipcRenderer.invoke("jobs:start", request),
  stopJob: (runID) => ipcRenderer.invoke("jobs:stop", runID),
  onJobEvent: (handler) => {
    const listener = (_event, payload) => handler(payload);
    ipcRenderer.on("jobs:event", listener);
    return () => ipcRenderer.removeListener("jobs:event", listener);
  },
  chooseProject: () => ipcRenderer.invoke("project:choose"),
  openExternal: (target) => ipcRenderer.invoke("app:openExternal", target)
});
