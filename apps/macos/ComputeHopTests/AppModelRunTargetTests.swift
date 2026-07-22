import ComputeHopProtocol
import Foundation
import Testing

@testable import ComputeHopApp

@Test
@MainActor
func runDisabledReasonExplainsDaemonOfflineState() {
    let model = AppModel(client: RecordingDaemonClient())
    model.commandInput = "hostname"

    #expect(!model.canSubmitCommand)
    #expect(model.runDisabledReason == "Start ComputeHop before running jobs.")
    #expect(model.runHelpText == "Start ComputeHop before running jobs.")
}

@Test
@MainActor
func runDisabledReasonExplainsEmptyCommand() {
    let model = AppModel(client: RecordingDaemonClient())
    model.daemon = daemonSummary()

    #expect(!model.canSubmitCommand)
    #expect(model.runDisabledReason == "Enter a command to run.")
}

@Test
@MainActor
func runDisabledReasonExplainsInvalidCommandInput() {
    let model = AppModel(client: RecordingDaemonClient())
    model.daemon = daemonSummary()
    model.commandInput = "echo \"unfinished"

    #expect(!model.canSubmitCommand)
    #expect(model.runDisabledReason == "The command has an unclosed double quote.")
    #expect(model.runCommandCopyValue == nil)
}

@Test
@MainActor
func runDisabledReasonExplainsMissingRemoteProjectFolder() {
    let worker = runTargetDevice(id: "worker-id", name: "Gaming PC")
    let model = AppModel(client: RecordingDaemonClient(devices: [worker]))
    model.daemon = daemonSummary()
    model.devices = [worker]
    model.runTargetID = AppModel.automaticWorkerTargetID
    model.commandInput = "cargo build"

    #expect(!model.canSubmitCommand)
    #expect(model.runDisabledReason == "Choose a project folder to upload, or enable Skip project upload for a utility command.")
}

@Test
@MainActor
func submitCommandRefusesMissingRemoteProjectFolder() async {
    let worker = runTargetDevice(id: "worker-id", name: "Gaming PC")
    let client = RecordingDaemonClient(devices: [worker])
    let model = AppModel(client: client)
    model.daemon = daemonSummary()
    model.devices = [worker]
    model.runTargetID = AppModel.automaticWorkerTargetID
    model.commandInput = "cargo build"

    await model.submitCommand()

    #expect(await client.lastSubmittedExecutable() == nil)
    #expect(model.lastError == "Choose a project folder to upload, or enable Skip project upload for a utility command.")
}

@Test
@MainActor
func runDisabledReasonAllowsRemoteUtilityWithoutProjectFolder() {
    let worker = runTargetDevice(id: "worker-id", name: "Gaming PC")
    let model = AppModel(client: RecordingDaemonClient(devices: [worker]))
    model.daemon = daemonSummary()
    model.devices = [worker]
    model.runTargetID = AppModel.automaticWorkerTargetID
    model.remoteRunWithoutProject = true
    model.commandInput = "hostname"

    #expect(model.canSubmitCommand)
    #expect(model.runDisabledReason == nil)
    #expect(model.runHelpText == "Run this command on the selected target.")
}

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

@Test
@MainActor
func submitRemoteCommandCanSkipProjectUpload() async {
    let worker = runTargetDevice(id: "worker-id", name: "Gaming PC")
    let client = RecordingDaemonClient(devices: [worker])
    let model = AppModel(client: client)
    model.daemon = daemonSummary()
    model.devices = [worker]
    model.runTargetID = AppModel.automaticWorkerTargetID
    model.remoteRunWithoutProject = true
    model.commandInput = "hostname"
    model.workingDirectory = "/Users/austin/project"
    model.outputsInput = "result.txt"

    await model.submitCommand()

    #expect(await client.lastSubmittedSelector() == "auto")
    #expect(await client.lastSubmittedWorkingDirectory() == "")
    #expect(await client.lastSubmittedOutputs() == [])
    #expect(model.jobs.first?.target == "Auto worker")
    #expect(model.lastError == nil)
}

