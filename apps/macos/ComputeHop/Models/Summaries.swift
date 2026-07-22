import Foundation
import ComputeHopProtocol

struct LocalDaemonSummary: Sendable {
    let version: String
    let deviceID: String?
    let deviceName: String?
    let role: String?

    init(_ value: Computehop_Local_V1_PingResponse) {
        version = value.daemonVersion
        deviceID = value.deviceID.isEmpty ? nil : value.deviceID
        deviceName = value.deviceName.isEmpty ? nil : value.deviceName
        let label = roleLabel(value.role)
        role = label == "Unknown" ? nil : label
    }

    var daemonText: String {
        version.isEmpty ? "Daemon running" : "Daemon \(version)"
    }

    var shortID: String? {
        guard let deviceID else { return nil }
        return String(deviceID.prefix(8))
    }

    var identityText: String? {
        guard let deviceName else { return nil }
        return ([deviceName, role, shortID] as [String?])
            .compactMap { $0 }
            .joined(separator: " · ")
    }
}

struct DeviceSummary: Identifiable, Sendable {
    enum Availability: String, Sendable {
        case nearby = "Nearby"
        case remote = "Remote"
        case connecting = "Connecting"
        case offline = "Offline"
    }

    let id: String
    let name: String
    let role: String
    let trust: String
    let availability: Availability
    let path: String?
    let address: String?
    let canPair: Bool
    let canDisconnect: Bool

    var shortID: String { String(id.prefix(8)) }
    var trustDisplay: String {
        switch trust {
        case "Paired": return "Connected"
        case "Unpaired": return "Not connected"
        default: return trust
        }
    }

    static func make(from response: Computehop_Local_V1_ListDevicesResponse) -> [DeviceSummary] {
        let activeCounts = Dictionary(grouping: response.trustedDevices.filter {
            $0.trustState == .paired
        }, by: deviceKey).mapValues(\.count)
        let nearbyByKey = Dictionary(grouping: response.devices, by: deviceKey)
        var consumedPresenceIDs = Set<String>()
        var result = response.trustedDevices.map { trusted in
            let key = deviceKey(trusted)
            let matches = nearbyByKey[key] ?? []
            let nearbyMatches = trusted.trustState == .paired && activeCounts[key] == 1
                ? matches
                : []
            for nearby in nearbyMatches {
                consumedPresenceIDs.insert(nearby.presenceID)
            }
            let remote = remoteAvailability(trusted)
            return DeviceSummary(
                id: trusted.deviceID,
                name: trusted.name,
                role: roleLabel(trusted.role),
                trust: trusted.trustState == .paired ? "Paired" : "Revoked",
                availability: nearbyMatches.isEmpty ? remote.availability : .nearby,
                path: nearbyMatches.isEmpty ? remote.path : "LAN",
                address: address(nearbyMatches),
                canPair: false,
                canDisconnect: trusted.trustState == .paired
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
                path: "LAN",
                address: address(nearby),
                canPair: true,
                canDisconnect: false
            )
        })
        return result.sorted {
            if $0.trust != $1.trust { return $0.trust == "Paired" }
            return $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending
        }
    }

    init(_ value: Computehop_Local_V1_TrustedDevice) {
        let remote = Self.remoteAvailability(value)
        id = value.deviceID
        name = value.name
        role = roleLabel(value.role)
        trust = value.trustState == .paired ? "Paired" : "Revoked"
        availability = remote.availability
        path = remote.path
        address = nil
        canPair = false
        canDisconnect = value.trustState == .paired
    }

    init(
        id: String,
        name: String,
        role: String,
        trust: String,
        availability: Availability,
        path: String?,
        address: String?,
        canPair: Bool,
        canDisconnect: Bool = false
    ) {
        self.id = id
        self.name = name
        self.role = role
        self.trust = trust
        self.availability = availability
        self.path = path
        self.address = address
        self.canPair = canPair
        self.canDisconnect = canDisconnect
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

    private static func address(_ values: [Computehop_Local_V1_NearbyDevice]) -> String? {
        switch values.count {
        case 0:
            return nil
        case 1:
            return address(values[0])
        default:
            return "\(values.count) LAN records"
        }
    }

    private static func remoteAvailability(
        _ value: Computehop_Local_V1_TrustedDevice
    ) -> (availability: Availability, path: String?) {
        switch value.connectivityState {
        case .connected:
            return (.remote, remotePathLabel(value.connectivityPath))
        case .connecting:
            return (.connecting, "Internet")
        case .disabled:
            return (.offline, "LAN only")
        default:
            return (.offline, nil)
        }
    }

    private static func remotePathLabel(_ kind: String) -> String {
        switch kind {
        case "host": return "Direct"
        case "server-reflexive": return "Direct via STUN"
        case "relay": return "Relay via TURN"
        default: return "Internet"
        }
    }
}

