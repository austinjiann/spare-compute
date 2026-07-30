import Foundation

enum DeviceCapability: String, CaseIterable, Codable, Identifiable, Sendable {
    case builds
    case tests
    case docker
    case ai
    case video
    case shell

    var id: String { rawValue }

    var title: String {
        switch self {
        case .builds:
            return "Builds"
        case .tests:
            return "Tests"
        case .docker:
            return "Docker"
        case .ai:
            return "AI"
        case .video:
            return "Video"
        case .shell:
            return "Commands"
        }
    }

    var systemImage: String {
        switch self {
        case .builds:
            return "hammer"
        case .tests:
            return "checkmark.seal"
        case .docker:
            return "shippingbox"
        case .ai:
            return "sparkles"
        case .video:
            return "film"
        case .shell:
            return "terminal"
        }
    }

    static let defaultLocal: Set<DeviceCapability> = [
        .builds,
        .tests,
        .docker,
        .shell,
    ]

    static let defaultWorker: Set<DeviceCapability> = [
        .builds,
        .tests,
        .docker,
    ]
}
