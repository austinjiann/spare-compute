function mapDevices(result = {}) {
  const rawTrustedDevices = result.trustedDevices || [];
  const nearbyDevices = result.devices || [];
  const nearbyByKey = groupBy(nearbyDevices, rawDeviceKey);
  const activeTrustedCounts = countBy(
    rawTrustedDevices,
    (trusted) => trusted?.trustState === "DEVICE_TRUST_STATE_PAIRED" ? rawDeviceKey(trusted) : ""
  );
  const consumedPresenceIDs = new Set();

  const devices = [];
  const seen = new Set();

  for (const rawTrusted of rawTrustedDevices) {
    const trusted = mapTrustedDevice(rawTrusted);
    if (!trusted || !trusted.id || seen.has(trusted.id)) {
      continue;
    }
    const key = rawDeviceKey(rawTrusted);
    const nearbyMatches = activeTrustedCounts.get(key) === 1 ? nearbyByKey.get(key) || [] : [];
    for (const nearby of nearbyMatches) {
      if (nearby.presenceId) {
        consumedPresenceIDs.add(nearby.presenceId);
      }
    }
    seen.add(trusted.id);
    devices.push(mergeNearbyTrustedDevice(trusted, nearbyMatches));
  }

  for (const nearby of nearbyDevices) {
    const id = nearby.presenceId || nearby.instance || nearby.name;
    if (!id || consumedPresenceIDs.has(nearby.presenceId) || seen.has(id)) {
      continue;
    }
    seen.add(id);
    devices.push(mapNearbyDevice(nearby, id));
  }

  return devices;
}

function mapLocalDevice(ping) {
  if (!ping) {
    return null;
  }
  return {
    name: ping.deviceName || "This Mac",
    id: "local",
    deviceID: ping.deviceId || "",
    connection: "active",
    role: roleLabel(ping.role),
    availability: "local",
    trustState: "paired",
    path: "local",
    platform: processPlatformHint(),
    arch: processArchHint(),
    address: "",
    updated: ""
  };
}

function mapTrustedDevice(trusted) {
  if (!trusted) {
    return null;
  }
  return {
    name: trusted.name || "Computer",
    id: trusted.deviceId || trusted.pairId || trusted.name || "",
    pairID: trusted.pairId || "",
    connection: trusted.trustState === "DEVICE_TRUST_STATE_PAIRED" ? connectionLabel(trusted) : "unpaired",
    role: roleLabel(trusted.role),
    availability: availabilityFromConnectivity(trusted.connectivityState),
    trustState: trustLabel(trusted.trustState),
    path: trusted.connectivityPath || "",
    connectionError: trusted.connectivityError || "",
    address: "",
    updated: timestampLabel(trusted.connectivityUpdatedAtUnixNano || trusted.updatedAtUnixNano)
  };
}

function mapPairings(pairings) {
  return (pairings || []).map(mapPairing).filter(Boolean);
}

function mapPairing(pairing) {
  if (!pairing) {
    return null;
  }
  return {
    id: pairing.id || "",
    peerDeviceID: pairing.peerDeviceId || "",
    peerName: pairing.peerName || "Computer",
    peerRole: roleLabel(pairing.peerRole),
    verificationCode: pairing.verificationCode || "",
    direction: pairing.direction === "PAIRING_DIRECTION_INBOUND" ? "inbound" : "outbound",
    state: pairingStateLabel(pairing.state),
    localConfirmed: Boolean(pairing.localConfirmed),
    remoteConfirmed: Boolean(pairing.remoteConfirmed),
    expiresAt: timestampLabel(pairing.expiresAtUnixNano),
    failure: pairing.failure || ""
  };
}

function mergeNearbyTrustedDevice(trusted, nearbyMatches) {
  const nearby = firstNearby(nearbyMatches);
  if (!nearby) {
    return trusted;
  }
  return {
    ...trusted,
    connection: "active",
    availability: "nearby",
    path: "lan",
    address: nearbyAddressList(nearbyMatches),
    platform: nearby.platform || trusted.platform || "",
    arch: nearby.arch || trusted.arch || "",
    updated: timestampLabel(nearby.lastSeenAtUnixNano) || trusted.updated
  };
}

