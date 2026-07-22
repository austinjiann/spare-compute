import AppKit
import SwiftUI

private struct SettingsClipboardWriter: ClipboardWriting {
    func write(_ value: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(value, forType: .string)
    }
}

struct SettingsSection: View {
    @Bindable var model: AppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Settings")
                .font(.headline)
            Toggle("Notify when jobs finish", isOn: $model.jobCompletionNotificationsEnabled)
            Text("Notifications fire only for jobs the menu has already observed while running.")
                .font(.caption2)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            VStack(alignment: .leading, spacing: 4) {
                Text("Worker setup defaults")
                    .font(.caption.weight(.semibold))
                TextField("Worker name, for example: Gaming PC", text: $model.workerSetupDeviceName)
                    .textFieldStyle(.roundedBorder)
                TextField("Cache size, for example: 40GiB (optional)", text: $model.workerSetupCacheSize)
                    .textFieldStyle(.roundedBorder)
                Text("These values update the setup commands shown when no worker is connected.")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Troubleshooting")
                        .font(.caption.weight(.semibold))
                    Text("Copy status, device, connection, job, and doctor commands.")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer()
                Button("Copy Diagnostics") {
                    model.copyDiagnosticsCommandBundle(to: SettingsClipboardWriter())
                }
                .help("Copy useful Terminal commands for debugging setup and connectivity.")
            }
        }
    }
}