@Test
@MainActor
func runCommandCopyValueFormatsLocalCommand() {
    let model = AppModel(client: RecordingDaemonClient())
    model.daemon = daemonSummary()
    model.commandInput = "echo 'hello world'"
    model.workingDirectory = "/Users/austin/My Project"
    model.outputsInput = "dist/app, report.json"

    #expect(model.runCommandCopyValue == "computehop run -C '/Users/austin/My Project' -o dist/app -o report.json echo 'hello world'")
}

@Test
@MainActor
func runCommandCopyValueFormatsRemoteUtilityCommand() {
    let worker = runTargetDevice(id: "worker-id", name: "Gaming PC")
    let model = AppModel(client: RecordingDaemonClient(devices: [worker]))
    model.daemon = daemonSummary()
    model.devices = [worker]
    model.runTargetID = AppModel.automaticWorkerTargetID
    model.remoteRunWithoutProject = true
    model.commandInput = "hostname"
    model.workingDirectory = "/Users/austin/project"
    model.outputsInput = "ignored.txt"

    #expect(model.runCommandCopyValue == "computehop run --on auto --no-project hostname")
}

@Test
@MainActor
func runCommandCopyValueUsesSeparatorWhenProgramArgumentsLookLikeFlags() {
    let worker = runTargetDevice(id: "worker-id", name: "Gaming PC")
    let model = AppModel(client: RecordingDaemonClient(devices: [worker]))
    model.daemon = daemonSummary()
    model.devices = [worker]
    model.runTargetID = worker.id
    model.commandInput = "cargo build --release"
    model.workingDirectory = "/Users/austin/project"

    #expect(model.runCommandCopyValue == "computehop run --on 'Gaming PC' -C /Users/austin/project -- cargo build --release")
}

@Test
@MainActor
func copyRunCommandCopiesCurrentCLICommand() {
    let model = AppModel(client: RecordingDaemonClient())
    let clipboard = RecordingClipboard()
    model.daemon = daemonSummary()
    model.commandInput = "hostname"

    model.copyRunCommand(to: clipboard)

    #expect(clipboard.value == "computehop run hostname")
}

@Test
@MainActor
func refreshNotifiesWhenObservedJobFinishes() async {
    let jobID = "7a338fa3-7ba4-4c54-bf59-da1161f6b76f"
    let client = RecordingDaemonClient(
        jobs: [
            jobSummary(
                id: jobID,
                executable: "cargo",
                arguments: ["build"],
                target: "Gaming PC",
                state: .running
            )
        ]
    )
    let notifier = RecordingJobCompletionNotifier()
    let model = AppModel(client: client, notifier: notifier)

    await model.refresh()

    #expect(notifier.notifications.isEmpty)

    await client.setJobs([
        jobSummary(
            id: jobID,
            executable: "cargo",
            arguments: ["build"],
            target: "Gaming PC",
            state: .succeeded
        )
    ])
    await model.refresh()

    #expect(notifier.notifications == [
        NotificationRecord(
            title: "ComputeHop job succeeded",
            body: "cargo build on Gaming PC · 7a338fa3"
        )
    ])
}

@Test
@MainActor
func refreshDoesNotNotifyForExistingTerminalHistory() async {
    let client = RecordingDaemonClient(
        jobs: [
            jobSummary(
                id: "7a338fa3-7ba4-4c54-bf59-da1161f6b76f",
                target: "This Mac",
                state: .succeeded
            )
        ]
    )
    let notifier = RecordingJobCompletionNotifier()
    let model = AppModel(client: client, notifier: notifier)

    await model.refresh()

    #expect(notifier.notifications.isEmpty)
}

@Test
@MainActor
func refreshDoesNotNotifyWhenJobNotificationsAreDisabled() async {
    let jobID = "7a338fa3-7ba4-4c54-bf59-da1161f6b76f"
    let client = RecordingDaemonClient(
        jobs: [
            jobSummary(id: jobID, target: "Gaming PC", state: .running)
        ]
    )
    let notifier = RecordingJobCompletionNotifier()
    let model = AppModel(
        client: client,
        notifier: notifier,
        settingsStore: RecordingSettingsStore(jobCompletionNotificationsEnabled: false)
    )

    await model.refresh()
    await client.setJobs([
        jobSummary(id: jobID, target: "Gaming PC", state: .succeeded)
    ])
    await model.refresh()

    #expect(notifier.notifications.isEmpty)
}

