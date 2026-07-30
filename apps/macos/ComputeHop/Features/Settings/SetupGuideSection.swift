import SwiftUI

struct SetupGuideSection: View {
    let model: AppModel

    var body: some View {
        if let guide = model.setupGuide {
            HStack(spacing: 12) {
                Image(systemName: statusIcon(for: guide.title))
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(.secondary)
                    .frame(width: 24, height: 24)
                    .background(.quaternary, in: Circle())
                Text(guide.title)
                    .font(.headline)
                    .lineLimit(1)
                Spacer()
                if model.canConnectNearbyWorker {
                    Button("Connect") {
                        Task { await model.connectNearbyWorker() }
                    }
                    .disabled(model.actionInProgress != nil)
                }
            }
            .padding(.vertical, 4)
            .help(guide.detail)
        }
    }

    private func statusIcon(for title: String) -> String {
        let lowercased = title.lowercased()
        if lowercased.contains("offline") {
            return "wifi.slash"
        }
        if lowercased.contains("connect") {
            return "link"
        }
        return "plus.circle"
    }
}
