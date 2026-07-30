import AppKit
import SwiftUI
import UniformTypeIdentifiers

struct RunJobSection: View {
    @Bindable var model: AppModel
    @State private var isProjectDropTargeted = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Ask")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                Spacer()
                Text("on \(model.selectedTargetName)")
                    .font(.caption)
                    .foregroundStyle(model.selectedDeviceCanRun ? Color.secondary : Color.orange)
                    .lineLimit(1)
            }

            TextField("Run CI, build the app, run tests…", text: $model.taskRequestInput)
                .textFieldStyle(.roundedBorder)
                .onSubmit { model.planRequestedTask() }

            HStack(spacing: 8) {
                Button {
                    chooseProjectFolder()
                } label: {
                    Label(projectLabel, systemImage: model.workingDirectory.isEmpty ? "folder" : "folder.fill")
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                .font(.caption)
                .buttonStyle(.borderless)
                .foregroundStyle(.secondary)

                Button("Plan") {
                    model.planRequestedTask()
                }
                .disabled(!model.canPlanTask)
            }

            if let planningError = model.planningError {
                Text(planningError)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
            }

            if let plan = model.plannedTask {
                planPreview(plan)
            }
        }
        .padding(.vertical, 2)
        .onDrop(of: [UTType.fileURL.identifier], isTargeted: $isProjectDropTargeted) { providers in
            handleProjectDrop(providers)
        }
        .overlay {
            if isProjectDropTargeted {
                RoundedRectangle(cornerRadius: 8)
                    .stroke(Color.accentColor, lineWidth: 2)
            }
        }
        .onAppear(perform: alignRunTargetWithSelectedDevice)
        .onChange(of: model.selectedDeviceID) {
            alignRunTargetWithSelectedDevice()
        }
        .onChange(of: model.devices.map(\.id)) {
            alignRunTargetWithSelectedDevice()
        }
    }

    private func planPreview(_ plan: TaskPlan) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Divider()
            Text(plan.title)
                .font(.caption.weight(.semibold))
            ForEach(Array(plan.commands.enumerated()), id: \.offset) { index, command in
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text("\(index + 1).")
                        .foregroundStyle(.secondary)
                    Text(command)
                        .font(.system(.caption, design: .monospaced))
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
            }
            if !plan.outputs.isEmpty {
                Text("Returns \(plan.outputs.joined(separator: ", "))")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            HStack {
                Spacer()
                Button("Do it") {
                    Task { await model.submitPlannedTask() }
                }
                .buttonStyle(.borderedProminent)
                .disabled(!model.canSubmitPlannedTask)
                .help(model.selectedDeviceCanRun ? "Run this approved plan." : "\(model.selectedTargetName) is not available.")
            }
        }
        .padding(.top, 2)
    }

    private var projectLabel: String {
        let trimmed = model.workingDirectory.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            return "Project"
        }
        return URL(fileURLWithPath: trimmed).lastPathComponent
    }

    private func chooseProjectFolder() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "Choose"
        if panel.runModal() == .OK, let url = panel.url {
            model.workingDirectory = url.path
        }
    }

    private func alignRunTargetWithSelectedDevice() {
        guard model.selectedDeviceID != AppModel.localDeviceID else {
            model.runTargetID = ""
            return
        }
        if let selectedDevice = model.selectedDevice {
            model.runTargetID = selectedDevice.id
        }
    }

    private func handleProjectDrop(_ providers: [NSItemProvider]) -> Bool {
        guard let provider = providers.first(where: {
            $0.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier)
        }) else {
            return false
        }
        provider.loadItem(forTypeIdentifier: UTType.fileURL.identifier, options: nil) { item, _ in
            let url: URL?
            if let data = item as? Data {
                url = URL(dataRepresentation: data, relativeTo: nil)
            } else {
                url = item as? URL
            }
            guard let path = url?.path else { return }
            Task { @MainActor in
                model.workingDirectory = path
            }
        }
        return true
    }
}
