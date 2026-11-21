import Foundation

/// What the server sends to start a passkey sign-in: a one-time challenge and
/// the site (relying party) the passkey must belong to.
public struct PasskeyChallenge: Decodable, Sendable {
    public let ceremonyId: UUID
    public let publicKey: PublicKey

    public struct PublicKey: Decodable, Sendable {
        public let challenge: String
        public let rpId: String
    }

    /// The challenge's bytes, which the authenticator signs.
    public var challengeData: Data? { Data(base64URL: publicKey.challenge) }
    public var relyingPartyID: String { publicKey.rpId }
}

/// The signed answer from a passkey, as AuthenticationServices returns it.
public struct PasskeyAssertion: Sendable {
    public let credentialID: Data
    public let clientDataJSON: Data
    public let authenticatorData: Data
    public let signature: Data
    public let userID: Data

    public init(credentialID: Data, clientDataJSON: Data, authenticatorData: Data, signature: Data, userID: Data) {
        self.credentialID = credentialID
        self.clientDataJSON = clientDataJSON
        self.authenticatorData = authenticatorData
        self.signature = signature
        self.userID = userID
    }
}

/// The finish request: the assertion in the WebAuthn JSON shape a browser
/// sends, so the server checks it exactly as it does the website's.
struct PasskeyFinishBody: Encodable {
    let ceremonyID: String
    let credential: Credential

    struct Credential: Encodable {
        let id: String
        let rawId: String
        let type = "public-key"
        let authenticatorAttachment = "platform"
        let response: Response
    }

    struct Response: Encodable {
        let clientDataJSON: String
        let authenticatorData: String
        let signature: String
        let userHandle: String
    }

    enum CodingKeys: String, CodingKey {
        case ceremonyID = "ceremony_id"
        case credential
    }

    init(ceremonyID: UUID, assertion: PasskeyAssertion) {
        let id = assertion.credentialID.base64URLEncodedString()
        self.ceremonyID = ceremonyID.uuidString.lowercased()
        self.credential = Credential(
            id: id, rawId: id,
            response: Response(
                clientDataJSON: assertion.clientDataJSON.base64URLEncodedString(),
                authenticatorData: assertion.authenticatorData.base64URLEncodedString(),
                signature: assertion.signature.base64URLEncodedString(),
                userHandle: assertion.userID.base64URLEncodedString()
            )
        )
    }
}

extension APIClient {
    /// Starts a passkey sign-in. Any passkey the person has for the site may
    /// answer, so no username is needed.
    public static func passkeyChallenge(server: URL, session: URLSession = .shared) async throws -> PasskeyChallenge {
        try await APIClient(baseURL: server, session: session).send("POST", "api/v1/session/passkey/options")
    }

    /// Finishes a passkey sign-in and returns the session token.
    public static func finishPasskeySignIn(
        server: URL, ceremonyID: UUID, assertion: PasskeyAssertion, session: URLSession = .shared
    ) async throws -> String {
        let login: Login = try await APIClient(baseURL: server, session: session).send(
            "POST", "api/v1/session/passkey", body: PasskeyFinishBody(ceremonyID: ceremonyID, assertion: assertion))
        return login.token
    }
}

extension Data {
    init?(base64URL text: String) {
        var base64 = text.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        base64 += String(repeating: "=", count: (4 - base64.count % 4) % 4)
        self.init(base64Encoded: base64)
    }

    func base64URLEncodedString() -> String {
        base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }
}
