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

    #expect(guide?.title == "Connect Nearby Worker")
    #expect(guide?.command == nil)
}

@Test
func setupGuidePointsAtManualChoiceForMultipleNearbyWorkers() {
    let first = deviceSummary(
        name: "Gaming PC",
        trust: "Unpaired",
        availability: .nearby,
        canPair: true
    )
    let second = deviceSummary(
        name: "Mini PC",
        trust: "Unpaired",
        availability: .nearby,
        canPair: true
    )

    let guide = SetupGuideSummary.make(
        isConnected: true,
        devices: [first, second],
        pairings: [],
        runnableDevices: []
    )

    #expect(guide?.title == "Choose a worker to connect")
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
func setupGuideExplainsLANOnlyOfflineTrustedWorker() {
    let worker = DeviceSummary(
        id: "lan-only-worker",
        name: "Gaming PC",
        role: "Worker",
        trust: "Paired",
        availability: .offline,
        path: "LAN only",
        address: nil,
        canPair: false
    )

    let guide = SetupGuideSummary.make(
        isConnected: true,
        devices: [worker],
        pairings: [],
        runnableDevices: []
    )

    #expect(guide?.title == "Worker offline")
    #expect(guide?.detail == "Remote connectivity is disabled. Put both devices on the same LAN, or reinstall without --lan-only after the VPS is ready.")
    #expect(guide?.command == "computehop devices")
    #expect(guide?.commands.map(\.label) == ["Check devices", "VPS worker setup"])
    #expect(guide?.commands.map(\.value) == [
        "computehop devices",
        "computehop setup worker --device-name 'Gaming PC' --connectivity-domain connect.example.com --turn-domain turn.example.com",
    ])
}

@Test
func setupGuidePointsAtWorkerSetupWhenNoWorkerExists() {
    let guide = SetupGuideSummary.make(
        isConnected: true,
        devices: [],
        pairings: [],
        runnableDevices: []
    )

    #expect(guide?.title == "Add a worker")
    #expect(guide?.command == "computehop setup worker --device-name 'Gaming PC'")
    #expect(guide?.commands.map(\.label) == ["Worker install", "LAN-only worker", "VPS worker template"])
    #expect(guide?.commands.map(\.value) == [
        "computehop setup worker --device-name 'Gaming PC'",
        "computehop setup worker --device-name 'Gaming PC' --lan-only",
        "computehop setup worker --device-name 'Gaming PC' --connectivity-domain connect.example.com --turn-domain turn.example.com",
    ])
}

@Test
func setupGuideUsesConfiguredWorkerSetupDefaults() {
    let guide = SetupGuideSummary.make(
        isConnected: true,
        devices: [],
        pairings: [],
        runnableDevices: [],
        workerDeviceName: "Studio Mini",
        workerCacheSize: "80GiB"
    )

    #expect(guide?.command == "computehop setup worker --device-name 'Studio Mini' --cache-size 80GiB")
    #expect(guide?.commands.map(\.value) == [
        "computehop setup worker --device-name 'Studio Mini' --cache-size 80GiB",
        "computehop setup worker --device-name 'Studio Mini' --cache-size 80GiB --lan-only",
        "computehop setup worker --device-name 'Studio Mini' --cache-size 80GiB --connectivity-domain connect.example.com --turn-domain turn.example.com",
    ])
}

@Test
func setupGuideShellQuotesWorkerNamesInCommands() {
    let worker = DeviceSummary(
        id: "quoted-worker",
        name: "Austin's Gaming PC",
        role: "Worker",
        trust: "Paired",
        availability: .offline,
        path: "LAN only",
        address: nil,
        canPair: false
    )

    let guide = SetupGuideSummary.make(
        isConnected: true,
        devices: [worker],
        pairings: [],
        runnableDevices: []
    )

    #expect(guide?.commands.map(\.value).contains(
        "computehop setup worker --device-name 'Austin'\"'\"'s Gaming PC' --connectivity-domain connect.example.com --turn-domain turn.example.com"
    ) == true)
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