@Test
@MainActor
func notificationSettingLoadsAndPersistsThroughStore() {
    let store = RecordingSettingsStore(jobCompletionNotificationsEnabled: false)
    let model = AppModel(
        client: RecordingDaemonClient(),
        notifier: RecordingJobCompletionNotifier(),
        settingsStore: store
    )

    #expect(!model.jobCompletionNotificationsEnabled)

    model.jobCompletionNotificationsEnabled = true

    #expect(store.savedNotificationValues == [true])
}

@Test
@MainActor
func submitLocalCommandIgnoresStaleNoProjectToggle() async {
    let client = RecordingDaemonClient(devices: [])
    let model = AppModel(client: client)
    model.daemon = daemonSummary()
    model.remoteRunWithoutProject = true
    model.commandInput = "hostname"
    model.outputsInput = "result.txt"

    #expect(!model.isNoProjectRemoteRunSelected)

    await model.submitCommand()

    #expect(await client.lastSubmittedSelector() == "")
    #expect(await client.lastSubmittedWorkingDirectory() == FileManager.default.homeDirectoryForCurrentUser.path)
    #expect(await client.lastSubmittedOutputs() == ["result.txt"])
    #expect(model.lastError == nil)
}

@Test
@MainActor
func submitSmokeTestUsesAutoWorkerWithoutProjectSnapshot() async {
    let worker = runTargetDevice(id: "worker-id", name: "Gaming PC")
    let client = RecordingDaemonClient(devices: [worker])
    let model = AppModel(client: client)
    model.daemon = daemonSummary()
    model.devices = [worker]

    #expect(model.canSubmitSmokeTest)
    #expect(model.smokeTestDisabledReason == nil)
    #expect(model.smokeTestHelpText == "Run hostname on a worker without uploading a project.")

    await model.submitSmokeTest()

    #expect(await client.lastSubmittedExecutable() == "hostname")
    #expect(await client.lastSubmittedArguments() == [])
    #expect(await client.lastSubmittedSelector() == "auto")
    #expect(await client.lastSubmittedWorkingDirectory() == "")
    #expect(await client.lastSubmittedOutputs() == [])
    #expect(model.jobs.first?.target == "Auto worker")
    #expect(model.selectedJobID == model.jobs.first?.id)
    #expect(model.lastError == nil)
}

@Test
@MainActor
func emptyJobsHelpPointsAtSmokeTestWhenOneWorkerIsRunnable() {
    let worker = runTargetDevice(id: "worker-id", name: "Gaming PC")
    let model = AppModel(client: RecordingDaemonClient(devices: [worker]))
    model.daemon = daemonSummary()
    model.devices = [worker]

    #expect(model.emptyJobsHelpText == "Use Smoke Test to verify the worker, or run a command above.")
}

@Test
@MainActor
func emptyJobsHelpPointsAtConnectWhenNoWorkerIsRunnable() {
    let model = AppModel(client: RecordingDaemonClient(devices: []))
    model.daemon = daemonSummary()

    #expect(model.emptyJobsHelpText == "Run a command on This Mac, or connect a nearby worker to enable Smoke Test.")
}

@Test
@MainActor
func selectedJobLogsPlaceholderExplainsTerminalJobWithNoOutput() {
    let model = AppModel(client: RecordingDaemonClient())
    let job = jobSummary(
        id: "7a338fa3-7ba4-4c54-bf59-da1161f6b76f",
        target: "Gaming PC",
        state: .succeeded
    )
    model.jobs = [job]
    model.selectedJobID = job.id

    #expect(model.selectedJobLogsPlaceholder == "No stdout or stderr was captured for 7a338fa3. Some successful commands do not print output.")
}

@Test
@MainActor
func selectedJobLogsPlaceholderExplainsRunningJobWithNoOutputYet() {
    let model = AppModel(client: RecordingDaemonClient())
    let job = jobSummary(
        id: "7a338fa3-7ba4-4c54-bf59-da1161f6b76f",
        target: "Gaming PC",
        state: .running
    )
    model.jobs = [job]
    model.selectedJobID = job.id

    #expect(model.selectedJobLogsPlaceholder == "No output captured yet for 7a338fa3. The job may still be starting or may not have written stdout/stderr.")
}

