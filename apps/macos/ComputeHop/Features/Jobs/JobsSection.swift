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
                            Text("\(job.state) · \(job.target) · \(job.shortID)")
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button("Logs") {
                            Task { await model.showLogs(for: job) }
                        }
                        .disabled(model.isLoadingLogs)
                        if !job.terminal {
                            Button("Cancel") {
                                Task { await model.cancel(job) }
                            }
                            .disabled(model.actionInProgress != nil)
                        }
                    }
                }
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
}
