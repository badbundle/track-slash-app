import Foundation
import Testing
@testable import TrackslashKit

final class StubProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var handler: (@Sendable (URLRequest) -> (Int, String))?
    nonisolated(unsafe) static var lastRequest: URLRequest?
    nonisolated(unsafe) static var lastBody: Data?

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        Self.lastRequest = request
        if let stream = request.httpBodyStream {
            stream.open()
            var data = Data()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let n = stream.read(&buffer, maxLength: buffer.count)
                if n <= 0 { break }
                data.append(buffer, count: n)
            }
            Self.lastBody = data
        } else {
            Self.lastBody = request.httpBody
        }
        let (status, body) = Self.handler!(request)
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: nil)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

func stubbedSession() -> URLSession {
    let config = URLSessionConfiguration.ephemeral
    config.protocolClasses = [StubProtocol.self]
    return URLSession(configuration: config)
}

let formJSON = """
{"id":"11111111-1111-1111-1111-111111111111","owner_username":"badbundle","project_key":"TRACK","ref":"form-5",
 "title":"Setup","description":"d","origin_label":"Claude","status":"open","comment":"",
 "url":"https://trackslash.com/badbundle/projects/TRACK/forms/form-5",
 "definition":{"sections":[{"questions":[
   {"id":"signin","type":"single_choice","label":"How?","options":[{"id":"a","label":"A"},{"id":"b","label":"B"}],"allow_other":true,"default":{"selected":["a"]}},
   {"id":"many","type":"multi_choice","label":"Which?","required":false,"options":[{"id":"x","label":"X"},{"id":"y","label":"Y"}]},
   {"id":"ok","type":"confirm","label":"Sure?"},
   {"id":"name","type":"text","label":"Name","multiline":true},
   {"id":"shot","type":"file","label":"Screenshot"}]}]},
 "answers":{"ok":{"checked":true,"note":"yes"}},
 "responses":[{"id":"shot","answered":false,"files":[]}]}
"""

@Suite(.serialized) struct KitTests {
    @Test func decodesFormAndBuildsDraft() throws {
        let form = try APIClient.decoder.decode(TrackForm.self, from: Data(formJSON.utf8))
        #expect(form.ref == "form-5" && form.isOpen)
        #expect(form.definition.questions.count == 5)
        var draft = AnswerDraft(form: form)
        // Default is filled in, saved answer is kept.
        #expect(draft.answer(for: form.definition.questions[0]).selected == ["a"])
        #expect(draft.answers["ok"]?.note == "yes")
        let missing = draft.missingRequired(in: form).map(\.id)
        #expect(missing == ["name", "shot"])
        #expect(draft.blockedByFiles(in: form).map(\.id) == ["shot"])
        draft.setText("  ", in: form.definition.questions[3])
        #expect(!draft.isAnswered(form.definition.questions[3], in: form))
        draft.setText("Brad", in: form.definition.questions[3])
        #expect(draft.isAnswered(form.definition.questions[3], in: form))
    }

    @Test func choiceEditing() throws {
        let form = try APIClient.decoder.decode(TrackForm.self, from: Data(formJSON.utf8))
        let single = form.definition.questions[0]
        let multi = form.definition.questions[1]
        var draft = AnswerDraft(form: form)
        draft.select("b", in: single)
        #expect(draft.answer(for: single).selected == ["b"])
        draft.select("other", in: single)
        #expect(!draft.isAnswered(single, in: form))
        draft.setOther("custom", in: single)
        #expect(draft.isAnswered(single, in: form))
        draft.select("a", in: single)
        #expect(draft.answer(for: single).other == nil)
        draft.select("x", in: multi)
        draft.select("y", in: multi)
        draft.select("x", in: multi)
        #expect(draft.answer(for: multi).selected == ["y"])
        draft.setChecked(false, in: form.definition.questions[2])
        #expect(!draft.isAnswered(form.definition.questions[2], in: form))
        draft.setNote("hm", in: form.definition.questions[2])
        #expect(draft.answers["ok"]?.note == "hm")
    }

    @Test func locationFromPath() {
        let want = FormLocation(owner: "badbundle", projectKey: "TRACK", ref: "form-5")
        #expect(FormLocation(path: "/badbundle/projects/TRACK/forms/form-5") == want)
        #expect(FormLocation(path: "https://trackslash.com/badbundle/projects/TRACK/forms/form-5") == want)
        #expect(FormLocation(path: "/badbundle/projects/TRACK/issues/TRACK-5") == nil)
        #expect(FormLocation(path: "/badbundle/projects/TRACK/forms/other") == nil)
        #expect(FormLocation(path: "") == nil)
    }

