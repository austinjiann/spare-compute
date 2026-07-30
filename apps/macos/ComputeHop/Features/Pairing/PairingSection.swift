import SwiftUI

struct PairingSection: View {
    let model: AppModel

    private var activePairings: [PairingSummary] {
        model.pairings.filter { $0.state == "Waiting" }
    }

    var body: some View {
        if !activePairings.isEmpty {
            VStack(alignment: .leading, spacing: 6) {
                Text("Connect")
                    .font(.headline)
                ForEach(activePairings) { pairing in
                    HStack(spacing: 8) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(pairing.peerName)
                                .lineLimit(1)
                            Text(pairing.verificationCode)
                                .font(.system(.caption, design: .monospaced, weight: .semibold))
                                .textSelection(.enabled)
                            Text(pairing.instructionText)
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                                .lineLimit(2)
                        }
                        Spacer(minLength: 6)
                        HStack(spacing: 6) {
                            Button("Reject", role: .destructive) {
                                Task { await model.reject(pairing) }
                            }
                            if pairing.needsLocalConfirmation {
                                Button(pairing.confirmActionTitle) {
                                    Task { await model.confirm(pairing) }
                                }
                                .buttonStyle(.borderedProminent)
                            } else {
                                Text("Waiting")
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)
                            }
                        }
                        .disabled(model.actionInProgress != nil)
                    }
                    .padding(8)
                    .background(.quaternary, in: RoundedRectangle(cornerRadius: 8))
                    .help(pairing.instructionText)
                }
            }
            Divider()
        }
    }
}
