import SwiftUI

struct LoginView: View {
    @Environment(AppModel.self) private var model
    @State private var server = "trackslash.com"
    @State private var username = ""
    @State private var password = ""
    @State private var error: String?
    @State private var busy = false

    var body: some View {
        NavigationStack {
            Form {
                Section("Server") {
                    TextField("trackslash.com", text: $server)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .keyboardType(.URL)
                }
                Section {
                    Button(action: signInWithPasskey) {
                        Label("Sign in with a passkey", systemImage: "person.badge.key")
                    }
                    .disabled(busy)
                } footer: {
                    Text("Uses the passkey you sign in to the website with. No username needed.")
                }
                Section("Or with a password") {
                    TextField("Username", text: $username)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .textContentType(.username)
                    SecureField("Password", text: $password)
                        .textContentType(.password)
                }
                if let error {
                    Section { Text(error).foregroundStyle(.red) }
                }
                Section {
                    Button(action: signIn) {
                        if busy { ProgressView() } else { Text("Sign in with password") }
                    }
                    .disabled(busy || username.isEmpty || password.isEmpty)
                }
            }
            .navigationTitle("Trackslash")
        }
    }

    private func signInWithPasskey() {
        busy = true
        error = nil
        Task {
            defer { busy = false }
            do {
                try await model.signInWithPasskey(serverText: server)
            } catch is CancellationError {
                // The person closed the passkey sheet.
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    private func signIn() {
        busy = true
        error = nil
        Task {
            defer { busy = false }
            do {
                try await model.signIn(serverText: server, username: username, password: password)
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}
