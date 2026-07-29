import ComputeHopProtocol
import Testing
@testable import ComputeHopApp

@Test
func pairingSummaryExplainsBothDevicesStillNeedConfirmation() {
    var value = Computehop_Local_V1_Pairing()
    value.id = "pairing-id"
    value.peerName = "Gaming PC"
    value.verificationCode = "0123-4567-89AB-CDEF"
    value.state = .waiting

    let summary = PairingSummary(value)
    #expect(summary.needsLocalConfirmation)
    #expect(summary.confirmationStatusText == "This device: not yet · Other device: not yet")
    #expect(summary.instructionText == "Confirm only if this exact code appears on both devices.")
}

@Test
func pairingSummaryExplainsOtherDeviceAlreadyConfirmed() {
    var value = Computehop_Local_V1_Pairing()
    value.id = "pairing-id"
    value.peerName = "Gaming PC"
    value.verificationCode = "0123-4567-89AB-CDEF"
    value.state = .waiting
    value.remoteConfirmed = true

    let summary = PairingSummary(value)
    #expect(summary.needsLocalConfirmation)
    #expect(summary.confirmationStatusText == "This device: not yet · Other device: confirmed")
    #expect(summary.instructionText == "The other device already confirmed. Click Codes Match here only if this exact code matches.")
}

@Test
func pairingSummaryExplainsWaitingForOtherDevice() {
    var value = Computehop_Local_V1_Pairing()
    value.id = "pairing-id"
    value.peerName = "Gaming PC"
    value.verificationCode = "0123-4567-89AB-CDEF"
    value.state = .waiting
    value.localConfirmed = true

    let summary = PairingSummary(value)
    #expect(!summary.needsLocalConfirmation)
    #expect(summary.confirmationStatusText == "This device: confirmed · Other device: not yet")
    #expect(summary.instructionText == "This device is confirmed. Finish on the other device to complete the connection.")
}
