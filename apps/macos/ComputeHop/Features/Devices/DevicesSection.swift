import SwiftUI

struct DevicesSection: View {
    let model: AppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Devices")
                .font(.headline)
            if model.devices.isEmpty {
                Text("No nearby or paired devices")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(model.devices) { device in
                    HStack(spacing: 8) {
                        Image(systemName: device.availability == .offline ? "circle" : "circle.fill")
                            .font(.system(size: 8))
                            .foregroundStyle(availabilityColor(device.availability))
                        VStack(alignment: .leading, spacing: 1) {
                            Text(device.name)
                            Text("\(device.role) · \(device.trust) · \(device.shortID)")
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        if device.canPair {
                            Button("Pair") {
                                Task { await model.pair(device) }
                            }
                            .disabled(model.actionInProgress != nil)
                        } else {
                            Text([device.availability.rawValue, device.path].compactMap { $0 }.joined(separator: " · "))
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }
        }
    }

    private func availabilityColor(_ availability: DeviceSummary.Availability) -> Color {
        switch availability {
        case .nearby: return .green
        case .remote: return .blue
        case .connecting: return .orange
        case .offline: return .secondary
        }
    }
}
