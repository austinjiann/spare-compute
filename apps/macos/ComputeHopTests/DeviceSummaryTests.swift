import ComputeHopProtocol
import Testing
@testable import ComputeHopApp

@Test
func localDaemonSummaryDescribesCurrentMacIdentity() {
    var response = Computehop_Local_V1_PingResponse()
    response.daemonVersion = "dev"
    response.deviceID = "abcdefghijklmnopqrstuvwxyz"
    response.deviceName = "Austin MacBook"
    response.role = .orchestrator

    let summary = LocalDaemonSummary(response)
    #expect(summary.version == "dev")
    #expect(summary.daemonText == "Daemon dev")
    #expect(summary.shortID == "abcdefgh")
    #expect(summary.identityText == "Austin MacBook · Orchestrator · abcdefgh")
}

@Test
func localDaemonSummaryHandlesVersionOnlyPing() {
    var response = Computehop_Local_V1_PingResponse()
    response.daemonVersion = "dev"

    let summary = LocalDaemonSummary(response)
    #expect(summary.version == "dev")
    #expect(summary.identityText == nil)
}

@Test
func deviceSummaryCombinesOneTrustedPeerWithItsNearbyPresence() {
    var trusted = Computehop_Local_V1_TrustedDevice()
    trusted.deviceID = "durable-device-id"
    trusted.name = "Gaming PC"
    trusted.role = .worker
    trusted.trustState = .paired

    var nearby = Computehop_Local_V1_NearbyDevice()
    nearby.presenceID = "ephemeral-presence-id"
    nearby.name = "Gaming PC"
    nearby.role = .worker
    nearby.endpointReady = true
    nearby.addresses = ["192.0.2.20"]
    nearby.port = 47_823

    var response = Computehop_Local_V1_ListDevicesResponse()
    response.trustedDevices = [trusted]
    response.devices = [nearby]

    let summaries = DeviceSummary.make(from: response)
    #expect(summaries.count == 1)
    #expect(summaries[0].id == "durable-device-id")
    #expect(summaries[0].availability == .nearby)
    #expect(summaries[0].path == "LAN")
    #expect(summaries[0].address == "192.0.2.20:47823")
    #expect(summaries[0].trustDisplay == "Connected")
    #expect(!summaries[0].canPair)
}

@Test
func deviceSummaryMakesConnectedRemoteWorkerRunnable() {
    var trusted = Computehop_Local_V1_TrustedDevice()
    trusted.deviceID = "remote-device-id"
    trusted.name = "Remote PC"
    trusted.role = .worker
    trusted.trustState = .paired
    trusted.connectivityState = .connected
    trusted.connectivityPath = "server-reflexive"

    var response = Computehop_Local_V1_ListDevicesResponse()
    response.trustedDevices = [trusted]

    let summaries = DeviceSummary.make(from: response)
    #expect(summaries.count == 1)
    #expect(summaries[0].availability == .remote)
    #expect(summaries[0].path == "Direct via STUN")
    #expect(summaries[0].address == nil)
}

@Test
func deviceSummaryUsesPresenceIDForUnpairedNearbyDevices() {
    var nearby = Computehop_Local_V1_NearbyDevice()
    nearby.presenceID = "ephemeral-presence-id"
    nearby.name = "Gaming PC"
    nearby.role = .worker
    nearby.endpointReady = true
    nearby.addresses = ["192.0.2.20"]
    nearby.port = 47_823

    var response = Computehop_Local_V1_ListDevicesResponse()
    response.devices = [nearby]

    let summaries = DeviceSummary.make(from: response)
    #expect(summaries.count == 1)
    #expect(summaries[0].id == "ephemeral-presence-id")
    #expect(summaries[0].name == "Gaming PC")
    #expect(summaries[0].canPair)
}
