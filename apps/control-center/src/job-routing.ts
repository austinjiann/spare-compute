const rememberedRemoteDeviceID = "__computehop_remembered_remote__";

function deviceSelectorFromDeviceID(deviceID) {
  const value = String(deviceID || "").trim();
  if (!value || value === "local" || value === rememberedRemoteDeviceID) {
    return "";
  }
  return value;
}

function jobDeviceIDForSelector(selector) {
  const value = String(selector || "").trim();
  if (!value || value === "local") {
    return "local";
  }
  if (value === "auto" || value === rememberedRemoteDeviceID) {
    return rememberedRemoteDeviceID;
  }
  return value;
}

function followupDeviceSelector(selector) {
  return String(selector || "").trim() === "auto" ? "" : String(selector || "").trim();
}

module.exports = {
  deviceSelectorFromDeviceID,
  followupDeviceSelector,
  jobDeviceIDForSelector,
  rememberedRemoteDeviceID
};