@Test
@MainActor
func selectedJobLogsCommandFollowsSelectedJob() {
    let model = AppModel(client: RecordingDaemonClient())

    #expect(model.selectedJobLogsCommand == nil)

    model.selectedJobID = "7a338fa3-7ba4-4c54-bf59-da1161f6b76f"

    #expect(model.selectedJobLogsCommand == "computehop logs --follow 7a338fa3-7ba4-4c54-bf59-da1161f6b76f")
}

@Test
@MainActor
func submitSmokeTestUsesSelectedWorkerWhenMultipleWorkersExist() async {
    let first = runTargetDevice(id: "first-worker-id", name: "Gaming PC")
    let second = runTargetDevice(id: "second-worker-id", name: "Mini PC")
    let client = RecordingDaemonClient(devices: [first, second])
    let model = AppModel(client: client)
    model.daemon = daemonSummary()
    model.devices = [first, second]
    model.runTargetID = second.id

    #expect(model.canSubmitSmokeTest)

    await model.submitSmokeTest()

    #expect(await client.lastSubmittedExecutable() == "hostname")
    #expect(await client.lastSubmittedSelector() == second.id)
    #expect(await client.lastSubmittedWorkingDirectory() == "")
    #expect(model.jobs.first?.target == "Mini PC")
    #expect(model.lastError == nil)
}

@Test
@MainActor
func submitSmokeTestRefusesAmbiguousWorkersWithoutSelection() async {
    let first = runTargetDevice(id: "first-worker-id", name: "Gaming PC")
    let second = runTargetDevice(id: "second-worker-id", name: "Mini PC")
    let client = RecordingDaemonClient(devices: [first, second])
    let model = AppModel(client: client)
    model.daemon = daemonSummary()
    model.devices = [first, second]

    #expect(!model.canSubmitSmokeTest)
    #expect(model.smokeTestDisabledReason == "Auto worker works only when exactly one connected worker is available. Choose a worker from Run on.")

    await model.submitSmokeTest()

    #expect(await client.lastSubmittedExecutable() == nil)
    #expect(model.lastError == "Auto worker works only when exactly one connected worker is available. Choose a worker from Run on.")
}

@Test
@MainActor
func submitSmokeTestExplainsWhenNoWorkerIsConnected() async {
    let client = RecordingDaemonClient(devices: [])
    let model = AppModel(client: client)
    model.daemon = daemonSummary()
    model.devices = []

    #expect(!model.canSubmitSmokeTest)
    #expect(model.smokeTestDisabledReason == "No connected worker is available. Connect a nearby worker first, or run on This Mac.")

    await model.submitSmokeTest()

    #expect(await client.lastSubmittedExecutable() == nil)
    #expect(model.lastError == "No connected worker is available. Connect a nearby worker first, or run on This Mac.")
}

@Test
@MainActor
func submitCommandExplainsWhenSelectedWorkerDisappears() async {
    let client = RecordingDaemonClient(devices: [])
    let model = AppModel(client: client)
    model.daemon = daemonSummary()
    model.devices = []
    model.runTargetID = "missing-worker-id"
    model.commandInput = "hostname"

    #expect(model.smokeTestDisabledReason == "The selected worker is no longer reachable. Refresh and choose another run target.")

    await model.submitCommand()

    #expect(await client.lastSubmittedExecutable() == nil)
    #expect(model.lastError == "The selected worker is no longer reachable. Refresh and choose another run target.")
}

@Test
@MainActor
func connectUsesPresenceIDInsteadOfDisplayName() async {
    let nearby = DeviceSummary(
        id: "ephemeral-presence-id",
        name: "Gaming PC",
        role: "Worker",
        trust: "Unpaired",
        availability: .nearby,
        path: "LAN",
        address: "192.0.2.20:47823",
        canPair: true
    )
    let client = RecordingDaemonClient(devices: [nearby])
    let model = AppModel(client: client)
    model.devices = [nearby]

    await model.connect(nearby)

    #expect(await client.lastPairingSelector() == "ephemeral-presence-id")
    #expect(model.lastError == nil)
}

