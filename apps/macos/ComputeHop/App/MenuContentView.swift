import AppKit
import SwiftUI

struct MenuContentView: View {
    let model: AppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("ComputeHop")
                        .font(.headline)
                    Text(model.isConnected ? "Daemon \(model.daemonVersion ?? "")" : "Daemon offline")
                        .font(.caption)
                        .foregroundStyle(model.isConnected ? Color.secondary : Color.red)
                }
                Spacer()
                Button {
                    Task { await model.refresh() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.plain)
                .disabled(model.isRefreshing)
                .help("Refresh")
            }

            if let error = model.lastError {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Divider()
            PairingSection(model: model)
            DevicesSection(model: model)
            Divider()
            RunJobSection(model: model)
            Divider()
            JobsSection(model: model)
            Divider()

            HStack {
                Text("Nearby addresses are untrusted hints; paired sessions still verify device keys.")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                Spacer()
                Button("Quit") { NSApplication.shared.terminate(nil) }
                    .buttonStyle(.plain)
            }
        }
        .padding(14)
        .frame(width: 420)
    }
}
