import ComputeHopProtocol
import Testing
@testable import ComputeHopApp

@Test
func setupGuideExplainsDaemonOfflineState() {
    let guide = SetupGuideSummary.make(
        isConnected: false,
        devices: [],
        pairings: [],
        runnableDevices: []
    )

    #expect(guide?.title == "Start ComputeHop")
    #expect(guide?.command == "computehop doctor")
}

@Test
func setupGuidePointsAtNearbyUnconnectedDevice() {
    let nearby = deviceSummary(
        name: "Gaming PC",
        trust: "Unpaired",
        availability: .nearby,
        canPair: true
    )

    let guide = SetupGuideSummary.make(
        isConnected: true,
        devices: [nearby],
        pairings: [],
        runnableDevices: []
    )

    #expect(guide?.title == "Connect Gaming PC")
    #expect(guide?.command == nil)
}

@Test
func setupGuideIsHiddenWhileConnectionRequestIsActive() {
    var pairing = Computehop_Local_V1_Pairing()
    pairing.id = "request-id"
    pairing.peerName = "Gaming PC"
    pairing.state = .waiting

    let guide = SetupGuideSummary.make(
        isConnected: true,
        devices: [],
        pairings: [PairingSummary(pairing)],
        runnableDevices: []
    )

    #expect(guide == nil)
}

@Test
func setupGuideExplainsOfflineTrustedWorker() {
    let worker = deviceSummary(
        name: "Gaming PC",
        trust: "Paired",
        availability: .offline,
        canPair: false
    )

    let guide = SetupGuideSummary.make(
        isConnected: true,
        devices: [worker],
        pairings: [],
        runnableDevices: []
    )

    #expect(guide?.title == "Worker offline")
    #expect(guide?.command == "computehop devices")
}

@Test
func setupGuideIsHiddenWhenWorkerCanRunJobs() {
    let worker = deviceSummary(
        name: "Gaming PC",
        trust: "Paired",
        availability: .nearby,
        canPair: false
    )

    let guide = SetupGuideSummary.make(
        isConnected: true,
        devices: [worker],
        pairings: [],
        runnableDevices: [worker]
    )

    #expect(guide == nil)
}

private func deviceSummary(
    name: String,
    trust: String,
    availability: DeviceSummary.Availability,
    canPair: Bool
) -> DeviceSummary {
    DeviceSummary(
        id: "\(name)-id",
        name: name,
        role: "Worker",
        trust: trust,
        availability: availability,
        path: nil,
        address: nil,
        canPair: canPair
    )
}