@Test
@MainActor
func connectNearbyWorkerUsesOnlyNearbyWorkerPresenceID() async {
    let nearby = pairableDevice(id: "ephemeral-presence-id", name: "Gaming PC")
    let client = RecordingDaemonClient(devices: [nearby])
    let model = AppModel(client: client)
    model.devices = [nearby]

    #expect(model.canConnectNearbyWorker)

    await model.connectNearbyWorker()

    #expect(await client.lastPairingSelector() == "ephemeral-presence-id")
    #expect(model.lastError == nil)
}

@Test
@MainActor
func connectNearbyWorkerRefusesAmbiguousNearbyWorkers() async {
    let first = pairableDevice(id: "first-presence-id", name: "Gaming PC")
    let second = pairableDevice(id: "second-presence-id", name: "Mini PC")
    let client = RecordingDaemonClient(devices: [first, second])
    let model = AppModel(client: client)
    model.devices = [first, second]

    #expect(!model.canConnectNearbyWorker)

    await model.connectNearbyWorker()

    #expect(await client.lastPairingSelector() == nil)
    #expect(model.lastError == "Connect Nearby Worker works only when exactly one nearby worker is available. Refresh and choose one from Devices.")
}

@Test
@MainActor
func disconnectUsesDurableDeviceIDAndClearsSelectedRunTarget() async {
    let worker = runTargetDevice(id: "durable-worker-id", name: "Gaming PC")
    let client = RecordingDaemonClient(devices: [])
    let model = AppModel(client: client)
    model.devices = [worker]
    model.runTargetID = worker.id
    model.remoteRunWithoutProject = true

    await model.disconnect(worker)

    #expect(await client.lastUnpairedSelector() == "durable-worker-id")
    #expect(model.runTargetID.isEmpty)
    #expect(!model.remoteRunWithoutProject)
    #expect(model.lastError == nil)
}

@Test
@MainActor
func copySetupGuideCommandCopiesCurrentCommand() {
    let model = AppModel(client: RecordingDaemonClient())
    model.daemon = daemonSummary()
    let clipboard = RecordingClipboard()

    model.copySetupGuideCommand(to: clipboard)

    #expect(clipboard.value == "computehop setup worker --device-name 'Gaming PC'")
}

@Test
@MainActor
func copySpecificSetupGuideCommandCopiesThatCommand() {
    let model = AppModel(client: RecordingDaemonClient())
    let clipboard = RecordingClipboard()

    model.copySetupGuideCommand(
        SetupGuideCommand(label: "LAN-only worker", value: "computehop setup worker --device-name 'Gaming PC' --lan-only"),
        to: clipboard
    )

    #expect(clipboard.value == "computehop setup worker --device-name 'Gaming PC' --lan-only")
}

@Test
@MainActor
func copySetupGuideCommandDoesNothingWhenGuideHasNoCommand() {
    let nearby = pairableDevice(id: "ephemeral-presence-id", name: "Gaming PC")
    let model = AppModel(client: RecordingDaemonClient())
    model.daemon = daemonSummary()
    model.devices = [nearby]
    let clipboard = RecordingClipboard()

    model.copySetupGuideCommand(to: clipboard)

    #expect(clipboard.value == nil)
}

@Test
@MainActor
func copySelectedJobLogsCommandCopiesFollowCommand() {
    let model = AppModel(client: RecordingDaemonClient())
    let clipboard = RecordingClipboard()
    model.selectedJobID = "7a338fa3-7ba4-4c54-bf59-da1161f6b76f"

    model.copySelectedJobLogsCommand(to: clipboard)

    #expect(clipboard.value == "computehop logs --follow 7a338fa3-7ba4-4c54-bf59-da1161f6b76f")
}

@Test
@MainActor
func copySelectedJobLogsCommandDoesNothingWithoutSelection() {
    let model = AppModel(client: RecordingDaemonClient())
    let clipboard = RecordingClipboard()

    model.copySelectedJobLogsCommand(to: clipboard)

    #expect(clipboard.value == nil)
}

