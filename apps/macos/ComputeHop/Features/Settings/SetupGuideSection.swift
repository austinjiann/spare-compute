import SwiftUI

struct SetupGuideSection: View {
    let model: AppModel

    var body: some View {
        if let guide = model.setupGuide {
            VStack(alignment: .leading, spacing: 6) {
                Label(guide.title, systemImage: "sparkle.magnifyingglass")
                    .font(.headline)
                Text(guide.detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                if let command = guide.command {
                    Text(command)
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 4)
                        .background(.quaternary, in: RoundedRectangle(cornerRadius: 6))
                }
                if model.canConnectNearbyWorker {
                    Button("Connect Nearby Worker") {
                        Task { await model.connectNearbyWorker() }
                    }
                    .disabled(model.actionInProgress != nil)
                }
            }
            .padding(8)
            .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 10))
        }
    }
}
