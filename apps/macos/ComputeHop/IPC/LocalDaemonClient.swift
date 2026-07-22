import Foundation
import Network
import ComputeHopProtocol
import SwiftProtobuf

enum LocalDaemonError: LocalizedError, Sendable {
    case notRunning
    case invalidCapabilityToken(String)
    case invalidFrame(String)
    case invalidResponse(String)
    case remote(String)
    case transport(String)

    var errorDescription: String? {
        switch self {
        case .notRunning:
            return "ComputeHop is not running. Start the daemon and try again."
        case .invalidCapabilityToken(let detail):
            return "The local daemon credential is invalid: \(detail)"
        case .invalidFrame(let detail):
            return "The local daemon sent an invalid frame: \(detail)"
        case .invalidResponse(let detail):
            return "The local daemon sent an invalid response: \(detail)"
        case .remote(let message):
            return message
        case .transport(let detail):
            return "Could not reach the local ComputeHop daemon: \(detail)"
        }
    }
}

enum LocalIPCFrame {
    static let maximumPayloadBytes = 1 << 20

    static func encode(_ payload: Data) throws -> Data {
        guard !payload.isEmpty else {
            throw LocalDaemonError.invalidFrame("empty payload")
        }
        guard payload.count <= maximumPayloadBytes else {
            throw LocalDaemonError.invalidFrame("payload exceeds 1 MiB")
        }
        let length = UInt32(payload.count)
        var frame = Data([
            UInt8((length >> 24) & 0xff),
            UInt8((length >> 16) & 0xff),
            UInt8((length >> 8) & 0xff),
            UInt8(length & 0xff),
        ])
        frame.append(payload)
        return frame
    }

    static func payloadLength(from header: Data) throws -> Int {
        guard header.count == 4 else {
            throw LocalDaemonError.invalidFrame("length header must be four bytes")
        }
        let length = header.reduce(UInt32(0)) { ($0 << 8) | UInt32($1) }
        guard length > 0 else {
            throw LocalDaemonError.invalidFrame("empty payload")
        }
        guard length <= maximumPayloadBytes else {
            throw LocalDaemonError.invalidFrame("payload exceeds 1 MiB")
        }
        return Int(length)
    }
}

private final class ContinuationGate<Value: Sendable>: @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<Value, Error>?

    init(_ continuation: CheckedContinuation<Value, Error>) {
        self.continuation = continuation
    }

    func succeed(_ value: Value) {
        take()?.resume(returning: value)
    }

    func fail(_ error: Error) {
        take()?.resume(throwing: error)
    }

    private func take() -> CheckedContinuation<Value, Error>? {
        lock.lock()
        defer { lock.unlock() }
        let current = continuation
        continuation = nil
        return current
    }
}

