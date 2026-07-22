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
    let version = try await client.ping()
    #expect(!version.isEmpty)
}
