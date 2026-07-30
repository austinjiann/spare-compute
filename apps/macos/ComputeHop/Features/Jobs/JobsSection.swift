import AppKit
import SwiftUI

struct JobsSection: View {
    let model: AppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Jobs")
                .font(.headline)
            if model.jobs.isEmpty {
                Text("No jobs yet")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(Array(model.jobs.prefix(3))) { job in
                    HStack(spacing: 7) {
                        VStack(alignment: .leading, spacing: 1) {
                            Text(jobTitle(job))
                                .lineLimit(1)
                            Text(jobDetailLine(job))
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                        Spacer(minLength: 6)
                        Button {
                            Task { await model.showLogs(for: job) }
                        } label: {
                            Image(systemName: "doc.text")
                        }
                        .buttonStyle(.borderless)
                        .disabled(model.isLoadingLogs)
                        .help("Logs")
                        if job.canFetchOutputs {
                            Button {
                                chooseArtifactDestination(for: job)
                            } label: {
                                Image(systemName: "square.and.arrow.down")
                            }
                            .buttonStyle(.borderless)
                            .disabled(model.actionInProgress != nil)
                            .help("Restore outputs")
                        }
                        if !job.terminal {
                            Button(role: .destructive) {
                                Task { await model.cancel(job) }
                            } label: {
                                Image(systemName: "stop.fill")
                            }
                            .buttonStyle(.borderless)
                            .disabled(model.actionInProgress != nil)
                            .help("Cancel")
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
                        Text(String(selectedJobID.prefix(8)))
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
                        Text(model.selectedJobLogs.isEmpty ? model.selectedJobLogsPlaceholder : model.selectedJobLogs)
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(model.selectedJobLogs.isEmpty ? Color.secondary : Color.primary)
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .frame(height: 90)
                    .padding(6)
                    .background(.black.opacity(0.08), in: RoundedRectangle(cornerRadius: 6))
                    if model.selectedJobLogsTruncated {
                        Text("Truncated")
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
            return "\(job.state) · \(progress) · \(friendlyTarget(job.target))"
        }
        return "\(job.state) · \(friendlyTarget(job.target))"
    }

    private func jobTitle(_ job: JobSummary) -> String {
        let parts = job.command.split(separator: " ", maxSplits: 1, omittingEmptySubsequences: true)
        guard let executable = parts.first else { return "Task" }
        let name = URL(fileURLWithPath: String(executable)).lastPathComponent
        guard let rest = parts.dropFirst().first else { return name }
        return "\(name) \(rest)"
    }

    private func friendlyTarget(_ target: String) -> String {
        target == "This Mac" ? "Here" : target
    }
}
