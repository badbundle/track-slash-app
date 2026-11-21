# Trackslash for iOS

A small iPhone app for the thing agents ask people most: answering a [form](../internal/model/form.go) and being told when one is waiting. It signs in with a username and password, lists the Inbox (To do and Done), lets you answer every question type and submit, and registers for push notifications.

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

## Notifications

The app asks for permission after sign-in, registers its APNs token with `PUT /api/v1/me/devices` on every launch, and removes it with `DELETE /api/v1/me/devices` on sign-out. The server sends through Apple when the `TRACK_SLASH_APNS_*` variables are set; see [DEPLOYMENT.md](../DEPLOYMENT.md#ios-app-push). Which events notify is the account's existing Notifications setting on the website (forms are on by default).

A notification carries the page it is about as `url`; tapping a form's notification opens that form.

Push only works on a real device. The simulator accepts `xcrun simctl push` for testing the tap handling, with a payload like `{"aps":{"alert":"Form waiting"},"url":"/owner/projects/KEY/forms/form-5"}`.

## Not done yet

- App Store listing, icon and TestFlight lane (Fastlane, as in the other Bad Bundle apps).
- Passkey sign-in, and sign-in with an API token.
- Media on questions, file answers, and per-question notes.
