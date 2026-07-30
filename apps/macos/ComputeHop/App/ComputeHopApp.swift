import AppKit
import SwiftUI

@main
struct ComputeHopApp: App {
    @NSApplicationDelegateAdaptor(ComputeHopAppDelegate.self) private var appDelegate

    var body: some Scene {
        Settings {
            EmptyView()
        }
    }
}

@MainActor
final class ComputeHopAppDelegate: NSObject, NSApplicationDelegate {
    private var model: AppModel?
    private var statusItem: NSStatusItem?
    private var popover: NSPopover?
    private var refreshTask: Task<Void, Never>?

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApplication.shared.setActivationPolicy(.accessory)

        let model = AppModel()
        self.model = model

        let popover = NSPopover()
        popover.behavior = .transient
        popover.contentViewController = NSHostingController(rootView: MenuContentView(model: model))
        self.popover = popover

        let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let button = statusItem.button {
            button.image = NSImage(systemSymbolName: "cpu", accessibilityDescription: "ComputeHop")
            button.image?.isTemplate = true
            button.toolTip = "ComputeHop"
            button.target = self
            button.action = #selector(togglePopover(_:))
        }
        self.statusItem = statusItem

        refreshTask = Task {
            await model.refreshLoop()
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        refreshTask?.cancel()
    }

    @objc private func togglePopover(_ sender: Any?) {
        guard let popover, let button = statusItem?.button else { return }
        if popover.isShown {
            popover.performClose(sender)
            return
        }
        popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
    }
}
