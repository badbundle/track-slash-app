import Foundation
import Testing
@testable import TrackslashKit

extension KitTests {
    @Test func base64URLRoundTrip() {
        let bytes = Data([0xfb, 0xff, 0x00, 0x3e, 0x3f])
        #expect(bytes.base64URLEncodedString() == "-_8APj8")
        #expect(Data(base64URL: "-_8APj8") == bytes)
        #expect(Data(base64URL: "!!") == nil)
    }

    @Test func passkeySignIn() async throws {
        StubProtocol.handler = { request in
            if request.url?.path == "/api/v1/session/passkey/options" {
                return (200, """
                {"ceremony_id":"22222222-2222-2222-2222-222222222222",
                 "publicKey":{"challenge":"AQID","timeout":300000,"rpId":"trackslash.com","userVerification":"required"}}
                """)
            }
            return (201, #"{"token":"passkey-token"}"#)
        }
        let server = URL(string: "https://trackslash.com")!
        let challenge = try await APIClient.passkeyChallenge(server: server, session: stubbedSession())
        #expect(StubProtocol.lastRequest?.httpMethod == "POST")
        #expect(challenge.relyingPartyID == "trackslash.com")
        #expect(challenge.challengeData == Data([1, 2, 3]))

        let assertion = PasskeyAssertion(
            credentialID: Data([0xfb, 0xff]), clientDataJSON: Data("{}".utf8),
            authenticatorData: Data([9]), signature: Data([8]), userID: Data([7]))
        let token = try await APIClient.finishPasskeySignIn(
            server: server, ceremonyID: challenge.ceremonyId, assertion: assertion, session: stubbedSession())
        #expect(token == "passkey-token")
        #expect(StubProtocol.lastRequest?.url?.path == "/api/v1/session/passkey")

        let body = try #require(StubProtocol.lastBody)
        let json = try #require(try JSONSerialization.jsonObject(with: body) as? [String: Any])
        #expect(json["ceremony_id"] as? String == "22222222-2222-2222-2222-222222222222")
        let credential = try #require(json["credential"] as? [String: Any])
        #expect(credential["id"] as? String == "-_8" && credential["rawId"] as? String == "-_8")
        #expect(credential["type"] as? String == "public-key")
        let response = try #require(credential["response"] as? [String: Any])
        #expect(response["clientDataJSON"] as? String == "e30")
        #expect(response["authenticatorData"] as? String == "CQ")
        #expect(response["signature"] as? String == "CA")
        #expect(response["userHandle"] as? String == "Bw")
    }
}
