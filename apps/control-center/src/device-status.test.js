const assert = require("node:assert/strict");
const test = require("node:test");
const {
  availabilityLabel,
  connectionPathLabel,
  deviceLabel,
  friendlyConnectionError
} = require("./device-status");

test("availabilityLabel does not mark offline trusted workers as nearby", () => {
  assert.equal(
    availabilityLabel({
      id: "worker-1",
      role: "worker",
      trustState: "paired",
      connection: "not connected",
      availability: "offline"
    }),
    "Offline"
  );

  assert.equal(
    availabilityLabel({
      id: "presence-1",
      role: "worker",
      trustState: "unpaired",
      connection: "not connected",
      availability: "nearby"
    }),
    "Nearby"
  );
});

test("deviceLabel describes connected paths without exposing route jargon", () => {
  assert.equal(
    deviceLabel({
      id: "worker-1",
      name: "Gaming PC",
      role: "worker",
      trustState: "paired",
      connection: "active",
      availability: "remote",
      path: "lan"
    }),
    "Computer · connected over LAN"
  );

  assert.equal(
    deviceLabel({
      id: "worker-2",
      name: "Home Server",
      role: "worker",
      trustState: "paired",
      connection: "active",
      availability: "remote",
      path: "turn-relay"
    }),
    "Server · connected over relay"
  );
});

test("friendlyConnectionError summarizes actionable offline reasons", () => {
  assert.equal(connectionPathLabel("ice-direct"), "direct link");
  assert.equal(friendlyConnectionError("re-pair this device to enable remote connectivity"), "needs reconnect setup");
  assert.equal(friendlyConnectionError("remote connectivity is disabled"), "remote access off");
  assert.equal(friendlyConnectionError("dial timeout"), "connection timed out");
});
