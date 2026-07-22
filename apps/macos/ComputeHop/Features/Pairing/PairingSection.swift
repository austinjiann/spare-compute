import SwiftUI

struct PairingSection: View {
    let model: AppModel

    private var activePairings: [PairingSummary] {
        model.pairings.filter { $0.state == "Waiting" }
    }

    var body: some View {
        if !activePairings.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
                Text("Connect Device")
                    .font(.headline)
                ForEach(activePairings) { pairing in
                    VStack(alignment: .leading, spacing: 6) {
                        Text(pairing.peerName)
                        Text(pairing.verificationCode)
                            .font(.system(.body, design: .monospaced, weight: .semibold))
                            .textSelection(.enabled)
                        Text(pairing.confirmationStatusText)
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                        Text(pairing.instructionText)
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                        HStack {
                            Button("Reject", role: .destructive) {
                                Task { await model.reject(pairing) }
                            }
                            if pairing.needsLocalConfirmation {
                                Button("Codes Match") {
                                    Task { await model.confirm(pairing) }
                                }
                                .buttonStyle(.borderedProminent)
                            } else {
                                Text("Waiting for the other device to confirm")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                        }
                        .disabled(model.actionInProgress != nil)
                    }
                    .padding(8)
                    .background(.quaternary, in: RoundedRectangle(cornerRadius: 8))
                }
            }
            Divider()
        }
    }
}
