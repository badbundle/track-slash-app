# Trackslash for iOS

A small iPhone app for the thing agents ask people most: answering a [form](../internal/model/form.go) and being told when one is waiting. It signs in with a passkey or a username and password, lists the Inbox (To do and Done), lets you answer every question type and submit, and registers for push notifications.

Files can't be attached from the app yet. A file question links to the form's page on the website, and the app won't submit until the file is there. Images, audio and video on a question are not shown yet either.

## Layout

- `TrackslashKit/`: Swift package with everything testable: the models, the API client, and the answer-draft logic (what counts as answered, what a submit still needs). Run `swift test` in that folder; it needs no simulator.
- `Trackslash/`: the SwiftUI app. `AppModel` holds the sign-in and push state, and the views are thin.
- `project.yml`: [XcodeGen](https://github.com/yonaskolb/XcodeGen) spec. `Trackslash.xcodeproj` is generated from it and committed; run `xcodegen generate` in this folder after changing the spec or adding a file.

The bundle ID is `com.badbundle.trackslash`. The token lives in the Keychain; the server address is the only thing kept in UserDefaults.

## Run it

```sh
cd ios
xcodebuild -project Trackslash.xcodeproj -scheme Trackslash \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' CODE_SIGNING_ALLOWED=NO build
```

Or open `Trackslash.xcodeproj` in Xcode. To try it against a dev server, enter `http://localhost:8080` (or your Mac's address) as the server: the app allows plain http to the local network only.

## Passkeys

The app signs in with the same passkeys as the website, through `POST /api/v1/session/passkey/options` and `POST /api/v1/session/passkey`. iOS only offers a site's passkeys to an app the site vouches for, so two things must match:

- The app's `com.apple.developer.associated-domains` entitlement lists `webcredentials:trackslash.com`. The App ID needs the Associated Domains capability in the developer portal.
- The server serves `/.well-known/apple-app-site-association` naming the app, which it does when `TRACK_SLASH_APPLE_APP_IDS=PQ7Z28BJ65.com.badbundle.trackslash` is set. Apple's CDN caches the file, so a change can take a while to reach phones.

Other servers can't use passkeys in this build, since the entitlement is fixed when the app is built; the app says so and points to the password. A build without signing (`CODE_SIGNING_ALLOWED=NO`) has no entitlements, so passkeys only work in a signed build.

## Notifications

The app asks for permission after sign-in, registers its APNs token with `PUT /api/v1/me/devices` on every launch, and removes it with `DELETE /api/v1/me/devices` on sign-out. The server sends through Apple when the `TRACK_SLASH_APNS_*` variables are set; see [DEPLOYMENT.md](../DEPLOYMENT.md#ios-app-push). Which events notify is the account's existing Notifications setting on the website (forms are on by default).

A notification carries the page it is about as `url`; tapping a form's notification opens that form.

Push only works on a real device. The simulator accepts `xcrun simctl push` for testing the tap handling, with a payload like `{"aps":{"alert":"Form waiting"},"url":"/owner/projects/KEY/forms/form-5"}`.

## Not done yet

- App Store listing, icon and TestFlight lane (Fastlane, as in the other Bad Bundle apps).
- Sign-in with an API token, and passkeys on servers other than trackslash.com.
- Media on questions, file answers, and per-question notes.
