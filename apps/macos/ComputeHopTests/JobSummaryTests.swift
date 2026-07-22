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

@Test
func jobSummaryFormatsProgress() {
    var spec = Computehop_Local_V1_JobSpec()
    spec.executable = "ffmpeg"
    spec.arguments = ["-i", "input.mov", "output.mp4"]
    spec.executor = .native
    var progress = Computehop_Local_V1_JobProgress()
    progress.phase = .download
    progress.completedBytes = 512 * 1024
    progress.totalBytes = 1024 * 1024
    progress.updatedAtUnixNano = 1
    var value = Computehop_Local_V1_Job()
    value.id = "job-id"
    value.spec = spec
    value.state = .transferring
    value.updatedAtUnixNano = 1
    value.progress = progress

    #expect(JobSummary(value).progressText == "Download 50% (512KiB/1MiB)")
}
