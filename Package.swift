// swift-tools-version: 6.2

import PackageDescription

let package = Package(
    name: "ComputeHop",
    platforms: [
        .macOS(.v14),
    ],
    products: [
        .executable(name: "ComputeHop", targets: ["ComputeHopApp"]),
    ],
    dependencies: [
        .package(
            url: "https://github.com/apple/swift-protobuf.git",
            exact: "1.38.1"
        ),
    ],
    targets: [
        .target(
            name: "ComputeHopProtocol",
            dependencies: [
                .product(name: "SwiftProtobuf", package: "swift-protobuf"),
            ],
            path: "gen/swift"
        ),
        .executableTarget(
            name: "ComputeHopApp",
            dependencies: ["ComputeHopProtocol"],
            path: "apps/macos/ComputeHop"
        ),
        .testTarget(
            name: "ComputeHopAppTests",
            dependencies: ["ComputeHopApp", "ComputeHopProtocol"],
            path: "apps/macos/ComputeHopTests"
        ),
    ]
)