struct SetupGuideSummary: Sendable {
    let title: String
    let detail: String
    let commands: [SetupGuideCommand]

    var command: String? { commands.first?.value }

    init(title: String, detail: String, command: String? = nil) {
        self.title = title
        self.detail = detail
        if let command {
            commands = [SetupGuideCommand(label: "Command", value: command)]
        } else {
            commands = []
        }
    }

    init(title: String, detail: String, commands: [SetupGuideCommand]) {
        self.title = title
        self.detail = detail
        self.commands = commands
    }

    static func make(
        isConnected: Bool,
        devices: [DeviceSummary],
        pairings: [PairingSummary],
        runnableDevices: [DeviceSummary],
        workerDeviceName: String = "Gaming PC",
        workerCacheSize: String = ""
    ) -> SetupGuideSummary? {
        if !isConnected {
            return SetupGuideSummary(
                title: "Start ComputeHop",
                detail: "The menu bar cannot reach the background daemon yet. Start it, then refresh.",
                command: "computehop doctor"
            )
        }
        if pairings.contains(where: { $0.state == "Waiting" }) {
            return nil
        }
        let nearbyWorkers = devices.filter {
            $0.canPair && $0.availability == .nearby && $0.role == "Worker"
        }
        if nearbyWorkers.count == 1 {
            return SetupGuideSummary(
                title: "Connect Nearby Worker",
                detail: "Use Connect Nearby Worker, compare the code on both devices, then confirm.",
                command: nil
            )
        }
        if let nearby = nearbyWorkers.first {
            return SetupGuideSummary(
                title: "Choose a worker to connect",
                detail: "Click Connect beside \(nearby.name), compare the code on both devices, then confirm.",
                command: nil
            )
        }
        if !runnableDevices.isEmpty {
            return nil
        }
        if devices.contains(where: {
            $0.role == "Worker" && $0.trust == "Paired" && $0.availability == .offline
        }) {
            let remoteDisabled = devices.contains {
                $0.role == "Worker" && $0.trust == "Paired" && $0.availability == .offline && $0.path == "LAN only"
            }
            let offlineWorkerName = devices.first {
                $0.role == "Worker" && $0.trust == "Paired" && $0.availability == .offline
            }?.name ?? "Gaming PC"
            let commands = remoteDisabled
                ? [
                    SetupGuideCommand(label: "Check devices", value: "computehop devices"),
                    SetupGuideCommand(
                        label: "VPS worker setup",
                        value: workerSetupCommand(
                            deviceName: offlineWorkerName,
                            cacheSize: workerCacheSize,
                            vpsTemplate: true
                        )
                    ),
                ]
                : [SetupGuideCommand(label: "Check devices", value: "computehop devices")]
            return SetupGuideSummary(
                title: "Worker offline",
                detail: remoteDisabled
                    ? "Remote connectivity is disabled. Put both devices on the same LAN, or reinstall without --lan-only after the VPS is ready."
                    : "A trusted worker exists but is not reachable. Start ComputeHop on that computer or put both devices on the same LAN.",
                commands: commands
            )
        }
        return SetupGuideSummary(
            title: "Add a worker",
            detail: "Install ComputeHop as a worker on another computer on this LAN. It will appear here automatically.",
            commands: [
                SetupGuideCommand(
                    label: "Worker install",
                    value: workerSetupCommand(deviceName: workerDeviceName, cacheSize: workerCacheSize)
                ),
                SetupGuideCommand(
                    label: "LAN-only worker",
                    value: workerSetupCommand(
                        deviceName: workerDeviceName,
                        cacheSize: workerCacheSize,
                        lanOnly: true
                    )
                ),
                SetupGuideCommand(
                    label: "VPS worker template",
                    value: workerSetupCommand(
                        deviceName: workerDeviceName,
                        cacheSize: workerCacheSize,
                        vpsTemplate: true
                    )
                ),
            ]
        )
    }

