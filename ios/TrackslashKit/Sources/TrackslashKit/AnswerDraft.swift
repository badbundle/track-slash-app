import Foundation

/// The answers a person is filling in on a form, before they are saved.
public struct AnswerDraft: Equatable, Sendable {
    public var answers: [String: FormAnswer]
    public var comment: String

    /// Starts from what the form already holds, and falls back to the answer
    /// each question suggests, so the person only has to confirm.
    public init(form: TrackForm) {
        var answers = form.answers
        for question in form.definition.questions where answers[question.id] == nil {
            if let suggested = question.default { answers[question.id] = FormAnswer(suggested) }
        }
        self.answers = answers
        self.comment = form.comment
    }

    public func answer(for question: FormQuestion) -> FormAnswer { answers[question.id] ?? FormAnswer() }

    public mutating func select(_ optionID: String, in question: FormQuestion) {
        var answer = answer(for: question)
        var selected = answer.selected ?? []
        if question.type == .singleChoice {
            selected = [optionID]
        } else if let index = selected.firstIndex(of: optionID) {
            selected.remove(at: index)
        } else {
            selected.append(optionID)
        }
        if !selected.contains(Self.otherID) { answer.other = nil }
        answer.selected = selected
        answers[question.id] = answer
    }

    public mutating func setOther(_ text: String, in question: FormQuestion) {
        var answer = answer(for: question)
        answer.other = text
        answers[question.id] = answer
    }

    public mutating func setChecked(_ checked: Bool, in question: FormQuestion) {
        var answer = answer(for: question)
        answer.checked = checked
        answers[question.id] = answer
    }

    public mutating func setText(_ text: String, in question: FormQuestion) {
        var answer = answer(for: question)
        answer.text = text
        answers[question.id] = answer
    }

    public mutating func setNote(_ note: String, in question: FormQuestion) {
        var answer = answer(for: question)
        answer.note = note
        answers[question.id] = answer
    }

    /// The id the server uses for a choice question's "Other" option.
    public static let otherID = "other"

    /// Whether a question has an answer a submit would accept. A file
    /// question is answered only on the server's say-so, since the app can't
    /// attach files.
    public func isAnswered(_ question: FormQuestion, in form: TrackForm) -> Bool {
        let answer = answer(for: question)
        switch question.type {
        case .singleChoice, .multiChoice:
            let selected = answer.selected ?? []
            if selected.isEmpty { return false }
            if selected.contains(Self.otherID) {
                return !(answer.other ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            }
            return true
        case .confirm:
            return answer.checked == true
        case .text:
            return !(answer.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        case .file:
            return form.responses.first { $0.id == question.id }?.answered == true
        }
    }

    /// Required questions still unanswered, in form order.
    public func missingRequired(in form: TrackForm) -> [FormQuestion] {
        form.definition.questions.filter { $0.isRequired && !isAnswered($0, in: form) }
    }

    /// Whether the app can finish the form: every missing question is one the
    /// app can answer.
    public func blockedByFiles(in form: TrackForm) -> [FormQuestion] {
        missingRequired(in: form).filter { $0.type == .file }
    }
}
