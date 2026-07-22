import Foundation
import Observation

@Observable
@MainActor
final class AppModel {
    private let client: LocalDaemonClient

    var daemonVersion: String?
    var devices: [DeviceSummary] = []
    var jobs: [JobSummary] = []
    var pairings: [PairingSummary] = []
    var lastError: String?
    var isRefreshing = false
    var actionInProgress: String?

    init(client: LocalDaemonClient = LocalDaemonClient()) {
        self.client = client
    }

    var isConnected: Bool { daemonVersion != nil }

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
            async let version = client.ping()
            async let newDevices = client.listDevices()
            async let newJobs = client.listJobs()
            async let newPairings = client.listPairings()
            let snapshot = try await (version, newDevices, newJobs, newPairings)
            daemonVersion = snapshot.0
            devices = snapshot.1
            jobs = snapshot.2
            pairings = snapshot.3
            lastError = nil
        } catch {
            daemonVersion = nil
            lastError = error.localizedDescription
        }
    }

    func pair(_ device: DeviceSummary) async {
        await perform("pair-\(device.id)") {
            _ = try await client.beginPairing(device: device.name)
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

    func cancel(_ job: JobSummary) async {
        await perform("cancel-\(job.id)") {
            try await client.cancelJob(id: job.id)
        }
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
