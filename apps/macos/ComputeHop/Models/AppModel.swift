import Foundation
import Observation

protocol ClipboardWriting {
    func write(_ value: String)
}

enum AppActionError: LocalizedError {
    case nearbyWorkerAmbiguous
    case noRunnableWorker
    case automaticWorkerAmbiguous
    case selectedWorkerUnavailable

    var errorDescription: String? {
        switch self {
        case .nearbyWorkerAmbiguous:
            return "Connect Nearby Worker works only when exactly one nearby worker is available. Refresh and choose one from Devices."
        case .noRunnableWorker:
            return "No connected worker is available. Connect a nearby worker first, or run on This Mac."
        case .automaticWorkerAmbiguous:
            return "Auto worker works only when exactly one connected worker is available. Choose a worker from Run on."
        case .selectedWorkerUnavailable:
            return "The selected worker is no longer reachable. Refresh and choose another run target."
        }
    }
}

@Observable
@MainActor
final class AppModel {
    static let automaticWorkerTargetID = "auto"
    static let localDeviceID = "local"
    private static let terminalJobStates: Set<String> = [
        "Succeeded",
        "Failed",
        "Cancelled",
        "Rejected",
        "Lost",
    ]

    private let client: LocalDaemonClientProtocol
    private let notifier: JobCompletionNotifying
    private let settingsStore: AppSettingsStoring
    private let planner: TaskPlanning
    private var trackedRemoteJobs: [String: String] = [:]
    private var observedJobStates: [String: String] = [:]
    private var nextLogSequence: UInt64 = 0
    private var deviceCapabilities: [String: Set<DeviceCapability>] = [:]

    var daemon: LocalDaemonSummary?
    var devices: [DeviceSummary] = []
    var jobs: [JobSummary] = []
    var pairings: [PairingSummary] = []
    var lastError: String?
    var isRefreshing = false
    var actionInProgress: String?
    var commandInput = ""
    var taskRequestInput = ""
    var workingDirectory = ""
    var outputsInput = ""
    var runTargetID = ""
    var selectedDeviceID = AppModel.localDeviceID
    var remoteRunWithoutProject = false
    var plannedTask: TaskPlan?
    var planningError: String?
    var selectedJobID: String?
    var selectedJobLogs = ""
    var selectedJobLogsTruncated = false
    var isLoadingLogs = false
    var artifactMessage: String?
    var jobCompletionNotificationsEnabled: Bool {
        didSet {
            settingsStore.setJobCompletionNotificationsEnabled(jobCompletionNotificationsEnabled)
        }
    }
    var workerSetupDeviceName: String {
        didSet {
            settingsStore.setWorkerSetupDeviceName(workerSetupDeviceName)
        }
    }
    var workerSetupCacheSize: String {
        didSet {
            settingsStore.setWorkerSetupCacheSize(workerSetupCacheSize)
        }
    }
    var vpsConnectivityDomain: String {
        didSet {
            settingsStore.setVPSConnectivityDomain(vpsConnectivityDomain)
        }
    }
    var vpsTurnDomain: String {
        didSet {
            settingsStore.setVPSTurnDomain(vpsTurnDomain)
        }
    }

    init(
        client: LocalDaemonClientProtocol = LocalDaemonClient(),
        notifier: JobCompletionNotifying = SystemJobCompletionNotifier(),
        settingsStore: AppSettingsStoring = UserDefaultsAppSettingsStore(),
        planner: TaskPlanning = LocalTaskPlanner()
    ) {
        self.client = client
        self.notifier = notifier
        self.settingsStore = settingsStore
        self.planner = planner
        jobCompletionNotificationsEnabled = settingsStore.jobCompletionNotificationsEnabled
        workerSetupDeviceName = settingsStore.workerSetupDeviceName
        workerSetupCacheSize = settingsStore.workerSetupCacheSize
        vpsConnectivityDomain = settingsStore.vpsConnectivityDomain
        vpsTurnDomain = settingsStore.vpsTurnDomain
        deviceCapabilities = settingsStore.deviceCapabilities
    }

