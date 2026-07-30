import Foundation

struct TaskPlan: Identifiable, Equatable, Sendable {
    let id = UUID()
    let title: String
    let projectPath: String
    let commands: [String]
    let outputs: [String]
    let notes: [String]

    var commandLine: String {
        CommandInput.shellCommand(["sh", "-lc", shellScript])
    }

    private var shellScript: String {
        (["set -e"] + commands).joined(separator: "\n")
    }
}

enum TaskPlannerError: LocalizedError, Equatable {
    case emptyRequest
    case missingProject
    case noAllowedCapabilities
    case noPlan(String)

    var errorDescription: String? {
        switch self {
        case .emptyRequest:
            return "Ask what to do first."
        case .missingProject:
            return "Drop a project first."
        case .noAllowedCapabilities:
            return "Enable at least one allowed task for this device."
        case .noPlan(let detail):
            return detail
        }
    }
}

protocol TaskPlanning: Sendable {
    func plan(
        request: String,
        projectPath: String,
        capabilities: Set<DeviceCapability>
    ) throws -> TaskPlan
}

struct LocalTaskPlanner: TaskPlanning {
    func plan(
        request: String,
        projectPath: String,
        capabilities: Set<DeviceCapability>
    ) throws -> TaskPlan {
        let trimmedRequest = request.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedRequest.isEmpty else {
            throw TaskPlannerError.emptyRequest
        }

        let trimmedProjectPath = projectPath.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedProjectPath.isEmpty else {
            throw TaskPlannerError.missingProject
        }
        guard !capabilities.isEmpty else {
            throw TaskPlannerError.noAllowedCapabilities
        }

        let project = ProjectSnapshot(path: trimmedProjectPath)
        let lowercasedRequest = trimmedRequest.lowercased()
        let wantsCI = containsAny(lowercasedRequest, ["ci", "check", "verify"])
        let wantsTests = wantsCI || containsAny(lowercasedRequest, ["test", "tests"])
        let wantsBuild = wantsCI || containsAny(lowercasedRequest, ["build", "package", "release"])
        let wantsDocker = containsAny(lowercasedRequest, ["docker", "container", "image"])

        var commands: [String] = []
        var outputs: [String] = []
        var notes: [String] = []

        if wantsTests {
            if capabilities.contains(.tests) {
                commands.append(contentsOf: testCommands(for: project))
            } else {
                notes.append("Tests are disabled for this device.")
            }
        }

        if wantsBuild {
            if capabilities.contains(.builds) {
                let build = buildCommands(for: project, request: lowercasedRequest)
                commands.append(contentsOf: build.commands)
                outputs.append(contentsOf: build.outputs)
            } else {
                notes.append("Builds are disabled for this device.")
            }
        }

        if wantsDocker {
            if capabilities.contains(.docker), project.has("Dockerfile") {
                commands.append("docker build .")
            } else if !capabilities.contains(.docker) {
                notes.append("Docker is disabled for this device.")
            }
        }

        if commands.isEmpty, capabilities.contains(.shell), looksLikeExactCommand(trimmedRequest) {
            commands.append(trimmedRequest)
            notes.append("Using the request as an exact command.")
        }

        commands = Array(NSOrderedSet(array: commands)) as? [String] ?? commands
        outputs = Array(NSOrderedSet(array: outputs)) as? [String] ?? outputs

        guard !commands.isEmpty else {
            throw TaskPlannerError.noPlan(
                "I couldn't infer a safe plan. Try “run tests”, “build app”, or enable Commands and use an exact command."
            )
        }

        return TaskPlan(
            title: title(for: lowercasedRequest),
            projectPath: trimmedProjectPath,
            commands: commands,
            outputs: outputs,
            notes: notes
        )
    }

    private func testCommands(for project: ProjectSnapshot) -> [String] {
        var commands: [String] = []
        if project.has("go.mod") {
            commands.append("go test ./...")
        }
        if project.has("Package.swift") {
            commands.append("swift test")
        }
        if project.has("Cargo.toml") {
            commands.append("cargo test")
        }
        if project.packageJSONContainsScript("test") {
            commands.append("npm test")
        }
        if project.has("pyproject.toml") || project.has("pytest.ini") {
            commands.append("python -m pytest")
        }
        if commands.isEmpty, project.makefileHasTarget("test") {
            commands.append("make test")
        }
        return commands
    }

    private func buildCommands(
        for project: ProjectSnapshot,
        request: String
    ) -> (commands: [String], outputs: [String]) {
        var commands: [String] = []
        var outputs: [String] = []
        let wantsPackage = containsAny(request, ["package", "release", "app"])

        if wantsPackage, project.makefileHasTarget("macos-package") {
            commands.append("make macos-package")
            outputs.append("dist/macos/ComputeHop.app")
            return (commands, outputs)
        }

        if project.has("Package.swift") {
            commands.append("swift build")
        }
        if project.has("go.mod") {
            commands.append("go build ./...")
        }
        if project.has("Cargo.toml") {
            commands.append("cargo build")
        }
        if project.packageJSONContainsScript("build") {
            commands.append("npm run build")
            outputs.append("dist")
        }
        if commands.isEmpty, project.makefileHasTarget("build") {
            commands.append("make build")
        }

        return (commands, outputs)
    }

    private func title(for request: String) -> String {
        if containsAny(request, ["ci", "check", "verify"]) {
            return "Run project checks"
        }
        if containsAny(request, ["test", "tests"]) {
            return "Run tests"
        }
        if containsAny(request, ["package", "release"]) {
            return "Package project"
        }
        if request.contains("build") {
            return "Build project"
        }
        return "Run task"
    }

    private func containsAny(_ value: String, _ needles: [String]) -> Bool {
        needles.contains { value.contains($0) }
    }

    private func looksLikeExactCommand(_ request: String) -> Bool {
        let firstToken = request.split(separator: " ").first.map(String.init) ?? ""
        return [
            "cargo",
            "docker",
            "ffmpeg",
            "go",
            "make",
            "npm",
            "pnpm",
            "python",
            "sh",
            "swift",
            "yarn",
        ].contains(firstToken)
    }
}

private struct ProjectSnapshot {
    let path: String

    func has(_ relativePath: String) -> Bool {
        FileManager.default.fileExists(atPath: url(relativePath).path)
    }

    func makefileHasTarget(_ target: String) -> Bool {
        let makefileNames = ["Makefile", "makefile"]
        return makefileNames.contains { name in
            guard let contents = try? String(contentsOf: url(name), encoding: .utf8) else {
                return false
            }
            return contents.contains("\n\(target):") || contents.hasPrefix("\(target):")
        }
    }

    func packageJSONContainsScript(_ script: String) -> Bool {
        guard let data = try? Data(contentsOf: url("package.json")),
              let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let scripts = object["scripts"] as? [String: Any]
        else {
            return false
        }
        return scripts[script] != nil
    }

    private func url(_ relativePath: String) -> URL {
        URL(fileURLWithPath: path).appendingPathComponent(relativePath)
    }
}