private final class RecordingClipboard: ClipboardWriting {
    var value: String?

    func write(_ value: String) {
        self.value = value
    }
}

private struct NotificationRecord: Equatable {
    let title: String
    let body: String
}

@MainActor
private final class RecordingJobCompletionNotifier: JobCompletionNotifying {
    var notifications: [NotificationRecord] = []

    func notifyJobFinished(title: String, body: String) async {
        notifications.append(NotificationRecord(title: title, body: body))
    }
}

@MainActor
private final class RecordingSettingsStore: AppSettingsStoring {
    var jobCompletionNotificationsEnabled: Bool
    var savedNotificationValues: [Bool] = []

    init(jobCompletionNotificationsEnabled: Bool = true) {
        self.jobCompletionNotificationsEnabled = jobCompletionNotificationsEnabled
    }

    func setJobCompletionNotificationsEnabled(_ enabled: Bool) {
        jobCompletionNotificationsEnabled = enabled
        savedNotificationValues.append(enabled)
    }
}

private actor RecordingDaemonClient: LocalDaemonClientProtocol {
    private let devices: [DeviceSummary]
    private var jobs: [JobSummary]
    private var submittedExecutable: String?
    private var submittedArguments: [String]?
    private var submittedSelector: String?
    private var submittedWorkingDirectory: String?
    private var submittedOutputs: [String]?
    private var pairingSelector: String?
    private var unpairedSelector: String?
    private let submittedID = "7a338fa3-7ba4-4c54-bf59-da1161f6b76f"

    init(devices: [DeviceSummary] = [], jobs: [JobSummary] = []) {
        self.devices = devices
        self.jobs = jobs
    }

    func lastSubmittedExecutable() -> String? { submittedExecutable }

    func lastSubmittedArguments() -> [String]? { submittedArguments }

    func lastSubmittedSelector() -> String? { submittedSelector }

    func lastSubmittedWorkingDirectory() -> String? { submittedWorkingDirectory }

    func lastSubmittedOutputs() -> [String]? { submittedOutputs }

    func lastPairingSelector() -> String? { pairingSelector }

    func lastUnpairedSelector() -> String? { unpairedSelector }

    func setJobs(_ jobs: [JobSummary]) {
        self.jobs = jobs
    }

    func ping() async throws -> LocalDaemonSummary { daemonSummary() }

    func listDevices() async throws -> [DeviceSummary] { devices }

    func listJobs(limit: UInt32) async throws -> [JobSummary] {
        Array(jobs.prefix(Int(limit)))
    }

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
        submittedExecutable = executable
        submittedArguments = arguments
        submittedSelector = deviceSelector
        submittedWorkingDirectory = workingDirectory
        submittedOutputs = outputs
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
        pairingSelector = selector
        return PairingSummary(Computehop_Local_V1_Pairing())
    }

    func confirmPairing(id: String) async throws {}

    func rejectPairing(id: String) async throws {}

    func unpairDevice(id: String) async throws -> DeviceSummary {
        unpairedSelector = id
        return DeviceSummary(
            id: id,
            name: "Gaming PC",
            role: "Worker",
            trust: "Revoked",
            availability: .offline,
            path: nil,
            address: nil,
            canPair: false
        )
    }

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

private func pairableDevice(id: String, name: String) -> DeviceSummary {
    DeviceSummary(
        id: id,
        name: name,
        role: "Worker",
        trust: "Unpaired",
        availability: .nearby,
        path: "LAN",
        address: "192.0.2.20:47823",
        canPair: true
    )
}

private func jobSummary(
    id: String,
    executable: String = "hostname",
    arguments: [String] = [],
    outputs: [String] = [],
    target: String,
    state: Computehop_Local_V1_JobState = .queued
) -> JobSummary {
    var spec = Computehop_Local_V1_JobSpec()
    spec.executable = executable
    spec.arguments = arguments
    spec.executor = .native
    spec.outputs = outputs

    var value = Computehop_Local_V1_Job()
    value.id = id
    value.spec = spec
    value.state = state
    value.updatedAtUnixNano = 1_700_000_000_000_000_000
    return JobSummary(value, target: target)
}
