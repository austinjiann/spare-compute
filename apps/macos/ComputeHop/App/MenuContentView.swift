import AppKit
import SwiftUI

struct MenuContentView: View {
    let model: AppModel
    @State private var showAdvanced = false

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            header

            if let error = model.lastError {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .lineLimit(2)
            }

            SetupGuideSection(model: model)
            Divider()
            PairingSection(model: model)
            DevicesSection(model: model)
            Divider()
            RunJobSection(model: model)
            if model.menuTaskJob != nil {
                Divider()
                JobsSection(model: model)
            }
            Divider()
            advanced

            Divider()
            footer
        }
        .padding(14)
        .frame(width: 360)
    }

    private var header: some View {
        HStack(spacing: 8) {
            BrandSymbol.view
                .resizable()
                .scaledToFit()
                .frame(width: 16, height: 16)
                .foregroundStyle(.primary)
            VStack(alignment: .leading, spacing: 1) {
                Text("ComputeHop")
                    .font(.headline)
                Text(headerSubtitle)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer()
        }
    }

    private var headerSubtitle: String {
        guard let daemon = model.daemon else { return "Daemon offline" }
        if let deviceName = daemon.deviceName {
            return "Ready on \(deviceName)"
        }
        return daemon.daemonText
    }

    private var advanced: some View {
        DisclosureGroup("Advanced", isExpanded: $showAdvanced) {
            VStack(alignment: .leading, spacing: 12) {
                SettingsSection(model: model)
            }
            .padding(.top, 4)
        }
        .font(.caption)
    }

    private var footer: some View {
        HStack {
            Spacer()
            Button {
                model.openControlCenter()
            } label: {
                Image(systemName: "slider.horizontal.3")
            }
            .buttonStyle(.plain)
            .help("Open Control Center")
            Button {
                Task { await model.refresh() }
            } label: {
                Image(systemName: "arrow.clockwise")
            }
            .buttonStyle(.plain)
            .disabled(model.isRefreshing)
            .help("Refresh")
            Button {
                NSApplication.shared.terminate(nil)
            } label: {
                Image(systemName: "power")
            }
            .buttonStyle(.plain)
            .help("Quit")
        }
    }
}
