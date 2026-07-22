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
                    ForEach(model.runnableDevices) { device in
                        Text(device.name).tag(device.id)
                    }
                }
                TextField(
                    model.runTargetID.isEmpty ? "Working directory (home by default)" : "Working directory on worker",
                    text: $model.workingDirectory
                )
                .textFieldStyle(.roundedBorder)
            }
            HStack {
                Text("Quotes group arguments; no shell expansion is performed.")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                Spacer()
                Button("Run") {
                    Task { await model.submitCommand() }
                }
                .buttonStyle(.borderedProminent)
                .disabled(
                    !model.isConnected ||
                    model.commandInput.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ||
                    model.actionInProgress != nil
                )
            }
        }
    }
}
