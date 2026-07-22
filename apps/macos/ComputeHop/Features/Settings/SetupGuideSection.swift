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
            }
            .padding(8)
            .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 10))
        }
    }
}
