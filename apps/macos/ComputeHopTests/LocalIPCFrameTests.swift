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
