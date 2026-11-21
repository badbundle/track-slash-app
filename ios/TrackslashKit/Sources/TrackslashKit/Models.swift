import Foundation

public enum FormStatus: String, Codable, Sendable {
    case open, submitted, acknowledged, cancelled
}

public enum QuestionType: String, Codable, Sendable {
    case singleChoice = "single_choice"
    case multiChoice = "multi_choice"
    case confirm, text, file
}

/// A row in the Inbox: a form without its questions.
public struct FormSummary: Codable, Identifiable, Hashable, Sendable {
    public let id: UUID
    public let ownerUsername: String
    public let projectKey: String
    public let ref: String
    public let title: String
    public let status: FormStatus
    public let originLabel: String
    public let questionCount: Int
    public let requiredCount: Int
    public let requiredAnswered: Int
    public let createdAt: Date

    public init(
        id: UUID = UUID(), ownerUsername: String, projectKey: String, ref: String, title: String,
        status: FormStatus = .open, originLabel: String = "", questionCount: Int = 0,
        requiredCount: Int = 0, requiredAnswered: Int = 0, createdAt: Date = Date()
    ) {
        self.id = id
        self.ownerUsername = ownerUsername
        self.projectKey = projectKey
        self.ref = ref
        self.title = title
        self.status = status
        self.originLabel = originLabel
        self.questionCount = questionCount
        self.requiredCount = requiredCount
        self.requiredAnswered = requiredAnswered
        self.createdAt = createdAt
    }

    public var location: FormLocation {
        FormLocation(owner: ownerUsername, projectKey: projectKey, ref: ref)
    }
}

/// Where a form lives on the server.
public struct FormLocation: Hashable, Sendable {
    public let owner: String
    public let projectKey: String
    public let ref: String

    public init(owner: String, projectKey: String, ref: String) {
        self.owner = owner
        self.projectKey = projectKey
        self.ref = ref
    }

    /// Reads a form's location from its page path, as a push notification
    /// carries it: `/owner/projects/KEY/forms/form-5`, with or without the
    /// site's origin in front. Any other page is not a form.
    public init?(path: String) {
        var path = path
        if let url = URL(string: path), url.scheme != nil { path = url.path }
        let parts = path.split(separator: "/").map(String.init)
        guard parts.count == 5, parts[1] == "projects", parts[3] == "forms", !parts[0].isEmpty,
              !parts[2].isEmpty, parts[4].hasPrefix("form-")
        else { return nil }
        self.init(owner: parts[0], projectKey: parts[2], ref: parts[4])
    }
}

public struct FormOption: Codable, Identifiable, Hashable, Sendable {
    public let id: String
    public let label: String
    public let description: String?

    public init(id: String, label: String, description: String? = nil) {
        self.id = id
        self.label = label
        self.description = description
    }
}

/// The value of an answer, or of the answer a question suggests.
public struct FormValue: Codable, Hashable, Sendable {
    public var selected: [String]?
    public var other: String?
    public var checked: Bool?
    public var text: String?

    public init(selected: [String]? = nil, other: String? = nil, checked: Bool? = nil, text: String? = nil) {
        self.selected = selected
        self.other = other
        self.checked = checked
        self.text = text
    }
}

public struct FormAnswer: Codable, Hashable, Sendable {
    public var selected: [String]?
    public var other: String?
    public var checked: Bool?
    public var text: String?
    public var note: String?

    public init(
        selected: [String]? = nil, other: String? = nil, checked: Bool? = nil, text: String? = nil,
        note: String? = nil
    ) {
        self.selected = selected
        self.other = other
        self.checked = checked
        self.text = text
        self.note = note
    }

    public init(_ value: FormValue) {
        self.init(selected: value.selected, other: value.other, checked: value.checked, text: value.text)
    }
}

public struct FormQuestion: Codable, Identifiable, Hashable, Sendable {
    public let id: String
    public let type: QuestionType
    public let label: String
    public let help: String?
    public let required: Bool?
    public let options: [FormOption]?
    public let allowOther: Bool?
    public let multiline: Bool?
    public let placeholder: String?
    public let `default`: FormValue?

    public init(
        id: String, type: QuestionType, label: String, help: String? = nil, required: Bool? = nil,
        options: [FormOption]? = nil, allowOther: Bool? = nil, multiline: Bool? = nil,
        placeholder: String? = nil, default: FormValue? = nil
    ) {
        self.id = id
        self.type = type
        self.label = label
        self.help = help
        self.required = required
        self.options = options
        self.allowOther = allowOther
        self.multiline = multiline
        self.placeholder = placeholder
        self.default = `default`
    }

    /// Questions are required unless the form says otherwise.
    public var isRequired: Bool { required ?? true }
}

public struct FormSection: Codable, Hashable, Sendable {
    public let title: String?
    public let description: String?
    public let questions: [FormQuestion]

    public init(title: String? = nil, description: String? = nil, questions: [FormQuestion]) {
        self.title = title
        self.description = description
        self.questions = questions
    }
}

public struct FormDefinition: Codable, Hashable, Sendable {
    public let sections: [FormSection]

    public init(sections: [FormSection]) { self.sections = sections }

    public var questions: [FormQuestion] { sections.flatMap(\.questions) }
}

/// The server's view of one question's answer. Only what the app needs: a
/// file question counts as answered once the person has attached a file on
/// the web, which the app can't do.
public struct FormResponse: Codable, Hashable, Sendable {
    public let id: String
    public let answered: Bool

    public init(id: String, answered: Bool) {
        self.id = id
        self.answered = answered
    }
}

public struct TrackForm: Codable, Identifiable, Hashable, Sendable {
    public let id: UUID
    public let ownerUsername: String
    public let projectKey: String
    public let ref: String
    public let title: String
    public let description: String
    public let originLabel: String
    public let status: FormStatus
    public let definition: FormDefinition
    public let answers: [String: FormAnswer]
    public let comment: String
    public let url: String
    public let responses: [FormResponse]

    public init(
        id: UUID = UUID(), ownerUsername: String, projectKey: String, ref: String, title: String,
        description: String = "", originLabel: String = "", status: FormStatus = .open,
        definition: FormDefinition, answers: [String: FormAnswer] = [:], comment: String = "",
        url: String = "", responses: [FormResponse] = []
    ) {
        self.id = id
        self.ownerUsername = ownerUsername
        self.projectKey = projectKey
        self.ref = ref
        self.title = title
        self.description = description
        self.originLabel = originLabel
        self.status = status
        self.definition = definition
        self.answers = answers
        self.comment = comment
        self.url = url
        self.responses = responses
    }

    public var location: FormLocation {
        FormLocation(owner: ownerUsername, projectKey: projectKey, ref: ref)
    }

    /// Whether the person can still change the answers.
    public var isOpen: Bool { status == .open }
}

public struct Page<Item: Codable & Sendable>: Codable, Sendable {
    public let items: [Item]
    public let nextCursor: String?
}
