import SwiftUI
import TrackslashKit

struct FormView: View {
    @Environment(AppModel.self) private var model
    let location: FormLocation
    let onChange: () -> Void

    @State private var form: TrackForm?
    @State private var draft: AnswerDraft?
    @State private var error: String?
    @State private var busy = false
    @State private var notice: String?

    var body: some View {
        Group {
            if let form, let draftBinding = Binding($draft) {
                content(form, draftBinding)
            } else if let error {
                ContentUnavailableView("Can't open this form", systemImage: "exclamationmark.triangle", description: Text(error))
            } else {
                ProgressView()
            }
        }
        .navigationTitle(form?.title ?? location.ref)
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
    }

    @ViewBuilder
    private func content(_ form: TrackForm, _ draft: Binding<AnswerDraft>) -> some View {
        SwiftUI.Form {
            if !form.description.isEmpty || !form.originLabel.isEmpty {
                Section {
                    if !form.description.isEmpty { Text(.init(form.description)) }
                    if !form.originLabel.isEmpty {
                        Text("From \(form.originLabel)").font(.footnote).foregroundStyle(.secondary)
                    }
                }
            }
            ForEach(Array(form.definition.sections.enumerated()), id: \.offset) { _, section in
                Section {
                    if let description = section.description { Text(.init(description)) }
                    ForEach(section.questions) { question in
                        QuestionView(question: question, form: form, draft: draft)
                    }
                } header: {
                    if let title = section.title { Text(title) }
                }
            }
            if form.isOpen {
                Section("Comment") {
                    TextField("Anything else?", text: draft.comment, axis: .vertical)
                }
            }
            if let error { Section { Text(error).foregroundStyle(.red) } }
            if let notice { Section { Text(notice).foregroundStyle(.secondary) } }
            if form.isOpen {
                Section {
                    Button("Save") { Task { await save(draft.wrappedValue, submit: false) } }
                    Button("Submit answers") { Task { await save(draft.wrappedValue, submit: true) } }
                        .bold()
                } footer: {
                    footer(form, draft.wrappedValue)
                }
                .disabled(busy)
            } else {
                Section { Label(statusText(form.status), systemImage: "checkmark.circle") }
            }
        }
        .scrollDismissesKeyboard(.interactively)
    }

    @ViewBuilder
    private func footer(_ form: TrackForm, _ draft: AnswerDraft) -> some View {
        let missing = draft.missingRequired(in: form)
        if !missing.isEmpty {
            Text("Still to answer: " + missing.map(\.label).joined(separator: ", "))
        }
    }

    private func statusText(_ status: FormStatus) -> String {
        switch status {
        case .open: "Open"
        case .submitted: "Submitted"
        case .acknowledged: "Submitted and read"
        case .cancelled: "Cancelled"
        }
    }

    private func load() async {
        guard let client = model.client else { return }
        do {
            let loaded = try await client.form(location)
            form = loaded
            draft = AnswerDraft(form: loaded)
            error = nil
        } catch {
            model.handle(error)
            self.error = error.localizedDescription
        }
    }

    private func save(_ draft: AnswerDraft, submit: Bool) async {
        guard let client = model.client, let form else { return }
        busy = true
        defer { busy = false }
        error = nil
        notice = nil
        if submit {
            let blocked = draft.blockedByFiles(in: form)
            if !blocked.isEmpty {
                error = "\(blocked.map(\.label).joined(separator: ", ")) needs a file. Attach it on the website, then come back."
                return
            }
            let missing = draft.missingRequired(in: form)
            if !missing.isEmpty {
                error = "Answer every required question first."
                return
            }
        }
        do {
            var saved = try await client.save(location, draft: draft)
            if submit { saved = try await client.submit(location) }
            self.form = saved
            self.draft = AnswerDraft(form: saved)
            notice = submit ? "Submitted. Tell Claude it's filled out." : "Saved."
            onChange()
        } catch {
            model.handle(error)
            self.error = error.localizedDescription
        }
    }
}

private struct QuestionView: View {
    let question: FormQuestion
    let form: TrackForm
    @Binding var draft: AnswerDraft

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(question.label + (question.isRequired ? "" : " (optional)")).font(.headline)
            if let help = question.help { Text(.init(help)).font(.footnote).foregroundStyle(.secondary) }
            control
        }
        .padding(.vertical, 4)
        .disabled(!form.isOpen)
    }

    @ViewBuilder
    private var control: some View {
        let answer = draft.answer(for: question)
        switch question.type {
        case .singleChoice, .multiChoice:
            ForEach(question.options ?? []) { option in
                choiceRow(option.id, label: option.label, detail: option.description, selected: answer.selected ?? [])
            }
            if question.allowOther == true {
                choiceRow(AnswerDraft.otherID, label: "Other", detail: nil, selected: answer.selected ?? [])
                if (answer.selected ?? []).contains(AnswerDraft.otherID) {
                    TextField("Your answer", text: Binding(
                        get: { answer.other ?? "" }, set: { draft.setOther($0, in: question) }))
                        .textFieldStyle(.roundedBorder)
                }
            }
        case .confirm:
            Toggle("Yes", isOn: Binding(get: { answer.checked ?? false }, set: { draft.setChecked($0, in: question) }))
        case .text:
            TextField(
                question.placeholder ?? "Your answer",
                text: Binding(get: { answer.text ?? "" }, set: { draft.setText($0, in: question) }),
                axis: question.multiline == true ? .vertical : .horizontal
            )
            .lineLimit(question.multiline == true ? 3...10 : 1...1)
            .textFieldStyle(.roundedBorder)
        case .file:
            if draft.isAnswered(question, in: form) {
                Label("File attached", systemImage: "paperclip")
            } else if let url = URL(string: form.url) {
                Link("Attach a file on the website", destination: url)
            }
        }
    }

    private func choiceRow(_ id: String, label: String, detail: String?, selected: [String]) -> some View {
        Button {
            draft.select(id, in: question)
        } label: {
            HStack(alignment: .top) {
                Image(systemName: selected.contains(id)
                    ? (question.type == .singleChoice ? "largecircle.fill.circle" : "checkmark.square.fill")
                    : (question.type == .singleChoice ? "circle" : "square"))
                VStack(alignment: .leading) {
                    Text(label)
                    if let detail { Text(detail).font(.caption).foregroundStyle(.secondary) }
                }
                .foregroundStyle(.primary)
                Spacer()
            }
        }
        .buttonStyle(.plain)
    }
}
