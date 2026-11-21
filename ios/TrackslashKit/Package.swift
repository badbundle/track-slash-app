// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "TrackslashKit",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [.library(name: "TrackslashKit", targets: ["TrackslashKit"])],
    targets: [
        .target(name: "TrackslashKit"),
        .testTarget(name: "TrackslashKitTests", dependencies: ["TrackslashKit"]),
    ]
)
