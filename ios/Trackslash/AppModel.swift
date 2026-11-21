import AuthenticationServices
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

    /// Must match the webcredentials entries in Trackslash.entitlements.
    private static let passkeyDomains: Set<String> = ["trackslash.com"]

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
        await finishSignIn(server: url, token: token)
    }

    /// Signs in with any passkey the person has for the site. iOS only offers
    /// passkeys for domains the app lists in its associated domains, so this
    /// works for trackslash.com and not for other servers.
    func signInWithPasskey(serverText: String) async throws {
        let url = try APIClient.normalizeServerURL(serverText)
        guard Self.passkeyDomains.contains(url.host() ?? "") else {
            throw APIError.transport("Passkeys in the app work with trackslash.com only. Sign in with your password.")
        }
        let challenge = try await APIClient.passkeyChallenge(server: url)
        do {
            let assertion = try await PasskeyAuthorizer().assert(challenge)
            let token = try await APIClient.finishPasskeySignIn(server: url, ceremonyID: challenge.ceremonyId, assertion: assertion)
            await finishSignIn(server: url, token: token)
        } catch let error as ASAuthorizationError where error.code == .canceled {
            throw CancellationError()
        }
    }

    private func finishSignIn(server url: URL, token: String) async {
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
