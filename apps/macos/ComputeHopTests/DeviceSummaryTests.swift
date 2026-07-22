import ComputeHopProtocol
import Testing
@testable import ComputeHopApp

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
    #expect(summaries[0].address == "192.0.2.20:47823")
    #expect(!summaries[0].canPair)
}