    private static func workerSetupCommand(
        deviceName: String,
        cacheSize: String = "",
        lanOnly: Bool = false,
        vpsTemplate: Bool = false
    ) -> String {
        let trimmedDeviceName = deviceName.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedCacheSize = cacheSize.trimmingCharacters(in: .whitespacesAndNewlines)
        var parts = [
            "computehop",
            "setup",
            "worker",
            "--device-name",
            shellArgument(trimmedDeviceName.isEmpty ? "Gaming PC" : trimmedDeviceName),
        ]
        if !trimmedCacheSize.isEmpty {
            parts.append(contentsOf: ["--cache-size", shellArgument(trimmedCacheSize)])
        }
        if lanOnly {
            parts.append("--lan-only")
        }
        if vpsTemplate {
            parts.append(contentsOf: [
                "--connectivity-domain",
                "connect.example.com",
                "--turn-domain",
                "turn.example.com",
            ])
        }
        return parts.joined(separator: " ")
    }

    private static func shellArgument(_ value: String) -> String {
        guard !value.isEmpty else { return "''" }
        let safeCharacters = CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:@%+=,")
        if value.unicodeScalars.allSatisfy({ safeCharacters.contains($0) }) {
            return value
        }
        return "'" + value.replacingOccurrences(of: "'", with: "'\"'\"'") + "'"
    }
}

struct SetupGuideCommand: Sendable, Identifiable {
    let label: String
    let value: String

    var id: String { label + "\u{0}" + value }
}

struct JobSummary: Identifiable, Sendable {
    let id: String
    let command: String
    let state: String
    let terminal: Bool
    let updatedAt: Date
    let target: String
    let outputs: [String]
    let progressText: String?

    init(_ value: Computehop_Local_V1_Job, target: String = "This Mac") {
        id = value.id
        command = ([value.spec.executable] + value.spec.arguments).joined(separator: " ")
        state = jobStateLabel(value.state)
        terminal = [.succeeded, .failed, .cancelled, .rejected, .lost].contains(value.state)
        updatedAt = Date(timeIntervalSince1970: Double(value.updatedAtUnixNano) / 1_000_000_000)
        self.target = target
        outputs = value.spec.outputs
        progressText = value.hasProgress ? jobProgressLabel(value.progress) : nil
    }

    var shortID: String { String(id.prefix(8)) }
    var canFetchOutputs: Bool { state == "Succeeded" && !outputs.isEmpty }
}

struct ArtifactRestoreSummary: Sendable {
    let destination: String
    let restoredFileCount: UInt32
    let conflictFileCount: UInt32
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
    let localConfirmed: Bool
    let remoteConfirmed: Bool
    let needsLocalConfirmation: Bool

    init(_ value: Computehop_Local_V1_Pairing) {
        id = value.id
        peerName = value.peerName
        verificationCode = value.verificationCode
        state = pairingStateLabel(value.state)
        localConfirmed = value.localConfirmed
        remoteConfirmed = value.remoteConfirmed
        needsLocalConfirmation = value.state == .waiting && !localConfirmed
    }

    var confirmationStatusText: String {
        "This device: \(confirmationLabel(localConfirmed)) · Other device: \(confirmationLabel(remoteConfirmed))"
    }

    var instructionText: String {
        if needsLocalConfirmation && remoteConfirmed {
            return "The other device already confirmed. Click Codes Match here only if this exact code matches."
        }
        if needsLocalConfirmation {
            return "Confirm only if this exact code appears on both devices."
        }
        if state == "Waiting" {
            return "This device is confirmed. Finish on the other device to complete the connection."
        }
        return "Connection state: \(state)"
    }
}

private func confirmationLabel(_ confirmed: Bool) -> String {
    confirmed ? "confirmed" : "not yet"
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

private func jobProgressLabel(_ progress: Computehop_Local_V1_JobProgress) -> String {
    let percent = progress.totalBytes > 0
        ? Int(progress.completedBytes * 100 / progress.totalBytes)
        : 0
    return "\(jobProgressPhaseLabel(progress.phase)) \(percent)% (\(byteCount(progress.completedBytes))/\(byteCount(progress.totalBytes)))"
}

private func jobProgressPhaseLabel(_ phase: Computehop_Local_V1_JobProgressPhase) -> String {
    switch phase {
    case .snapshot: return "Snapshot"
    case .upload: return "Upload"
    case .download: return "Download"
    case .restore: return "Restore"
    case .collect: return "Collect"
    default: return "Progress"
    }
}

private func byteCount(_ value: Int64) -> String {
    let units: [(String, Int64)] = [
        ("GiB", 1 << 30),
        ("MiB", 1 << 20),
        ("KiB", 1 << 10),
    ]
    for (suffix, bytes) in units where value >= bytes {
        let whole = value / bytes
        let tenth = value % bytes * 10 / bytes
        return tenth == 0 ? "\(whole)\(suffix)" : "\(whole).\(tenth)\(suffix)"
    }
    return "\(value)B"
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
