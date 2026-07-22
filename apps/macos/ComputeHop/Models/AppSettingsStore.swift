import Foundation

@MainActor
protocol AppSettingsStoring {
    var jobCompletionNotificationsEnabled: Bool { get }
    var workerSetupDeviceName: String { get }
    var workerSetupCacheSize: String { get }
    func setJobCompletionNotificationsEnabled(_ enabled: Bool)
    func setWorkerSetupDeviceName(_ value: String)
    func setWorkerSetupCacheSize(_ value: String)
}

final class UserDefaultsAppSettingsStore: AppSettingsStoring {
    private let defaults: UserDefaults
    private let notificationsKey = "jobCompletionNotificationsEnabled"
    private let workerSetupDeviceNameKey = "workerSetupDeviceName"
    private let workerSetupCacheSizeKey = "workerSetupCacheSize"

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    var jobCompletionNotificationsEnabled: Bool {
        guard defaults.object(forKey: notificationsKey) != nil else {
            return true
        }
        return defaults.bool(forKey: notificationsKey)
    }

    var workerSetupDeviceName: String {
        let value = defaults.string(forKey: workerSetupDeviceNameKey) ?? ""
        return value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "Gaming PC" : value
    }

    var workerSetupCacheSize: String {
        defaults.string(forKey: workerSetupCacheSizeKey) ?? ""
    }

    func setJobCompletionNotificationsEnabled(_ enabled: Bool) {
        defaults.set(enabled, forKey: notificationsKey)
    }

    func setWorkerSetupDeviceName(_ value: String) {
        defaults.set(value, forKey: workerSetupDeviceNameKey)
    }

    func setWorkerSetupCacheSize(_ value: String) {
        defaults.set(value, forKey: workerSetupCacheSizeKey)
    }
}