function mapNearbyDevice(nearby, id) {
  return {
    name: nearby.name || "Computer",
    id,
    connection: nearby.trustState === "DEVICE_TRUST_STATE_PAIRED" ? "paired" : "not connected",
    role: roleLabel(nearby.role),
    availability: nearby.endpointReady ? "nearby" : "offline",
    trustState: trustLabel(nearby.trustState),
    path: "lan",
    platform: nearby.platform || "",
    arch: nearby.arch || "",
    address: nearbyAddress(nearby),
    updated: timestampLabel(nearby.lastSeenAtUnixNano)
  };
}

function firstNearby(values) {
  if (!Array.isArray(values) || values.length === 0) {
    return null;
  }
  return [...values].sort((left, right) => Number(right.lastSeenAtUnixNano || 0) - Number(left.lastSeenAtUnixNano || 0))[0];
}

function nearbyAddressList(values) {
  if (!Array.isArray(values) || values.length === 0) {
    return "";
  }
  if (values.length > 1) {
    return `${values.length} LAN records`;
  }
  return nearbyAddress(values[0]);
}

function nearbyAddress(nearby = {}) {
  const host = (nearby.addresses || [])[0] || nearby.hostName || "";
  if (!host) {
    return "";
  }
  if (!nearby.endpointReady || !nearby.port) {
    return host;
  }
  if (host.includes(":") && !host.startsWith("[")) {
    return `[${host}]:${nearby.port}`;
  }
  return `${host}:${nearby.port}`;
}

function rawDeviceKey(value = {}) {
  const name = value.name || "";
  const role = value.role || "";
  if (!name || !role) {
    return "";
  }
  return `${name}\u0000${role}`;
}

function groupBy(values, keyFor) {
  const groups = new Map();
  for (const value of values || []) {
    const key = keyFor(value);
    if (!key) {
      continue;
    }
    const group = groups.get(key) || [];
    group.push(value);
    groups.set(key, group);
  }
  return groups;
}

function countBy(values, keyFor) {
  const counts = new Map();
  for (const value of values || []) {
    const key = keyFor(value);
    if (!key) {
      continue;
    }
    counts.set(key, (counts.get(key) || 0) + 1);
  }
  return counts;
}

function roleLabel(role) {
  if (role === "DEVICE_ROLE_WORKER") {
    return "worker";
  }
  if (role === "DEVICE_ROLE_ORCHESTRATOR") {
    return "orchestrator";
  }
  return "device";
}

function connectionLabel(device) {
  return device.connectivityState === "CONNECTIVITY_STATE_CONNECTED" ? "active" : "not connected";
}

function trustLabel(state) {
  if (state === "DEVICE_TRUST_STATE_PAIRED") {
    return "paired";
  }
  if (state === "DEVICE_TRUST_STATE_REVOKED") {
    return "revoked";
  }
  return "unpaired";
}

function pairingStateLabel(state) {
  switch (state) {
    case "PAIRING_STATE_WAITING":
      return "waiting";
    case "PAIRING_STATE_PAIRED":
      return "paired";
    case "PAIRING_STATE_REJECTED":
      return "rejected";
    case "PAIRING_STATE_EXPIRED":
      return "expired";
    case "PAIRING_STATE_FAILED":
      return "failed";
    default:
      return "unknown";
  }
}

function availabilityFromConnectivity(state) {
  switch (state) {
    case "CONNECTIVITY_STATE_CONNECTED":
      return "remote";
    case "CONNECTIVITY_STATE_CONNECTING":
      return "connecting";
    default:
      return "offline";
  }
}

function timestampLabel(value) {
  if (!value) {
    return "";
  }
  const numeric = Number(value);
  if (!Number.isFinite(numeric) || numeric <= 0) {
    return "";
  }
  return new Date(Math.floor(numeric / 1_000_000)).toISOString();
}

function processPlatformHint() {
  if (typeof process === "undefined") {
    return "";
  }
  return process.platform || "";
}

function processArchHint() {
  if (typeof process === "undefined") {
    return "";
  }
  return process.arch || "";
}

module.exports = {
  mapDevices,
  mapLocalDevice,
  mapPairing,
  mapPairings,
  mapTrustedDevice,
  timestampLabel
};