actor LocalDaemonClient {
    static let protocolVersion: UInt32 = 4

    private let socketURL: URL
    private let tokenURL: URL
    private let queue = DispatchQueue(label: "com.computehop.local-ipc")

    init(stateDirectory: URL = LocalDaemonClient.defaultStateDirectory()) {
        socketURL = stateDirectory.appendingPathComponent("computehop.sock")
        tokenURL = stateDirectory.appendingPathComponent("local-ipc.token")
    }

    func ping() async throws -> String {
        let response = try await call(.ping(Computehop_Local_V1_PingRequest()))
        guard case .ping(let ping)? = response.result else {
            throw LocalDaemonError.invalidResponse("missing ping result")
        }
        return ping.daemonVersion
    }

    func listDevices() async throws -> [DeviceSummary] {
        let response = try await call(.listDevices(Computehop_Local_V1_ListDevicesRequest()))
        guard case .listDevices(let devices)? = response.result else {
            throw LocalDaemonError.invalidResponse("missing device list")
        }
        return DeviceSummary.make(from: devices)
    }

    func listJobs(limit: UInt32 = 20) async throws -> [JobSummary] {
        var operation = Computehop_Local_V1_ListJobsRequest()
        operation.limit = limit
        let response = try await call(.listJobs(operation))
        guard case .listJobs(let jobs)? = response.result else {
            throw LocalDaemonError.invalidResponse("missing job list")
        }
        return jobs.jobs.map { JobSummary($0) }
    }

    func getJob(id: String, target: String = "This Mac") async throws -> JobSummary {
        var operation = Computehop_Local_V1_GetJobRequest()
        operation.jobID = id
        let response = try await call(.getJob(operation))
        guard case .getJob(let result)? = response.result, result.hasJob else {
            throw LocalDaemonError.invalidResponse("missing job")
        }
        return JobSummary(result.job, target: target)
    }

    func submitJob(
        executable: String,
        arguments: [String],
        workingDirectory: String,
        outputs: [String] = [],
        deviceSelector: String,
        target: String
    ) async throws -> JobSummary {
        var spec = Computehop_Local_V1_JobSpec()
        spec.executable = executable
        spec.arguments = arguments
        spec.workingDirectory = workingDirectory
        spec.executor = .native
        spec.outputs = outputs

        var operation = Computehop_Local_V1_SubmitJobRequest()
        operation.spec = spec
        operation.deviceSelector = deviceSelector
        let response = try await call(.submitJob(operation))
        guard case .submitJob(let result)? = response.result, result.hasJob else {
            throw LocalDaemonError.invalidResponse("missing submitted job")
        }
        return JobSummary(result.job, target: target)
    }

    func fetchArtifacts(
        id: String,
        destination: String,
        deviceSelector: String = ""
    ) async throws -> ArtifactRestoreSummary {
        var operation = Computehop_Local_V1_FetchArtifactsRequest()
        operation.jobID = id
        operation.destination = destination
        operation.deviceSelector = deviceSelector
        let response = try await call(.fetchArtifacts(operation))
        guard case .fetchArtifacts(let result)? = response.result,
            !result.destination.isEmpty
        else {
            throw LocalDaemonError.invalidResponse("missing artifact result")
        }
        return ArtifactRestoreSummary(
            destination: result.destination,
            restoredFileCount: result.restoredFileCount,
            conflictFileCount: result.conflictFileCount
        )
    }

    func readJobLogs(
        id: String,
        afterSequence: UInt64,
        limit: UInt32 = 32,
        target: String = "This Mac"
    ) async throws -> JobLogPage {
        var operation = Computehop_Local_V1_ReadJobLogsRequest()
        operation.jobID = id
        operation.afterSequence = afterSequence
        operation.limit = limit
        let response = try await call(.readJobLogs(operation))
        guard case .readJobLogs(let result)? = response.result, result.hasJob else {
            throw LocalDaemonError.invalidResponse("missing job logs")
        }
        return JobLogPage(
            job: JobSummary(result.job, target: target),
            records: result.records.map(JobLogRecordSummary.init),
            hasMore: result.hasMore_p
        )
    }

    func listPairings() async throws -> [PairingSummary] {
        let response = try await call(.listPairings(Computehop_Local_V1_ListPairingsRequest()))
        guard case .listPairings(let pairings)? = response.result else {
            throw LocalDaemonError.invalidResponse("missing pairing list")
        }
        return pairings.pairings.map(PairingSummary.init)
    }

    func beginPairing(device selector: String) async throws -> PairingSummary {
        var operation = Computehop_Local_V1_BeginPairingRequest()
        operation.deviceSelector = selector
        let response = try await call(.beginPairing(operation))
        guard case .beginPairing(let result)? = response.result, result.hasPairing else {
            throw LocalDaemonError.invalidResponse("missing new pairing")
        }
        return PairingSummary(result.pairing)
    }

    func confirmPairing(id: String) async throws {
        var operation = Computehop_Local_V1_ConfirmPairingRequest()
        operation.pairingSelector = id
        let response = try await call(.confirmPairing(operation))
        guard case .confirmPairing(let result)? = response.result, result.hasPairing else {
            throw LocalDaemonError.invalidResponse("missing confirmed pairing")
        }
    }

    func rejectPairing(id: String) async throws {
        var operation = Computehop_Local_V1_RejectPairingRequest()
        operation.pairingSelector = id
        let response = try await call(.rejectPairing(operation))
        guard case .rejectPairing(let result)? = response.result, result.hasPairing else {
            throw LocalDaemonError.invalidResponse("missing rejected pairing")
        }
    }

    func cancelJob(id: String) async throws {
        var operation = Computehop_Local_V1_CancelJobRequest()
        operation.jobID = id
        let response = try await call(.cancelJob(operation))
        guard case .cancelJob(let result)? = response.result, result.hasJob else {
            throw LocalDaemonError.invalidResponse("missing cancelled job")
        }
    }

    private func call(
        _ operation: Computehop_Local_V1_Request.OneOf_Operation
    ) async throws -> Computehop_Local_V1_Response {
        let token = try loadCapabilityToken()
        let requestID = UUID().uuidString.lowercased()
        var request = Computehop_Local_V1_Request()
        request.protocolVersion = Self.protocolVersion
        request.requestID = requestID
        request.capabilityToken = token
        request.operation = operation

        let payload = try request.serializedData()
        let connection = NWConnection(to: .unix(path: socketURL.path), using: .tcp)
        defer { connection.cancel() }

        try await start(connection)
        try await send(try LocalIPCFrame.encode(payload), over: connection)
        let header = try await receiveExactly(4, from: connection)
        let responseLength = try LocalIPCFrame.payloadLength(from: header)
        let responseData = try await receiveExactly(responseLength, from: connection)
        let response = try Computehop_Local_V1_Response(serializedBytes: responseData)

        guard response.protocolVersion == Self.protocolVersion else {
            throw LocalDaemonError.invalidResponse(
                "protocol version \(response.protocolVersion) is not supported"
            )
        }
        guard response.requestID == requestID else {
            throw LocalDaemonError.invalidResponse("request identifier does not match")
        }
        if response.hasError {
            throw LocalDaemonError.remote(response.error.message)
        }
        return response
    }

    private func loadCapabilityToken() throws -> Data {
        guard FileManager.default.fileExists(atPath: tokenURL.path) else {
            throw LocalDaemonError.notRunning
        }
        let values = try tokenURL.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey])
        guard values.isRegularFile == true, values.isSymbolicLink != true else {
            throw LocalDaemonError.invalidCapabilityToken("token must be a regular file")
        }
        let attributes = try FileManager.default.attributesOfItem(atPath: tokenURL.path)
        if let permissions = attributes[.posixPermissions] as? NSNumber,
           permissions.intValue & 0o077 != 0 {
            throw LocalDaemonError.invalidCapabilityToken("file permissions must be owner-only")
        }
        let encoded = try String(contentsOf: tokenURL, encoding: .utf8)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        let padding = String(repeating: "=", count: (4 - encoded.count % 4) % 4)
        guard let token = Data(base64Encoded: encoded + padding), token.count == 32 else {
            throw LocalDaemonError.invalidCapabilityToken("expected 32 random bytes")
        }
        return token
    }

    private func start(_ connection: NWConnection) async throws {
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                let gate = ContinuationGate<Void>(continuation)
                connection.stateUpdateHandler = { state in
                    switch state {
                    case .ready:
                        gate.succeed(())
                    case .waiting(let error), .failed(let error):
                        gate.fail(LocalDaemonError.transport(error.localizedDescription))
                    case .cancelled:
                        gate.fail(CancellationError())
                    default:
                        break
                    }
                }
                connection.start(queue: queue)
            }
        } onCancel: {
            connection.cancel()
        }
    }

    private func send(_ data: Data, over connection: NWConnection) async throws {
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                let gate = ContinuationGate<Void>(continuation)
                connection.send(
                    content: data,
                    contentContext: .defaultMessage,
                    isComplete: true,
                    completion: .contentProcessed { error in
                        if let error {
                            gate.fail(LocalDaemonError.transport(error.localizedDescription))
                        } else {
                            gate.succeed(())
                        }
                    }
                )
            }
        } onCancel: {
            connection.cancel()
        }
    }

    private func receiveExactly(_ count: Int, from connection: NWConnection) async throws -> Data {
        var result = Data()
        while result.count < count {
            let remaining = count - result.count
            let chunk = try await receive(maximumLength: remaining, from: connection)
            result.append(chunk)
        }
        return result
    }

    private func receive(maximumLength: Int, from connection: NWConnection) async throws -> Data {
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                let gate = ContinuationGate<Data>(continuation)
                connection.receive(minimumIncompleteLength: 1, maximumLength: maximumLength) {
                    data, _, isComplete, error in
                    if let error {
                        gate.fail(LocalDaemonError.transport(error.localizedDescription))
                    } else if let data, !data.isEmpty {
                        gate.succeed(data)
                    } else if isComplete {
                        gate.fail(LocalDaemonError.invalidFrame("connection closed early"))
                    } else {
                        gate.fail(LocalDaemonError.invalidFrame("received an empty stream segment"))
                    }
                }
            }
        } onCancel: {
            connection.cancel()
        }
    }

    nonisolated static func defaultStateDirectory() -> URL {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("ComputeHop", isDirectory: true)
    }
}