    @Test func normalizesServerURL() throws {
        #expect(try APIClient.normalizeServerURL(" trackslash.com/ ").absoluteString == "https://trackslash.com")
        #expect(try APIClient.normalizeServerURL("http://localhost:8080").absoluteString == "http://localhost:8080")
        #expect(throws: APIError.invalidServerURL) { try APIClient.normalizeServerURL("ftp://x.com") }
        #expect(throws: APIError.invalidServerURL) { try APIClient.normalizeServerURL("") }
    }

    @Test func signInAndInbox() async throws {
        StubProtocol.handler = { request in
            if request.url?.path == "/api/v1/session" { return (201, #"{"token":"secret","id":"x"}"#) }
            return (200, """
            {"items":[{"id":"11111111-1111-1111-1111-111111111111","owner_username":"o","project_key":"K","ref":"form-1",
            "title":"T","status":"open","origin_label":"","question_count":2,"required_count":2,"required_answered":1,
            "created_at":"2026-11-21T13:38:41.31045Z"}],"next_cursor":"abc"}
            """)
        }
        let server = URL(string: "https://example.test")!
        let token = try await APIClient.signIn(server: server, username: "u", password: "p", session: stubbedSession())
        #expect(token == "secret")
        let body = try #require(StubProtocol.lastBody)
        #expect(String(decoding: body, as: UTF8.self).contains("\"username\""))

        let client = APIClient(baseURL: server, token: token, session: stubbedSession())
        let page = try await client.inbox(state: .done, cursor: "c")
        #expect(page.items.count == 1 && page.nextCursor == "abc")
        #expect(page.items[0].location.ref == "form-1")
        let request = try #require(StubProtocol.lastRequest)
        #expect(request.value(forHTTPHeaderField: "Authorization") == "Bearer secret")
        #expect(request.url?.query?.contains("state=done") == true && request.url?.query?.contains("cursor=c") == true)
    }

    @Test func saveSubmitAndDevices() async throws {
        StubProtocol.handler = { request in
            let path = request.url?.path ?? ""
            if path.hasSuffix("/me/devices") { return (200, #"{"registered":true}"#) }
            return (200, formJSON)
        }
        let client = APIClient(baseURL: URL(string: "https://example.test")!, token: "t", session: stubbedSession())
        let form = try await client.form(FormLocation(owner: "badbundle", projectKey: "TRACK", ref: "form-5"))
        let draft = AnswerDraft(form: form)
        _ = try await client.save(form.location, draft: draft)
        #expect(StubProtocol.lastRequest?.httpMethod == "PUT")
        #expect(StubProtocol.lastRequest?.url?.path == "/api/v1/badbundle/projects/TRACK/forms/form-5/answers")
        let sentBody = try #require(StubProtocol.lastBody)
        let sent = try #require(try JSONSerialization.jsonObject(with: sentBody) as? [String: Any])
        #expect((sent["answers"] as? [String: Any])?["signin"] != nil)
        _ = try await client.submit(form.location)
        #expect(StubProtocol.lastRequest?.url?.path.hasSuffix("/submit") == true)
        try await client.registerDevice(token: "ab", name: "iPhone")
        #expect(StubProtocol.lastRequest?.httpMethod == "PUT")
        try await client.unregisterDevice(token: "ab")
        #expect(StubProtocol.lastRequest?.httpMethod == "DELETE")
    }

    @Test func errors() async throws {
        let client = APIClient(baseURL: URL(string: "https://example.test")!, token: "t", session: stubbedSession())
        let location = FormLocation(owner: "o", projectKey: "K", ref: "form-1")
        StubProtocol.handler = { _ in (401, #"{"error":"unauthorized"}"#) }
        await #expect(throws: APIError.unauthorized) { try await client.form(location) }
        StubProtocol.handler = { _ in (400, #"{"error":"Answer every required question"}"#) }
        await #expect(throws: APIError.server(status: 400, message: "Answer every required question")) { try await client.submit(location) }
        StubProtocol.handler = { _ in (502, "<html>") }
        await #expect(throws: APIError.server(status: 502, message: "Request failed (502)")) { try await client.form(location) }
        StubProtocol.handler = { _ in (200, "<html>") }
        await #expect(throws: APIError.transport("Unexpected response from the server")) { try await client.form(location) }
        // Wrong password is a 401 with no token: a server message, not a sign-out.
        StubProtocol.handler = { _ in (401, #"{"error":"invalid credentials"}"#) }
        await #expect(throws: APIError.server(status: 401, message: "invalid credentials")) {
            try await APIClient.signIn(server: URL(string: "https://example.test")!, username: "u", password: "x", session: stubbedSession())
        }
        #expect(APIError.unauthorized.errorDescription != nil)
    }
}
