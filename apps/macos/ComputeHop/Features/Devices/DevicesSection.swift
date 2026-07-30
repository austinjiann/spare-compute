import SwiftUI

struct DevicesSection: View {
    @Bindable var model: AppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Device")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .padding(.bottom, 4)

            deviceMenu

            selectedStatus
        }
    }

    private var deviceMenu: some View {
        Menu {
            Button {
                model.selectLocalDevice()
            } label: {
                Label("This Mac", systemImage: model.selectedDeviceID == AppModel.localDeviceID ? "checkmark" : "desktopcomputer")
            }

            if !model.devices.isEmpty {
                Divider()
            }

            ForEach(model.devices) { device in
                Button {
                    if device.canPair {
                        Task { await model.connect(device) }
                    } else {
                        model.selectDevice(device)
                    }
                } label: {
                    Label(
                        devicePickerLabel(device),
                        systemImage: model.selectedDeviceID == device.id ? "checkmark" : statusIcon(for: device)
                    )
                }
            }
        } label: {
            menuRow(
                icon: "desktopcomputer",
                title: "Run on",
                value: model.selectedTargetName
            )
        }
        .menuStyle(.borderlessButton)
    }

    private var selectedStatus: some View {
        Group {
            if let selected = model.selectedDevice {
                HStack(spacing: 10) {
                    statusDot(selected.availability)
                    Text(shortStatus(for: selected))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                    if selected.canPair {
                        Button("Connect") {
                            Task { await model.connect(selected) }
                        }
                        .disabled(model.actionInProgress != nil)
                    }
                }
                .padding(.vertical, 4)
            }
        }
    }

    private func menuRow(icon: String, title: String, value: String) -> some View {
        HStack(spacing: 12) {
            Image(systemName: icon)
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(.secondary)
                .frame(width: 24, height: 24)
                .background(.quaternary, in: Circle())
            Text(title)
                .font(.body.weight(.semibold))
            Spacer()
            Text(value)
                .font(.body)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            Image(systemName: "chevron.right")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
        }
        .contentShape(Rectangle())
        .padding(.vertical, 7)
    }

    private func statusIcon(for device: DeviceSummary) -> String {
        if device.canPair {
            return "plus.circle"
        }
        switch device.availability {
        case .nearby, .remote:
            return "circle.fill"
        case .connecting:
            return "circle.dotted"
        case .offline:
            return "circle"
        }
    }

    private func statusDot(_ availability: DeviceSummary.Availability) -> some View {
        Circle()
            .fill(availabilityColor(availability))
            .frame(width: 7, height: 7)
            .opacity(availability == .offline ? 0.45 : 1)
    }

    private func availabilityColor(_ availability: DeviceSummary.Availability) -> Color {
        switch availability {
        case .nearby: return .green
        case .remote: return .blue
        case .connecting: return .orange
        case .offline: return .secondary
        }
    }

    private func shortStatus(for device: DeviceSummary) -> String {
        switch device.availability {
        case .nearby:
            return "Available"
        case .remote:
            return "Available"
        case .connecting:
            return "Connecting"
        case .offline:
            return "Offline"
        }
    }

    private func devicePickerLabel(_ device: DeviceSummary) -> String {
        if device.canPair {
            return "\(device.name) · New"
        }
        if device.availability == .offline {
            return "\(device.name) · Offline"
        }
        return device.name
    }
}
