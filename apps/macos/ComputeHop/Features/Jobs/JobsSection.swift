import AppKit
import SwiftUI

struct JobsSection: View {
    let model: AppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Recent Jobs")
                .font(.headline)
            if model.jobs.isEmpty {
                Text("No jobs yet")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(Array(model.jobs.prefix(5))) { job in
                    HStack(spacing: 8) {
                        VStack(alignment: .leading, spacing: 1) {
                            Text(job.command)
                                .lineLimit(1)
                            Text(jobDetailLine(job))
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button("Logs") {
                            Task { await model.showLogs(for: job) }
                        }
                        .disabled(model.isLoadingLogs)
                        if job.canFetchOutputs {
                            Button("Outputs…") {
                                chooseArtifactDestination(for: job)
                            }
                            .disabled(model.actionInProgress != nil)
                        }
                        if !job.terminal {
                            Button("Cancel") {
                                Task { await model.cancel(job) }
                            }
                            .disabled(model.actionInProgress != nil)
                        }
                    }
                }
            }

            if let message = model.artifactMessage {
                Text(message)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            if let selectedJobID = model.selectedJobID {
                VStack(alignment: .leading, spacing: 5) {
                    HStack {
                        Text("Output · \(String(selectedJobID.prefix(8)))")
                            .font(.caption.weight(.semibold))
                        Spacer()
                        if model.isLoadingLogs {
                            ProgressView()
                                .controlSize(.small)
                        }
                        Button {
                            model.closeLogs()
                        } label: {
                            Image(systemName: "xmark")
                        }
                        .buttonStyle(.plain)
                    }
                    ScrollView {
                        Text(model.selectedJobLogs.isEmpty ? "No output yet" : model.selectedJobLogs)
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(model.selectedJobLogs.isEmpty ? .secondary : .primary)
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .frame(height: 120)
                    .padding(6)
                    .background(.black.opacity(0.08), in: RoundedRectangle(cornerRadius: 6))
                    if model.selectedJobLogsTruncated {
                        Text("Showing the newest 128,000 characters loaded in this session.")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
    }

    private func chooseArtifactDestination(for job: JobSummary) {
        let panel = NSSavePanel()
        panel.title = "Choose Output Folder"
        panel.prompt = "Restore Outputs"
        panel.canCreateDirectories = true
        panel.nameFieldStringValue = "ComputeHop-\(job.shortID)"
        if panel.runModal() == .OK, let destination = panel.url {
            Task { await model.fetchArtifacts(for: job, destination: destination.path) }
        }
    }

    private func jobDetailLine(_ job: JobSummary) -> String {
        if let progress = job.progressText {
            return "\(job.state) · \(progress) · \(job.target) · \(job.shortID)"
        }
        return "\(job.state) · \(job.target) · \(job.shortID)"
    }
}
