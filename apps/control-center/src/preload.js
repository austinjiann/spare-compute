const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("computeHop", {
  listDevices: () => ipcRenderer.invoke("devices:list"),
  openExternal: (target) => ipcRenderer.invoke("app:openExternal", target)
});
