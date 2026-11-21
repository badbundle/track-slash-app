import Foundation

public enum APIError: Error, Equatable, LocalizedError, Sendable {
    /// The token is no longer good; the person has to sign in again.
    case unauthorized
    case server(status: Int, message: String)
    case invalidServerURL
    case transport(String)

    public var errorDescription: String? {
        switch self {
        case .unauthorized: "You have been signed out. Sign in again."
        case .server(_, let message): message
        case .invalidServerURL: "That is not a valid server address."
        case .transport(let message): message
        }
    }
}

/// Talks to a Trackslash server's REST API with one person's token.
public struct APIClient: Sendable {
    public let baseURL: URL
    public let token: String?
    private let session: URLSession

    public init(baseURL: URL, token: String? = nil, session: URLSession = .shared) {
        self.baseURL = baseURL
        self.token = token
        self.session = session
    }

    /// Turns what a person typed into a server URL. A bare host gets https.
    public static func normalizeServerURL(_ text: String) throws -> URL {
        var text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        while text.hasSuffix("/") { text.removeLast() }
        if !text.contains("://") { text = "https://" + text }
        guard let url = URL(string: text), let scheme = url.scheme, ["http", "https"].contains(scheme),
              let host = url.host, !host.isEmpty
        else { throw APIError.invalidServerURL }
        return url
    }

    // MARK: Sign-in

    public struct Login: Decodable, Sendable { public let token: String }

    public static func signIn(
        server: URL, username: String, password: String, session: URLSession = .shared
    ) async throws -> String {
        let client = APIClient(baseURL: server, session: session)
        let login: Login = try await client.send(
            "POST", "api/v1/session", body: ["username": username, "password": password])
        return login.token
    }

    // MARK: Forms

    public func inbox(state: InboxState = .todo, cursor: String? = nil) async throws -> Page<FormSummary> {
        var query = [URLQueryItem(name: "state", value: state.rawValue), URLQueryItem(name: "limit", value: "50")]
        if let cursor { query.append(URLQueryItem(name: "cursor", value: cursor)) }
        return try await send("GET", "api/v1/me/inbox", query: query)
    }

    public func form(_ location: FormLocation) async throws -> TrackForm {
        try await send("GET", Self.path(location))
    }

    public func save(_ location: FormLocation, draft: AnswerDraft) async throws -> TrackForm {
        struct Body: Encodable {
            let answers: [String: FormAnswer]
            let comment: String
        }
        return try await send("PUT", Self.path(location) + "/answers", body: Body(answers: draft.answers, comment: draft.comment))
    }

    public func submit(_ location: FormLocation) async throws -> TrackForm {
        try await send("POST", Self.path(location) + "/submit")
    }

    // MARK: Devices

    public func registerDevice(token: String, name: String) async throws {
        let _: Registered = try await send("PUT", "api/v1/me/devices", body: ["token": token, "name": name])
    }

    public func unregisterDevice(token: String) async throws {
        let _: Registered = try await send("DELETE", "api/v1/me/devices", body: ["token": token])
    }

    struct Registered: Decodable { let registered: Bool }
    struct Failure: Decodable { let error: String }

    // MARK: Plumbing

    static func path(_ location: FormLocation) -> String {
        "api/v1/\(location.owner)/projects/\(location.projectKey)/forms/\(location.ref)"
    }

    func send<Response: Decodable>(
        _ method: String, _ path: String, query: [URLQueryItem] = [], body: (some Encodable)? = nil as [String: String]?
    ) async throws -> Response {
        var components = URLComponents(url: baseURL.appendingPathComponent(path), resolvingAgainstBaseURL: false)
        if !query.isEmpty { components?.queryItems = query }
        guard let url = components?.url else { throw APIError.invalidServerURL }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if let token { request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        if let body {
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONEncoder().encode(body)
        }
        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch {
            throw APIError.transport(error.localizedDescription)
        }
        guard let http = response as? HTTPURLResponse else { throw APIError.transport("No response") }
        guard (200..<300).contains(http.statusCode) else {
            if http.statusCode == 401 && token != nil { throw APIError.unauthorized }
            let message = (try? JSONDecoder().decode(Failure.self, from: data))?.error ?? "Request failed (\(http.statusCode))"
            throw APIError.server(status: http.statusCode, message: message)
        }
        do {
            return try Self.decoder.decode(Response.self, from: data)
        } catch {
            throw APIError.transport("Unexpected response from the server")
        }
    }

    static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        decoder.dateDecodingStrategy = .custom { decoder in
            let text = try decoder.singleValueContainer().decode(String.self)
            let fractional = ISO8601DateFormatter()
            fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            let plain = ISO8601DateFormatter()
            if let date = fractional.date(from: text) ?? plain.date(from: text) { return date }
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "bad date \(text)"))
        }
        return decoder
    }()
}

public enum InboxState: String, Sendable {
    case todo, done
}
