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
    @State private var isExpanded = false

    var body: some View {
        DisclosureGroup("Settings", isExpanded: $isExpanded) {
            VStack(alignment: .leading, spacing: 6) {
                Toggle("Notify when jobs finish", isOn: $model.jobCompletionNotificationsEnabled)
                    .toggleStyle(.checkbox)
                TextField("Worker name", text: $model.workerSetupDeviceName)
                    .textFieldStyle(.roundedBorder)
                TextField("Cache size", text: $model.workerSetupCacheSize)
                    .textFieldStyle(.roundedBorder)
                DisclosureGroup("VPS") {
                    VStack(alignment: .leading, spacing: 4) {
                        TextField("Connectivity domain", text: $model.vpsConnectivityDomain)
                            .textFieldStyle(.roundedBorder)
                        TextField("TURN domain", text: $model.vpsTurnDomain)
                            .textFieldStyle(.roundedBorder)
                    }
                }
                Button("Copy Diagnostics") {
                    model.copyDiagnosticsCommandBundle(to: SettingsClipboardWriter())
                }
                .help("Copy useful Terminal commands for debugging setup and connectivity.")
            }
        }
        .font(.caption)
    }
}
