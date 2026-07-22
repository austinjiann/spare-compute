import SwiftUI

struct SettingsSection: View {
    @Bindable var model: AppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Settings")
                .font(.headline)
            Toggle("Notify when jobs finish", isOn: $model.jobCompletionNotificationsEnabled)
            Text("Notifications fire only for jobs the menu has already observed while running.")
                .font(.caption2)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}
