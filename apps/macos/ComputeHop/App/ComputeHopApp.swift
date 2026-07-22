import AppKit
import SwiftUI

@main
@MainActor
struct ComputeHopApp: App {
    @State private var model = AppModel()

    init() {
        NSApplication.shared.setActivationPolicy(.accessory)
    }

    var body: some Scene {
        MenuBarExtra {
            MenuContentView(model: model)
                .task { await model.refreshLoop() }
        } label: {
            Label(
                "ComputeHop",
                systemImage: model.isConnected ? "point.3.connected.trianglepath.dotted" : "exclamationmark.triangle"
            )
        }
        .menuBarExtraStyle(.window)
    }
}
