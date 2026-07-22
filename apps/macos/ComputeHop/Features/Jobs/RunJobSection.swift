import AppKit
import SwiftUI

struct RunJobSection: View {
    @Bindable var model: AppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Run a Job")
                .font(.headline)
            TextField("Command, for example: cargo build --release", text: $model.commandInput)
                .textFieldStyle(.roundedBorder)
                .onSubmit { Task { await model.submitCommand() } }
            HStack {
                Picker("Run on", selection: $model.runTargetID) {
                    Text("This Mac").tag("")
                    if model.canRunAutomatically {
                        Text("Auto worker").tag(AppModel.automaticWorkerTargetID)
                    }
                    ForEach(model.runnableDevices) { device in
                        Text(device.name).tag(device.id)
                    }
                }
                TextField(
                    model.isRemoteRunTargetSelected ? "Project folder on this Mac" : "Working directory (home by default)",
                    text: $model.workingDirectory
                )
                .textFieldStyle(.roundedBorder)
                if model.isRemoteRunTargetSelected {
                    Button("Choose…") {
                        chooseProjectFolder()
                    }
                }
            }
            TextField(
                "Outputs to return, comma-separated (for example: dist, report.json)",
                text: $model.outputsInput
            )
            .textFieldStyle(.roundedBorder)
            HStack {
                Text("Quotes group arguments; no shell expansion is performed.")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                Spacer()
                Button("Smoke Test") {
                    Task { await model.submitSmokeTest() }
                }
                .disabled(
                    !model.isConnected ||
                    !model.canSubmitSmokeTest ||
                    model.actionInProgress != nil
                )
                .help("Run hostname on a worker without uploading a project")
                Button("Run") {
                    Task { await model.submitCommand() }
                }
                .buttonStyle(.borderedProminent)
                .disabled(
                    !model.isConnected ||
                    model.commandInput.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ||
                    (model.isRemoteRunTargetSelected && model.workingDirectory.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty) ||
                    model.actionInProgress != nil
                )
            }
        }
    }

    private func chooseProjectFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.canCreateDirectories = false
        panel.prompt = "Choose Project"
        if panel.runModal() == .OK, let selected = panel.url {
            model.workingDirectory = selected.path
        }
    }
}
