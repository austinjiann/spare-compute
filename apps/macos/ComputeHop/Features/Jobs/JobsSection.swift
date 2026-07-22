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
                            Text("\(job.state) · \(job.shortID)")
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        if !job.terminal {
                            Button("Cancel") {
                                Task { await model.cancel(job) }
                            }
                            .disabled(model.actionInProgress != nil)
                        }
                    }
                }
            }
        }
    }
}
