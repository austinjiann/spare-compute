import Foundation
import ComputeHopProtocol

struct DeviceSummary: Identifiable, Sendable {
    enum Availability: String, Sendable {
        case nearby = "Nearby"
        case offline = "Offline"
    }

    let id: String
    let name: String
    let role: String
    let trust: String
    let availability: Availability
    let address: String?
    let canPair: Bool

    var shortID: String { String(id.prefix(8)) }

    static func make(from response: Computehop_Local_V1_ListDevicesResponse) -> [DeviceSummary] {
        let activeCounts = Dictionary(grouping: response.trustedDevices.filter {
            $0.trustState == .paired
        }, by: deviceKey).mapValues(\.count)
        let nearbyByKey = Dictionary(grouping: response.devices, by: deviceKey)
        var consumedPresenceIDs = Set<String>()
        var result = response.trustedDevices.map { trusted in
            let key = deviceKey(trusted)
            let matches = nearbyByKey[key] ?? []
            let nearby = trusted.trustState == .paired && activeCounts[key] == 1 && matches.count == 1
                ? matches[0]
                : nil
            if let nearby {
                consumedPresenceIDs.insert(nearby.presenceID)
            }
            return DeviceSummary(
                id: trusted.deviceID,
                name: trusted.name,
                role: roleLabel(trusted.role),
                trust: trusted.trustState == .paired ? "Paired" : "Revoked",
                availability: nearby == nil ? .offline : .nearby,
                address: nearby.flatMap(address),
                canPair: false
            )
        }
        result.append(contentsOf: response.devices.compactMap { nearby in
            guard !consumedPresenceIDs.contains(nearby.presenceID) else { return nil }
            return DeviceSummary(
                id: nearby.presenceID,
                name: nearby.name,
                role: roleLabel(nearby.role),
                trust: "Unpaired",
                availability: .nearby,
                address: address(nearby),
                canPair: true
            )
        })
        return result.sorted {
            if $0.trust != $1.trust { return $0.trust == "Paired" }
            return $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending
        }
    }

    private static func deviceKey(_ value: Computehop_Local_V1_TrustedDevice) -> String {
        "\(value.name)\u{0}\(value.role.rawValue)"
    }

    private static func deviceKey(_ value: Computehop_Local_V1_NearbyDevice) -> String {
        "\(value.name)\u{0}\(value.role.rawValue)"
    }

    private static func address(_ value: Computehop_Local_V1_NearbyDevice) -> String? {
        let host = value.addresses.first ?? value.hostName
        guard !host.isEmpty else { return nil }
        guard value.endpointReady, value.port > 0 else { return host }
        if host.contains(":") {
            return "[\(host)]:\(value.port)"
        }
        return "\(host):\(value.port)"
    }
}

struct JobSummary: Identifiable, Sendable {
    let id: String
    let command: String
    let state: String
    let terminal: Bool
    let updatedAt: Date
    let target: String

    init(_ value: Computehop_Local_V1_Job, target: String = "This Mac") {
        id = value.id
        command = ([value.spec.executable] + value.spec.arguments).joined(separator: " ")
        state = jobStateLabel(value.state)
        terminal = [.succeeded, .failed, .cancelled, .rejected, .lost].contains(value.state)
        updatedAt = Date(timeIntervalSince1970: Double(value.updatedAtUnixNano) / 1_000_000_000)
        self.target = target
    }

    var shortID: String { String(id.prefix(8)) }
}

struct JobLogRecordSummary: Sendable {
    let sequence: UInt64
    let stream: String
    let text: String

    init(_ value: Computehop_Local_V1_JobLogRecord) {
        sequence = value.sequence
        stream = value.stream == .stderr ? "stderr" : "stdout"
        text = String(decoding: value.data, as: UTF8.self)
    }
}

struct JobLogPage: Sendable {
    let job: JobSummary
    let records: [JobLogRecordSummary]
    let hasMore: Bool
}

struct PairingSummary: Identifiable, Sendable {
    let id: String
    let peerName: String
    let verificationCode: String
    let state: String
    let needsLocalConfirmation: Bool

    init(_ value: Computehop_Local_V1_Pairing) {
        id = value.id
        peerName = value.peerName
        verificationCode = value.verificationCode
        state = pairingStateLabel(value.state)
        needsLocalConfirmation = value.state == .waiting && !value.localConfirmed
    }
}

private func roleLabel(_ role: Computehop_Local_V1_DeviceRole) -> String {
    switch role {
    case .worker: return "Worker"
    case .orchestrator: return "Orchestrator"
    default: return "Unknown"
    }
}

private func jobStateLabel(_ state: Computehop_Local_V1_JobState) -> String {
    switch state {
    case .created: return "Created"
    case .validating: return "Validating"
    case .queued: return "Queued"
    case .snapshotting: return "Snapshotting"
    case .transferring: return "Transferring"
    case .starting: return "Starting"
    case .running: return "Running"
    case .collecting: return "Collecting"
    case .restoring: return "Restoring"
    case .succeeded: return "Succeeded"
    case .failed: return "Failed"
    case .cancelled: return "Cancelled"
    case .rejected: return "Rejected"
    case .lost: return "Lost"
    default: return "Unknown"
    }
}

private func pairingStateLabel(_ state: Computehop_Local_V1_PairingState) -> String {
    switch state {
    case .waiting: return "Waiting"
    case .paired: return "Paired"
    case .rejected: return "Rejected"
    case .expired: return "Expired"
    case .failed: return "Failed"
    default: return "Unknown"
    }
}
