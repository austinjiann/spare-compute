const assert = require("node:assert/strict");
const test = require("node:test");
const {
  deviceSelectorFromDeviceID,
  followupDeviceSelector,
  jobDeviceIDForSelector,
  rememberedRemoteDeviceID
} = require("./job-routing");

test("deviceSelectorFromDeviceID maps local and remembered job operations to daemon placement routing", () => {
  assert.equal(deviceSelectorFromDeviceID("local"), "");
  assert.equal(deviceSelectorFromDeviceID(rememberedRemoteDeviceID), "");
});

test("deviceSelectorFromDeviceID preserves explicit and automatic selectors for selection-level operations", () => {
  assert.equal(deviceSelectorFromDeviceID("auto"), "auto");
  assert.equal(deviceSelectorFromDeviceID("5wc2jkni"), "5wc2jkni");
});

test("jobDeviceIDForSelector stores auto jobs as remembered remote jobs", () => {
  assert.equal(jobDeviceIDForSelector(""), "local");
  assert.equal(jobDeviceIDForSelector("local"), "local");
  assert.equal(jobDeviceIDForSelector("auto"), rememberedRemoteDeviceID);
  assert.equal(jobDeviceIDForSelector(rememberedRemoteDeviceID), rememberedRemoteDeviceID);
  assert.equal(jobDeviceIDForSelector("5wc2jkni"), "5wc2jkni");
});

test("followupDeviceSelector uses remembered placement after auto submission", () => {
  assert.equal(followupDeviceSelector("auto"), "");
  assert.equal(followupDeviceSelector("5wc2jkni"), "5wc2jkni");
  assert.equal(followupDeviceSelector(" local "), "local");
});
