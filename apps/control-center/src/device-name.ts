function normalizedDeviceName(value, fallback = "This Mac") {
  const trimmed = String(value || "").replace(/\.local$/i, "").trim();
  return trimmed || fallback;
}

module.exports = {
  normalizedDeviceName
};