    var daemonVersion: String? { daemon?.version }
    var isConnected: Bool { daemon != nil }

    var runnableDevices: [DeviceSummary] {
        devices.filter {
            $0.trust == "Paired" && [.nearby, .remote].contains($0.availability) && $0.role == "Worker"
        }
    }

    var pairableWorkers: [DeviceSummary] {
        devices.filter {
            $0.canPair && $0.availability == .nearby && $0.role == "Worker"
        }
    }

    var canRunAutomatically: Bool { runnableDevices.count == 1 }

    var canConnectNearbyWorker: Bool { pairableWorkers.count == 1 }

    var selectedDevice: DeviceSummary? {
        devices.first { $0.id == selectedDeviceID }
    }

    var selectedTargetName: String {
        if selectedDeviceID == Self.localDeviceID {
            return "Here"
        }
        return selectedDevice?.name ?? "No device selected"
    }

    var selectedDeviceCanRun: Bool {
        selectedDeviceID == Self.localDeviceID ||
            runnableDevices.contains(where: { $0.id == selectedDeviceID })
    }

    var selectedCapabilityID: String { selectedDeviceID }

    var selectedCapabilities: Set<DeviceCapability> {
        capabilities(forDeviceID: selectedCapabilityID)
    }

    var canPlanTask: Bool {
        !taskRequestInput.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty &&
            !workingDirectory.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    var canSubmitPlannedTask: Bool {
        plannedTask != nil && selectedDeviceCanRun && isConnected && actionInProgress == nil
    }

    var canSubmitSmokeTest: Bool { (try? smokeTestTarget()) != nil }

    var smokeTestDisabledReason: String? {
        guard isConnected else {
            return "Start ComputeHop before running a worker smoke test."
        }
        do {
            _ = try smokeTestTarget()
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    var smokeTestHelpText: String {
        smokeTestDisabledReason ?? "Run hostname on a worker without uploading a project."
    }

    var emptyJobsHelpText: String {
        guard isConnected else {
            return "Start ComputeHop to run jobs."
        }
        if runnableDevices.count == 1 {
            return "Use Smoke Test to verify the worker, or run a command above."
        }
        if runnableDevices.count > 1 {
            return "Choose a worker from Run on for Smoke Test, or run a command above."
        }
        return "Run a command on This Mac, or connect a nearby worker to enable Smoke Test."
    }

    var selectedJobLogsPlaceholder: String {
        guard let selectedJobID else {
            return "No output selected."
        }
        guard let job = jobs.first(where: { $0.id == selectedJobID }) else {
            return "No output loaded yet. Refresh jobs, then open logs again."
        }
        if job.terminal {
            return "No stdout or stderr was captured for \(job.shortID). Some successful commands do not print output."
        }
        return "No output captured yet for \(job.shortID). The job may still be starting or may not have written stdout/stderr."
    }

    var selectedJobLogsCommand: String? {
        guard let selectedJobID else { return nil }
        return "computehop logs --follow \(selectedJobID)"
    }

    var canSubmitCommand: Bool { runDisabledReason == nil }

    var runDisabledReason: String? {
        if !isConnected {
            return "Start ComputeHop before running jobs."
        }
        if actionInProgress != nil {
            return "Another ComputeHop action is already running."
        }
        if commandInput.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return "Enter a command to run."
        }
        do {
            _ = try CommandInput.parse(commandInput)
        } catch {
            return error.localizedDescription
        }
        if isAutomaticRunTargetSelected && !canRunAutomatically {
            return automaticWorkerSelectionError().localizedDescription
        }
        let selectedWorkerUnavailable = !runTargetID.isEmpty &&
            !isAutomaticRunTargetSelected &&
            !runnableDevices.contains(where: { $0.id == runTargetID })
        if selectedWorkerUnavailable {
            return AppActionError.selectedWorkerUnavailable.localizedDescription
        }
        let missingRemoteProjectFolder = isRemoteRunTargetSelected &&
            !isNoProjectRemoteRunSelected &&
            workingDirectory.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        if missingRemoteProjectFolder {
            return "Choose a project folder to upload, or enable Skip project upload for a utility command."
        }
        return nil
    }

    var runHelpText: String {
        runDisabledReason ?? "Run this command on the selected target."
    }

    var diagnosticsCommandBundle: String {
        [
            "computehop status",
            "computehop devices",
            "computehop connect",
            "computehop jobs --limit 10",
            "computehop doctor",
        ].joined(separator: "\n")
    }

    var runCommandCopyValue: String? {
        guard canSubmitCommand, let arguments = try? CommandInput.parse(commandInput) else {
            return nil
        }
        var command = ["computehop", "run"]
        if let selector = runTargetCLISelector() {
            command.append(contentsOf: ["--on", selector])
        }
        if isNoProjectRemoteRunSelected {
            command.append("--no-project")
        } else {
            let directory = workingDirectory.trimmingCharacters(in: .whitespacesAndNewlines)
            if !directory.isEmpty {
                command.append(contentsOf: ["-C", directory])
            }
            for output in declaredOutputs() {
                command.append(contentsOf: ["-o", output])
            }
        }
        if arguments.contains(where: { $0.hasPrefix("-") }) {
            command.append("--")
        }
        command.append(contentsOf: arguments)
        return CommandInput.shellCommand(command)
    }

    var isRemoteRunTargetSelected: Bool { !runTargetID.isEmpty }

    var isNoProjectRemoteRunSelected: Bool { isRemoteRunTargetSelected && remoteRunWithoutProject }

    var isAutomaticRunTargetSelected: Bool { runTargetID == Self.automaticWorkerTargetID }

    var setupGuide: SetupGuideSummary? {
        SetupGuideSummary.make(
            isConnected: isConnected,
            devices: devices,
            pairings: pairings,
            runnableDevices: runnableDevices,
            workerDeviceName: workerSetupDeviceName,
            workerCacheSize: workerSetupCacheSize,
            vpsConnectivityDomain: vpsConnectivityDomain,
            vpsTurnDomain: vpsTurnDomain
        )
    }

    func copySetupGuideCommand(to clipboard: ClipboardWriting) {
        guard let command = setupGuide?.command else { return }
        clipboard.write(command)
    }

    func copySetupGuideCommand(_ command: SetupGuideCommand, to clipboard: ClipboardWriting) {
        clipboard.write(command.value)
    }

    func copySelectedJobLogsCommand(to clipboard: ClipboardWriting) {
        guard let selectedJobLogsCommand else { return }
        clipboard.write(selectedJobLogsCommand)
    }

    func copyRunCommand(to clipboard: ClipboardWriting) {
        guard let runCommandCopyValue else { return }
        clipboard.write(runCommandCopyValue)
    }

    func copyDiagnosticsCommandBundle(to clipboard: ClipboardWriting) {
        clipboard.write(diagnosticsCommandBundle)
    }

    func refreshLoop() async {
        await refresh()
        while !Task.isCancelled {
            try? await Task.sleep(for: .seconds(3))
            guard !Task.isCancelled else { return }
            await refresh()
        }
    }

    func refresh() async {
        guard !isRefreshing else { return }
        isRefreshing = true
        defer { isRefreshing = false }
        do {
            async let daemon = client.ping()
            async let newDevices = client.listDevices()
            async let newJobs = client.listJobs(limit: 20)
            async let newPairings = client.listPairings()
            let snapshot = try await (daemon, newDevices, newJobs, newPairings)
            self.daemon = snapshot.0
            devices = snapshot.1
            var refreshedJobs = snapshot.2
            for (id, target) in trackedRemoteJobs where !refreshedJobs.contains(where: { $0.id == id }) {
                if let remoteJob = try? await client.getJob(id: id, target: target) {
                    refreshedJobs.append(remoteJob)
                }
            }
            jobs = refreshedJobs.sorted { $0.updatedAt > $1.updatedAt }
            await recordJobStateTransitions(jobs)
            pairings = snapshot.3
            if selectedDeviceID != Self.localDeviceID &&
                !devices.contains(where: { $0.id == selectedDeviceID })
            {
                selectedDeviceID = Self.localDeviceID
            }
            if isAutomaticRunTargetSelected && !canRunAutomatically {
                runTargetID = ""
                remoteRunWithoutProject = false
            } else if !isAutomaticRunTargetSelected && !runTargetID.isEmpty && !runnableDevices.contains(where: { $0.id == runTargetID }) {
                runTargetID = ""
                remoteRunWithoutProject = false
            }
            lastError = nil
            if selectedJobID != nil {
                await refreshSelectedLogs()
            }
        } catch {
            daemon = nil
            lastError = error.localizedDescription
        }
    }

    func connect(_ device: DeviceSummary) async {
        await perform("connect-\(device.id)") {
            _ = try await client.beginPairing(device: device.id)
        }
    }

    func connectNearbyWorker() async {
        await perform("connect-auto") {
            guard pairableWorkers.count == 1, let device = pairableWorkers.first else {
                throw AppActionError.nearbyWorkerAmbiguous
            }
            _ = try await client.beginPairing(device: device.id)
        }
    }

    func confirm(_ pairing: PairingSummary) async {
        await perform("confirm-\(pairing.id)") {
            try await client.confirmPairing(id: pairing.id)
        }
    }

    func reject(_ pairing: PairingSummary) async {
        await perform("reject-\(pairing.id)") {
            try await client.rejectPairing(id: pairing.id)
        }
    }

    func disconnect(_ device: DeviceSummary) async {
        await perform("disconnect-\(device.id)") {
            _ = try await client.unpairDevice(id: device.id)
            if runTargetID == device.id {
                runTargetID = ""
                remoteRunWithoutProject = false
            }
            if selectedDeviceID == device.id {
                selectedDeviceID = Self.localDeviceID
            }
        }
    }

    func selectLocalDevice() {
        selectedDeviceID = Self.localDeviceID
        runTargetID = ""
        remoteRunWithoutProject = false
        plannedTask = nil
        planningError = nil
    }

    func selectDevice(_ device: DeviceSummary) {
        selectedDeviceID = device.id
        runTargetID = device.id
        remoteRunWithoutProject = false
        plannedTask = nil
        planningError = nil
    }

    func capabilities(forDeviceID id: String) -> Set<DeviceCapability> {
        if let configured = deviceCapabilities[id] {
            return configured
        }
        return id == Self.localDeviceID ? DeviceCapability.defaultLocal : DeviceCapability.defaultWorker
    }

    func setCapability(_ capability: DeviceCapability, enabled: Bool, forDeviceID id: String) {
        var capabilities = capabilities(forDeviceID: id)
        if enabled {
            capabilities.insert(capability)
        } else {
            capabilities.remove(capability)
        }
        deviceCapabilities[id] = capabilities
        settingsStore.setDeviceCapabilities(capabilities, forDeviceID: id)
        plannedTask = nil
        planningError = nil
    }

    func planRequestedTask() {
        do {
            let plan = try planner.plan(
                request: taskRequestInput,
                projectPath: workingDirectory,
                capabilities: selectedCapabilities
            )
            plannedTask = plan
            planningError = nil
            commandInput = plan.commandLine
            outputsInput = plan.outputs.joined(separator: ", ")
        } catch {
            plannedTask = nil
            planningError = error.localizedDescription
        }
    }

    func submitPlannedTask() async {
        if plannedTask == nil {
            planRequestedTask()
        }
        guard let plan = plannedTask else { return }
        if !selectedDeviceCanRun {
            lastError = "\(selectedTargetName) is not available."
            return
        }
        commandInput = plan.commandLine
        outputsInput = plan.outputs.joined(separator: ", ")
        remoteRunWithoutProject = false
        await submitCommand()
        if lastError == nil {
            taskRequestInput = ""
            plannedTask = nil
            planningError = nil
        }
    }

    func cancel(_ job: JobSummary) async {
        await perform("cancel-\(job.id)") {
            try await client.cancelJob(id: job.id)
        }
    }

    func submitCommand() async {
        if let runDisabledReason {
            lastError = runDisabledReason
            return
        }
        await perform("submit-job") {
            let arguments = try CommandInput.parse(commandInput)
            let executable = arguments[0]
            let automaticTarget = isAutomaticRunTargetSelected
            let targetDevice = automaticTarget ? nil : runnableDevices.first { $0.id == runTargetID }
            if automaticTarget && !canRunAutomatically {
                throw automaticWorkerSelectionError()
            }
            if !runTargetID.isEmpty && !automaticTarget && targetDevice == nil {
                throw AppActionError.selectedWorkerUnavailable
            }
            let targetName = automaticTarget ? "Auto worker" : targetDevice?.name ?? "This Mac"
            let directory = workingDirectory.trimmingCharacters(in: .whitespacesAndNewlines)
            let noProjectRemoteRun = isNoProjectRemoteRunSelected
            let outputs = noProjectRemoteRun ? [] : declaredOutputs()
            let effectiveDirectory: String
            if noProjectRemoteRun {
                effectiveDirectory = ""
            } else if !isRemoteRunTargetSelected && directory.isEmpty {
                effectiveDirectory = FileManager.default.homeDirectoryForCurrentUser.path
            } else {
                effectiveDirectory = directory
            }
            let submitted = try await client.submitJob(
                executable: executable,
                arguments: Array(arguments.dropFirst()),
                workingDirectory: effectiveDirectory,
                outputs: outputs,
                deviceSelector: automaticTarget ? Self.automaticWorkerTargetID : targetDevice?.id ?? "",
                target: targetName
            )
            if automaticTarget || targetDevice != nil {
                trackedRemoteJobs[submitted.id] = targetName
            }
            jobs.removeAll { $0.id == submitted.id }
            jobs.insert(submitted, at: 0)
            observedJobStates[submitted.id] = submitted.state
            selectedJobID = submitted.id
            selectedJobLogs = ""
            selectedJobLogsTruncated = false
            nextLogSequence = 0
            commandInput = ""
            outputsInput = ""
        }
    }

    func submitSmokeTest() async {
        await perform("smoke-test") {
            let target = try smokeTestTarget()
            let submitted = try await client.submitJob(
                executable: "hostname",
                arguments: [],
                workingDirectory: "",
                outputs: [],
                deviceSelector: target.selector,
                target: target.name
            )
            trackedRemoteJobs[submitted.id] = target.name
            jobs.removeAll { $0.id == submitted.id }
            jobs.insert(submitted, at: 0)
            observedJobStates[submitted.id] = submitted.state
            selectedJobID = submitted.id
            selectedJobLogs = ""
            selectedJobLogsTruncated = false
            nextLogSequence = 0
        }
    }

    func fetchArtifacts(for job: JobSummary, destination: String) async {
        await perform("artifacts-\(job.id)") {
            let result = try await client.fetchArtifacts(
                id: job.id,
                destination: destination,
                deviceSelector: ""
            )
            if result.conflictFileCount == 0 {
                artifactMessage =
                    "Restored \(result.restoredFileCount) output file(s) to \(result.destination)."
            } else {
                artifactMessage =
                    "Restored outputs to \(result.destination). Kept existing files and saved \(result.conflictFileCount) conflict(s) under .computehop-conflicts."
            }
        }
    }

    func showLogs(for job: JobSummary) async {
        if selectedJobID != job.id {
            selectedJobID = job.id
            selectedJobLogs = ""
            selectedJobLogsTruncated = false
            nextLogSequence = 0
        }
        await refreshSelectedLogs()
    }

    func closeLogs() {
        selectedJobID = nil
        selectedJobLogs = ""
        selectedJobLogsTruncated = false
        nextLogSequence = 0
    }

    private func refreshSelectedLogs() async {
        guard let selectedJobID, !isLoadingLogs else { return }
        isLoadingLogs = true
        defer { isLoadingLogs = false }
        do {
            let target = jobs.first(where: { $0.id == selectedJobID })?.target ?? "This Mac"
            var pagesRead = 0
            var hasMore = true
            while hasMore && pagesRead < 8 {
                let page = try await client.readJobLogs(
                    id: selectedJobID,
                    afterSequence: nextLogSequence,
                    limit: 32,
                    target: target
                )
                if let jobIndex = jobs.firstIndex(where: { $0.id == selectedJobID }) {
                    let previousState = jobs[jobIndex].state
                    jobs[jobIndex] = page.job
                    await recordJobStateTransition(from: previousState, to: page.job)
                }
                for record in page.records {
                    selectedJobLogs.append(record.text)
                    nextLogSequence = max(nextLogSequence, record.sequence)
                }
                hasMore = page.hasMore
                pagesRead += 1
                trimLogsIfNeeded()
            }
        } catch {
            lastError = error.localizedDescription
        }
    }

    private func trimLogsIfNeeded() {
        let maximumCharacters = 128 * 1024
        guard selectedJobLogs.count > maximumCharacters else { return }
        selectedJobLogs = String(selectedJobLogs.suffix(maximumCharacters))
        selectedJobLogsTruncated = true
    }

    private func recordJobStateTransitions(_ refreshedJobs: [JobSummary]) async {
        for job in refreshedJobs {
            await recordJobStateTransition(from: observedJobStates[job.id], to: job)
        }
        let currentJobIDs = Set(refreshedJobs.map(\.id))
        observedJobStates = observedJobStates.filter { currentJobIDs.contains($0.key) }
    }

    private func recordJobStateTransition(from previousState: String?, to job: JobSummary) async {
        defer { observedJobStates[job.id] = job.state }
        guard let previousState, previousState != job.state else { return }
        guard job.terminal, !Self.terminalJobStates.contains(previousState) else { return }
        guard jobCompletionNotificationsEnabled else { return }

        await notifier.notifyJobFinished(
            title: "ComputeHop job \(job.state.lowercased())",
            body: "\(job.command) on \(job.target) · \(job.shortID)"
        )
    }

    private func smokeTestTarget() throws -> (selector: String, name: String) {
        if isAutomaticRunTargetSelected || runTargetID.isEmpty {
            guard canRunAutomatically else { throw automaticWorkerSelectionError() }
            return (Self.automaticWorkerTargetID, "Auto worker")
        }
        guard let device = runnableDevices.first(where: { $0.id == runTargetID }) else {
            throw AppActionError.selectedWorkerUnavailable
        }
        return (device.id, device.name)
    }

    private func automaticWorkerSelectionError() -> AppActionError {
        runnableDevices.isEmpty ? .noRunnableWorker : .automaticWorkerAmbiguous
    }

    private func runTargetCLISelector() -> String? {
        if isAutomaticRunTargetSelected {
            return Self.automaticWorkerTargetID
        }
        guard !runTargetID.isEmpty else { return nil }
        guard let device = runnableDevices.first(where: { $0.id == runTargetID }) else {
            return runTargetID
        }
        let matchingNameCount = runnableDevices.filter { $0.name == device.name }.count
        return matchingNameCount == 1 ? device.name : device.id
    }

    private func declaredOutputs() -> [String] {
        outputsInput.split(separator: ",", omittingEmptySubsequences: true)
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
    }

    private func perform(_ action: String, operation: () async throws -> Void) async {
        guard actionInProgress == nil else { return }
        actionInProgress = action
        defer { actionInProgress = nil }
        do {
            try await operation()
            await refresh()
        } catch {
            lastError = error.localizedDescription
        }
    }
}
