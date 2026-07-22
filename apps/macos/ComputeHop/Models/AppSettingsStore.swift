import Foundation

@MainActor
protocol AppSettingsStoring {
    var jobCompletionNotificationsEnabled: Bool { get }
    func setJobCompletionNotificationsEnabled(_ enabled: Bool)
}

final class UserDefaultsAppSettingsStore: AppSettingsStoring {
    private let defaults: UserDefaults
    private let notificationsKey = "jobCompletionNotificationsEnabled"

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    var jobCompletionNotificationsEnabled: Bool {
        guard defaults.object(forKey: notificationsKey) != nil else {
            return true
        }
        return defaults.bool(forKey: notificationsKey)
    }

    func setJobCompletionNotificationsEnabled(_ enabled: Bool) {
        defaults.set(enabled, forKey: notificationsKey)
    }
}
