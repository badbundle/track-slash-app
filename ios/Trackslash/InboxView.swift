import SwiftUI
import TrackslashKit

struct InboxView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.scenePhase) private var scenePhase
    @State private var state: InboxState = .todo
    @State private var forms: [FormSummary] = []
    @State private var loaded = false
    @State private var error: String?
    @State private var path: [FormLocation] = []

    var body: some View {
        NavigationStack(path: $path) {
            List {
                Picker("Show", selection: $state) {
                    Text("To do").tag(InboxState.todo)
                    Text("Done").tag(InboxState.done)
                }
                .pickerStyle(.segmented)
                .listRowBackground(Color.clear)

                if let error {
                    Text(error).foregroundStyle(.red)
                }
                ForEach(forms) { form in
                    NavigationLink(value: form.location) { FormRow(form: form) }
                }
                if loaded && forms.isEmpty && error == nil {
                    ContentUnavailableView(
                        state == .todo ? "Nothing waiting" : "Nothing answered yet",
                        systemImage: "tray",
                        description: Text(state == .todo ? "Forms that need your answers show up here." : "")
                    )
                }
            }
            .navigationTitle("Inbox")
            .navigationDestination(for: FormLocation.self) { location in
                FormView(location: location) { Task { await load() } }
            }
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        if let server = model.server { Text(server.host() ?? server.absoluteString) }
                        Button("Sign out", role: .destructive) { Task { await model.signOut() } }
                    } label: { Image(systemName: "person.circle") }
                }
            }
            .refreshable { await load() }
            .task(id: state) { await load() }
            .onChange(of: scenePhase) { _, phase in
                if phase == .active { Task { await load() } }
            }
            .onChange(of: model.openForm) { _, location in
                guard let location else { return }
                model.openForm = nil
                path = [location]
            }
        }
    }

    private func load() async {
        guard let client = model.client else { return }
        do {
            forms = try await client.inbox(state: state).items
            error = nil
        } catch {
            model.handle(error)
            self.error = error.localizedDescription
        }
        loaded = true
        try? await UNUserNotificationCenter.current().setBadgeCount(state == .todo ? forms.count : 0)
    }
}

private struct FormRow: View {
    let form: FormSummary

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(form.title).font(.headline)
            HStack(spacing: 8) {
                Text("\(form.projectKey)").font(.caption.monospaced())
                if !form.originLabel.isEmpty { Text(form.originLabel).lineLimit(1) }
            }
            .font(.caption)
            .foregroundStyle(.secondary)
            if form.status == .open {
                Text("\(form.requiredAnswered) of \(form.requiredCount) required answered")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
    }
}
