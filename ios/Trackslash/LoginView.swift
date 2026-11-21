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
                Section("Account") {
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
                        if busy { ProgressView() } else { Text("Sign in") }
                    }
                    .disabled(busy || username.isEmpty || password.isEmpty)
                } footer: {
                    Text("Accounts that only use passkeys need a password set on the website first.")
                }
            }
            .navigationTitle("Trackslash")
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
