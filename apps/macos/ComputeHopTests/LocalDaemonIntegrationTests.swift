import Foundation
import Testing
@testable import ComputeHopApp

@Test
func localDaemonPingWhenIntegrationStateIsConfigured() async throws {
    guard let stateDirectory = ProcessInfo.processInfo.environment["COMPUTEHOP_INTEGRATION_STATE_DIR"] else {
        return
    }
    let client = LocalDaemonClient(
        stateDirectory: URL(fileURLWithPath: stateDirectory, isDirectory: true)
    )
    let daemon = try await client.ping()
    #expect(!daemon.version.isEmpty)
}

@Test
func localDaemonSubmissionAndLogsWhenExplicitlyEnabled() async throws {
    let environment = ProcessInfo.processInfo.environment
    guard environment["COMPUTEHOP_INTEGRATION_SUBMIT"] == "1",
          let stateDirectory = environment["COMPUTEHOP_INTEGRATION_STATE_DIR"] else {
        return
    }
    let client = LocalDaemonClient(
        stateDirectory: URL(fileURLWithPath: stateDirectory, isDirectory: true)
    )
    let submitted = try await client.submitJob(
        executable: "/usr/bin/printf",
        arguments: ["swift-ipc-ok\n"],
        workingDirectory: FileManager.default.homeDirectoryForCurrentUser.path,
        deviceSelector: "",
        target: "This Mac"
    )

    var current = submitted
    for _ in 0..<50 where !current.terminal {
        try await Task.sleep(for: .milliseconds(100))
        current = try await client.getJob(id: submitted.id)
    }
    #expect(current.state == "Succeeded")

    var afterSequence: UInt64 = 0
    var output = ""
    var hasMore = true
    while hasMore {
        let page = try await client.readJobLogs(id: submitted.id, afterSequence: afterSequence)
        for record in page.records {
            output.append(record.text)
            afterSequence = max(afterSequence, record.sequence)
        }
        hasMore = page.hasMore
    }
    #expect(output == "swift-ipc-ok\n")
}
