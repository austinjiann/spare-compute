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
                    model.isNoProjectRemoteRunSelected
                        ? "No project will be uploaded"
                        : model.isRemoteRunTargetSelected
                            ? "Project folder on this Mac"
                            : "Working directory (home by default)",
                    text: $model.workingDirectory
                )
                .textFieldStyle(.roundedBorder)
                .disabled(model.isNoProjectRemoteRunSelected)
                if model.isRemoteRunTargetSelected {
                    Button("Choose…") {
                        chooseProjectFolder()
                    }
                    .disabled(model.isNoProjectRemoteRunSelected)
                }
            }
            if model.isRemoteRunTargetSelected {
                Toggle("Skip project upload for utility command", isOn: $model.remoteRunWithoutProject)
                    .font(.caption)
                if model.isNoProjectRemoteRunSelected {
                    Text("Runs without local files and cannot return declared outputs.")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            TextField(
                model.isNoProjectRemoteRunSelected
                    ? "Outputs disabled for no-project runs"
                    : "Outputs to return, comma-separated (for example: dist, report.json)",
                text: $model.outputsInput
            )
            .textFieldStyle(.roundedBorder)
            .disabled(model.isNoProjectRemoteRunSelected)
            if let runDisabledReason = model.runDisabledReason {
                Text(runDisabledReason)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let smokeTestDisabledReason = model.smokeTestDisabledReason {
                Text(smokeTestDisabledReason)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
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
                .help(model.smokeTestHelpText)
                Button("Run") {
                    Task { await model.submitCommand() }
                }
                .buttonStyle(.borderedProminent)
                .disabled(!model.canSubmitCommand)
                .help(model.runHelpText)
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
