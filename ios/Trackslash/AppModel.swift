import Observation
import SwiftUI
import TrackslashKit
import UIKit
import UserNotifications

@MainActor @Observable
final class AppModel {
    private(set) var server: URL?
    private(set) var token: String?
    private(set) var deviceToken: String?
    /// A form to show now, set when a notification is tapped.
    var openForm: FormLocation?

    static let shared = AppModel()

    private static let serverKey = "server"

    init() {
        if let text = UserDefaults.standard.string(forKey: Self.serverKey) { server = URL(string: text) }
        token = Keychain.read()
    }

    var isSignedIn: Bool { server != nil && token != nil }

    var client: APIClient? {
        guard let server, let token else { return nil }
        return APIClient(baseURL: server, token: token)
    }

    func signIn(serverText: String, username: String, password: String) async throws {
        let url = try APIClient.normalizeServerURL(serverText)
        let token = try await APIClient.signIn(server: url, username: username, password: password)
        Keychain.write(token)
        UserDefaults.standard.set(url.absoluteString, forKey: Self.serverKey)
        self.server = url
        self.token = token
        await requestNotifications()
        await registerDevice()
    }

    func signOut() async {
        if let client, let deviceToken { try? await client.unregisterDevice(token: deviceToken) }
        Keychain.delete()
        token = nil
    }

    /// Call when any request says the token is no longer good.
    func handle(_ error: Error) {
        if let api = error as? APIError, api == .unauthorized {
            Keychain.delete()
            token = nil
        }
    }

    func requestNotifications() async {
        let granted = (try? await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .badge, .sound])) ?? false
        if granted { UIApplication.shared.registerForRemoteNotifications() }
    }

    func didRegister(deviceToken data: Data) {
        deviceToken = data.map { String(format: "%02x", $0) }.joined()
        Task { await registerDevice() }
    }

    /// Tells the server about this phone. Runs on every launch, since APNs may
    /// hand out a new token, and the server moves a token to whoever sent it
    /// last.
    func registerDevice() async {
        guard let client, let deviceToken else { return }
        do {
            try await client.registerDevice(token: deviceToken, name: UIDevice.current.name)
        } catch {
            handle(error)
        }
    }

    func didOpenNotification(path: String?) {
        guard let path, let location = FormLocation(path: path) else { return }
        openForm = location
    }
}
