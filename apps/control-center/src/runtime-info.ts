function appRuntimeInfo(platform = process.platform) {
  const roles = daemonRolesForPlatform(platform);
  return {
    platform,
    defaultDaemonRole: roles[0].id,
    daemonRoles: roles
  };
}

function daemonRolesForPlatform(platform = process.platform) {
  if (platform === "darwin") {
    return [
      { id: "orchestrator", label: "Control Mac" },
      { id: "worker", label: "Worker" }
    ];
  }

  return [
    { id: "worker", label: "Worker" }
  ];
}

function normalizeDaemonRole(value, platform = process.platform) {
  const role = String(value || "").trim().toLowerCase();
  const roles = daemonRolesForPlatform(platform);
  return roles.some((candidate) => candidate.id === role) ? role : roles[0].id;
}

module.exports = {
  appRuntimeInfo,
  daemonRolesForPlatform,
  normalizeDaemonRole
};
