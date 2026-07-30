import AppKit
import Foundation

@MainActor
protocol ControlCenterLaunching {
    func openControlCenter() throws
}

enum ControlCenterLaunchError: LocalizedError {
    case notFound
    case openFailed(URL)

    var errorDescription: String? {
        switch self {
        case .notFound:
            return "ComputeHop Control Center was not found. Package or install the Control Center app, then try again."
        case .openFailed(let url):
            return "Could not open ComputeHop Control Center at \(url.path)."
        }
    }
}

struct SystemControlCenterLauncher: ControlCenterLaunching {
    private static let bundleIdentifier = "com.computehop.controlcenter"
    private static let appName = "ComputeHop Control Center.app"

    func openControlCenter() throws {
        if let url = NSWorkspace.shared.urlForApplication(withBundleIdentifier: Self.bundleIdentifier) {
            try open(url)
            return
        }

        for url in candidateAppURLs() where FileManager.default.fileExists(atPath: url.path) {
            try open(url)
            return
        }

        throw ControlCenterLaunchError.notFound
    }

    private func open(_ url: URL) throws {
        guard NSWorkspace.shared.open(url) else {
            throw ControlCenterLaunchError.openFailed(url)
        }
    }

    private func candidateAppURLs() -> [URL] {
        let home = FileManager.default.homeDirectoryForCurrentUser
        let currentDirectory = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
        let appBundleFolder = Bundle.main.bundleURL.deletingLastPathComponent()

        return [
            URL(fileURLWithPath: "/Applications").appendingPathComponent(Self.appName),
            home.appendingPathComponent("Applications").appendingPathComponent(Self.appName),
            appBundleFolder.appendingPathComponent(Self.appName),
            currentDirectory
                .appendingPathComponent("apps/control-center/.out/ComputeHop Control Center-darwin-arm64")
                .appendingPathComponent(Self.appName),
        ]
    }
}
