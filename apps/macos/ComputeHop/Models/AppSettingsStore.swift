import Foundation

enum AppSettingDefaults {
    static let workerDeviceName = "Gaming PC"
    static let vpsConnectivityDomain = "connect.example.com"
    static let vpsTurnDomain = "turn.example.com"
}

@MainActor
protocol AppSettingsStoring {
    var jobCompletionNotificationsEnabled: Bool { get }
    var workerSetupDeviceName: String { get }
    var workerSetupCacheSize: String { get }
    var vpsConnectivityDomain: String { get }
    var vpsTurnDomain: String { get }
    var deviceCapabilities: [String: Set<DeviceCapability>] { get }
    func setJobCompletionNotificationsEnabled(_ enabled: Bool)
    func setWorkerSetupDeviceName(_ value: String)
    func setWorkerSetupCacheSize(_ value: String)
    func setVPSConnectivityDomain(_ value: String)
    func setVPSTurnDomain(_ value: String)
    func setDeviceCapabilities(_ capabilities: Set<DeviceCapability>, forDeviceID id: String)
}

final class UserDefaultsAppSettingsStore: AppSettingsStoring {
    private let defaults: UserDefaults
    private let notificationsKey = "jobCompletionNotificationsEnabled"
    private let workerSetupDeviceNameKey = "workerSetupDeviceName"
    private let workerSetupCacheSizeKey = "workerSetupCacheSize"
    private let vpsConnectivityDomainKey = "vpsConnectivityDomain"
    private let vpsTurnDomainKey = "vpsTurnDomain"
    private let deviceCapabilitiesKey = "deviceCapabilities"

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
        return value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            ? AppSettingDefaults.workerDeviceName
            : value
    }

    var workerSetupCacheSize: String {
        defaults.string(forKey: workerSetupCacheSizeKey) ?? ""
    }

    var vpsConnectivityDomain: String {
        let value = defaults.string(forKey: vpsConnectivityDomainKey) ?? ""
        return value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            ? AppSettingDefaults.vpsConnectivityDomain
            : value
    }

    var vpsTurnDomain: String {
        let value = defaults.string(forKey: vpsTurnDomainKey) ?? ""
        return value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            ? AppSettingDefaults.vpsTurnDomain
            : value
    }

    var deviceCapabilities: [String: Set<DeviceCapability>] {
        guard let data = defaults.data(forKey: deviceCapabilitiesKey),
              let decoded = try? JSONDecoder().decode([String: [DeviceCapability]].self, from: data)
        else {
            return [:]
        }
        return decoded.mapValues(Set.init)
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

    func setDeviceCapabilities(_ capabilities: Set<DeviceCapability>, forDeviceID id: String) {
        var current = deviceCapabilities
        current[id] = capabilities
        let encoded = current.mapValues { capabilities in
            capabilities.sorted { $0.rawValue < $1.rawValue }
        }
        if let data = try? JSONEncoder().encode(encoded) {
            defaults.set(data, forKey: deviceCapabilitiesKey)
        }
    }
}
