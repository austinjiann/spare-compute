import Foundation

@MainActor
protocol AppSettingsStoring {
    var jobCompletionNotificationsEnabled: Bool { get }
    var workerSetupDeviceName: String { get }
    var workerSetupCacheSize: String { get }
    var vpsConnectivityDomain: String { get }
    var vpsTurnDomain: String { get }
    func setJobCompletionNotificationsEnabled(_ enabled: Bool)
    func setWorkerSetupDeviceName(_ value: String)
    func setWorkerSetupCacheSize(_ value: String)
    func setVPSConnectivityDomain(_ value: String)
    func setVPSTurnDomain(_ value: String)
}

final class UserDefaultsAppSettingsStore: AppSettingsStoring {
    private let defaults: UserDefaults
    private let notificationsKey = "jobCompletionNotificationsEnabled"
    private let workerSetupDeviceNameKey = "workerSetupDeviceName"
    private let workerSetupCacheSizeKey = "workerSetupCacheSize"
    private let vpsConnectivityDomainKey = "vpsConnectivityDomain"
    private let vpsTurnDomainKey = "vpsTurnDomain"

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

    var vpsConnectivityDomain: String {
        let value = defaults.string(forKey: vpsConnectivityDomainKey) ?? ""
        return value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "connect.example.com" : value
    }

    var vpsTurnDomain: String {
        let value = defaults.string(forKey: vpsTurnDomainKey) ?? ""
        return value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "turn.example.com" : value
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

    func setVPSConnectivityDomain(_ value: String) {
        defaults.set(value, forKey: vpsConnectivityDomainKey)
    }

    func setVPSTurnDomain(_ value: String) {
        defaults.set(value, forKey: vpsTurnDomainKey)
    }
}
