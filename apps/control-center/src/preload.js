const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("computeHop", {
  listDevices: () => ipcRenderer.invoke("devices:list"),
  runJob: (request) => ipcRenderer.invoke("jobs:run", request),
  chooseProject: () => ipcRenderer.invoke("project:choose"),
  openExternal: (target) => ipcRenderer.invoke("app:openExternal", target)
});
