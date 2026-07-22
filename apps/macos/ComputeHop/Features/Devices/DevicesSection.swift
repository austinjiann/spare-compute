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
                        Image(systemName: device.availability == .nearby ? "circle.fill" : "circle")
                            .font(.system(size: 8))
                            .foregroundStyle(device.availability == .nearby ? .green : .secondary)
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
                            Text(device.availability.rawValue)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }
        }
    }
}
