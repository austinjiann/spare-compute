import Foundation
@preconcurrency import UserNotifications

@MainActor
protocol JobCompletionNotifying {
    func notifyJobFinished(title: String, body: String) async
}

struct SystemJobCompletionNotifier: JobCompletionNotifying {
    func notifyJobFinished(title: String, body: String) async {
        let center = UNUserNotificationCenter.current()
        guard await authorizationAllowsNotification(center) else { return }

        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.sound = .default

        let request = UNNotificationRequest(
            identifier: "computehop-job-\(UUID().uuidString)",
            content: content,
            trigger: nil
        )
        await add(request, to: center)
    }

    private func authorizationAllowsNotification(_ center: UNUserNotificationCenter) async -> Bool {
        await withCheckedContinuation { continuation in
            center.getNotificationSettings { settings in
                switch settings.authorizationStatus {
                case .authorized, .provisional, .ephemeral:
                    continuation.resume(returning: true)
                case .denied:
                    continuation.resume(returning: false)
                case .notDetermined:
                    center.requestAuthorization(options: [.alert, .sound]) { granted, _ in
                        continuation.resume(returning: granted)
                    }
                @unknown default:
                    continuation.resume(returning: false)
                }
            }
        }
    }

    private func add(_ request: UNNotificationRequest, to center: UNUserNotificationCenter) async {
        await withCheckedContinuation { continuation in
            center.add(request) { _ in
                continuation.resume()
            }
        }
    }
}
