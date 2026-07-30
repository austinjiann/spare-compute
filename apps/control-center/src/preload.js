const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("computeHop", {
  listDevices: () => ipcRenderer.invoke("devices:list"),
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
