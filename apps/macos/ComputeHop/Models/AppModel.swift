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

    private let client: LocalDaemonClientProtocol
    private var trackedRemoteJobs: [String: String] = [:]
    private var nextLogSequence: UInt64 = 0

    var daemon: LocalDaemonSummary?
    var devices: [DeviceSummary] = []
    var jobs: [JobSummary] = []
    var pairings: [PairingSummary] = []
    var lastError: String?
    var isRefreshing = false
    var actionInProgress: String?
    var commandInput = ""
    var workingDirectory = ""
    var outputsInput = ""
    var runTargetID = ""
    var selectedJobID: String?
    var selectedJobLogs = ""
    var selectedJobLogsTruncated = false
    var isLoadingLogs = false
    var artifactMessage: String?

    init(client: LocalDaemonClientProtocol = LocalDaemonClient()) {
        self.client = client
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

    var isRemoteRunTargetSelected: Bool { !runTargetID.isEmpty }

    var isAutomaticRunTargetSelected: Bool { runTargetID == Self.automaticWorkerTargetID }

    var setupGuide: SetupGuideSummary? {
        SetupGuideSummary.make(
            isConnected: isConnected,
            devices: devices,
            pairings: pairings,
            runnableDevices: runnableDevices
        )
    }

    func copySetupGuideCommand(to clipboard: ClipboardWriting) {
        guard let command = setupGuide?.command else { return }
        clipboard.write(command)
    }

    func copySetupGuideCommand(_ command: SetupGuideCommand, to clipboard: ClipboardWriting) {
        clipboard.write(command.value)
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
            pairings = snapshot.3
            if isAutomaticRunTargetSelected && !canRunAutomatically {
                runTargetID = ""
            } else if !isAutomaticRunTargetSelected && !runTargetID.isEmpty && !runnableDevices.contains(where: { $0.id == runTargetID }) {
                runTargetID = ""
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
            }
        }
    }

    func cancel(_ job: JobSummary) async {
        await perform("cancel-\(job.id)") {
            try await client.cancelJob(id: job.id)
        }
    }

    func submitCommand() async {
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
            let outputs = outputsInput.split(separator: ",", omittingEmptySubsequences: true)
                .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
                .filter { !$0.isEmpty }
            let effectiveDirectory = !isRemoteRunTargetSelected && directory.isEmpty
                ? FileManager.default.homeDirectoryForCurrentUser.path
                : directory
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
                    jobs[jobIndex] = page.job
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
