import ComputeHopProtocol
import Testing

@testable import ComputeHopApp

@Test
func succeededJobWithDeclaredOutputsCanFetchThem() {
    var spec = Computehop_Local_V1_JobSpec()
    spec.executable = "cargo"
    spec.arguments = ["build", "--release"]
    spec.outputs = ["target/release/app"]

    var value = Computehop_Local_V1_Job()
    value.id = "7a338fa3-7ba4-4c54-bf59-da1161f6b76f"
    value.spec = spec
    value.state = .succeeded
    value.updatedAtUnixNano = 1_700_000_000_000_000_000

    let summary = JobSummary(value, target: "Gaming PC")
    #expect(summary.outputs == ["target/release/app"])
    #expect(summary.canFetchOutputs)
    #expect(summary.target == "Gaming PC")
}

@Test
func incompleteJobCannotFetchDeclaredOutputsYet() {
    var spec = Computehop_Local_V1_JobSpec()
    spec.executable = "cargo"
    spec.outputs = ["target/release/app"]

    var value = Computehop_Local_V1_Job()
    value.id = "7a338fa3-7ba4-4c54-bf59-da1161f6b76f"
    value.spec = spec
    value.state = .collecting
    value.updatedAtUnixNano = 1_700_000_000_000_000_000

    #expect(!JobSummary(value).canFetchOutputs)
}
