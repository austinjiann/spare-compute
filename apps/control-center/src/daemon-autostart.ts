(function attachDaemonAutostart(root, factory) {
  const exports = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = exports;
  } else {
    root.computeHopDaemonAutostart = exports;
  }
}(typeof globalThis === "object" ? globalThis : window, function createDaemonAutostart() {
  function shouldAutoStartDaemon(state: any = {}) {
    if (state.daemonAvailable) {
      return false;
    }
    if (state.startingDaemon) {
      return false;
    }
    if (state.autoStartAttempted) {
      return false;
    }
    if (!state.runtimeLoaded || !state.settingsHydrated) {
      return false;
    }
    if (state.settings?.lanDiscovery === false) {
      return false;
    }
    return true;
  }

  return {
    shouldAutoStartDaemon
  };
}));
