import Foundation
import Testing
@testable import ComputeHopApp

@Test
func frameUsesBigEndianLengthPrefix() throws {
    let frame = try LocalIPCFrame.encode(Data([0xaa, 0xbb, 0xcc]))
    #expect(Array(frame.prefix(4)) == [0, 0, 0, 3])
    #expect(Array(frame.dropFirst(4)) == [0xaa, 0xbb, 0xcc])
    #expect(try LocalIPCFrame.payloadLength(from: frame.prefix(4)) == 3)
}

@Test
func frameRejectsEmptyAndOversizedPayloads() {
    #expect(throws: LocalDaemonError.self) {
        try LocalIPCFrame.encode(Data())
    }
    #expect(throws: LocalDaemonError.self) {
        try LocalIPCFrame.encode(Data(count: LocalIPCFrame.maximumPayloadBytes + 1))
    }
}

@Test
func localDaemonErrorExplainsIncompatibleDaemonForMenuUsers() {
    let error = LocalDaemonError.incompatibleDaemon(
        "Daemon protocol version 5 is not supported by app protocol version 6."
    )
    #expect(error.errorDescription?.contains("ComputeHop daemon does not match this menu app") == true)
    #expect(error.errorDescription?.contains("Reinstall or restart ComputeHop") == true)
    #expect(error.errorDescription?.contains("protocol version 5") == true)
}

@Test
func localDaemonArtifactErrorsExplainNoDeclaredOutputs() {
    let jobID = "7a338fa3-7ba4-4c54-bf59-da1161f6b76f"
    let error = LocalDaemonClient.artifactFetchError(
        id: jobID,
        error: LocalDaemonError.remote(
            .conflict,
            "job artifacts are not ready: job \(jobID) is succeeded"
        )
    )

    #expect(error.localizedDescription.contains("No declared outputs are available") == true)
    #expect(error.localizedDescription.contains("-o/--output") == true)
}

@Test
func localDaemonArtifactErrorsExplainNotReadyOutputs() {
    let jobID = "7a338fa3-7ba4-4c54-bf59-da1161f6b76f"
    let error = LocalDaemonClient.artifactFetchError(
        id: jobID,
        error: LocalDaemonError.remote(
            .conflict,
            "job artifacts are not ready: job \(jobID) is running"
        )
    )

    #expect(error.localizedDescription.contains("Outputs for \(jobID) are not ready yet") == true)
    #expect(error.localizedDescription.contains("Wait for the job to succeed") == true)
}

@Test
func localDaemonArtifactErrorsExplainMissingOutputs() {
    let jobID = "7a338fa3-7ba4-4c54-bf59-da1161f6b76f"
    let error = LocalDaemonClient.artifactFetchError(
        id: jobID,
        error: LocalDaemonError.remote(
            .notFound,
            "job artifacts not found: \(jobID)"
        )
    )

    #expect(error.localizedDescription.contains("Outputs were not found for \(jobID)") == true)
    #expect(error.localizedDescription.contains("job ID/worker") == true)
}
