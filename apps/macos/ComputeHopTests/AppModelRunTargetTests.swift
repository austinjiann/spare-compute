import ComputeHopProtocol
import Foundation
import Testing

@testable import ComputeHopApp

@Test
@MainActor
func appModelOffersAutomaticWorkerForExactlyOneRunnableDevice() {
    let model = AppModel(client: RecordingDaemonClient())
    model.devices = [
        runTargetDevice(id: "worker-id", name: "Gaming PC")
    ]

    #expect(model.canRunAutomatically)
    model.runTargetID = AppModel.automaticWorkerTargetID
    #expect(model.isAutomaticRunTargetSelected)
    #expect(model.isRemoteRunTargetSelected)
}

@Test
@MainActor
func submitCommandUsesAutomaticWorkerSelector() async {
    let worker = runTargetDevice(id: "worker-id", name: "Gaming PC")
    let client = RecordingDaemonClient(devices: [worker])
    let model = AppModel(client: client)
    model.daemon = daemonSummary()
    model.devices = [worker]
    model.runTargetID = AppModel.automaticWorkerTargetID
    model.commandInput = "hostname"
    model.workingDirectory = "/Users/austin/project"

    await model.submitCommand()

    #expect(await client.lastSubmittedSelector() == "auto")
    #expect(await client.lastSubmittedWorkingDirectory() == "/Users/austin/project")
    #expect(model.jobs.first?.target == "Auto worker")
    #expect(model.lastError == nil)
}

private actor RecordingDaemonClient: LocalDaemonClientProtocol {
    private let devices: [DeviceSummary]
    private var submittedSelector: String?
    private var submittedWorkingDirectory: String?
    private let submittedID = "7a338fa3-7ba4-4c54-bf59-da1161f6b76f"

    init(devices: [DeviceSummary] = []) {
        self.devices = devices
    }

    func lastSubmittedSelector() -> String? { submittedSelector }

    func lastSubmittedWorkingDirectory() -> String? { submittedWorkingDirectory }

    func ping() async throws -> LocalDaemonSummary { daemonSummary() }

    func listDevices() async throws -> [DeviceSummary] { devices }

    func listJobs(limit: UInt32) async throws -> [JobSummary] { [] }

    func getJob(id: String, target: String) async throws -> JobSummary {
        jobSummary(id: id, target: target)
    }

    func submitJob(
        executable: String,
        arguments: [String],
        workingDirectory: String,
        outputs: [String],
        deviceSelector: String,
        target: String
    ) async throws -> JobSummary {
        submittedSelector = deviceSelector
        submittedWorkingDirectory = workingDirectory
        return jobSummary(
            id: submittedID,
            executable: executable,
            arguments: arguments,
            outputs: outputs,
            target: target
        )
    }

    func fetchArtifacts(
        id: String,
        destination: String,
        deviceSelector: String
    ) async throws -> ArtifactRestoreSummary {
        ArtifactRestoreSummary(destination: destination, restoredFileCount: 0, conflictFileCount: 0)
    }

    func readJobLogs(
        id: String,
        afterSequence: UInt64,
        limit: UInt32,
        target: String
    ) async throws -> JobLogPage {
        JobLogPage(job: jobSummary(id: id, target: target), records: [], hasMore: false)
    }

    func listPairings() async throws -> [PairingSummary] { [] }

    func beginPairing(device selector: String) async throws -> PairingSummary {
        PairingSummary(Computehop_Local_V1_Pairing())
    }

    func confirmPairing(id: String) async throws {}

    func rejectPairing(id: String) async throws {}

    func cancelJob(id: String) async throws {}
}

private func daemonSummary() -> LocalDaemonSummary {
    var response = Computehop_Local_V1_PingResponse()
    response.daemonVersion = "dev"
    response.deviceID = "orchestrator-device-id"
    response.deviceName = "Austin MacBook"
    response.role = .orchestrator
    return LocalDaemonSummary(response)
}

private func runTargetDevice(id: String, name: String) -> DeviceSummary {
    DeviceSummary(
        id: id,
        name: name,
        role: "Worker",
        trust: "Paired",
        availability: .nearby,
        path: "LAN",
        address: "192.0.2.20:47823",
        canPair: false
    )
}

private func jobSummary(
    id: String,
    executable: String = "hostname",
    arguments: [String] = [],
    outputs: [String] = [],
    target: String
) -> JobSummary {
    var spec = Computehop_Local_V1_JobSpec()
    spec.executable = executable
    spec.arguments = arguments
    spec.executor = .native
    spec.outputs = outputs

    var value = Computehop_Local_V1_Job()
    value.id = id
    value.spec = spec
    value.state = .queued
    value.updatedAtUnixNano = 1_700_000_000_000_000_000
    return JobSummary(value, target: target)
}
