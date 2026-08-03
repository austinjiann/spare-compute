#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_dir=$(CDPATH= cd -- "$script_dir/.." && pwd -P)

cd "$repository_dir"

echo "==> CLI device/setup/run guidance"
go test ./cmd/computehop -run 'Test(DevicesCommand(PrintsNearbyDevicesAsNotConnected|PrintsConnectedAndNearbyEmptyState|SuggestsExplicitConnectForMultipleNearbyWorkers|CombinesOneTrustedPeerWithItsNearbyPresence|CollapsesDuplicateNearbyRowsForSingleActivePeer|ShowsRemotePathForOfflineLANPeer|ShowsLANOnlyForDisabledRemoteConnectivity)|RunCommand(AutoSelectorErrorExplainsConnectNearby|UnavailableWorkerErrorAddsRecoverySteps|ExplicitSelectorAmbiguityExplainsLongerID|DeclaresOutputsAndPrintsFetchHint|FollowsAndFetchesDeclaredOutputs|GetWaitsAndFetchesDeclaredOutputsToWorkingDirectory)|Setup(CommandPrintsFirstRunChecklistWithoutDaemon|HelpShowsRoleAliasesMacAndVPS|SubcommandHelpShowsExamplesWithoutDaemon|WorkersCommandPrintsLinuxAndWindowsPackageChecklistWithoutDaemon|WorkersCommandRejectsInvalidConnectivityWithoutDaemon|SmokeCommandPrintsPackageChecklistWithoutDaemon|MacCommandPrintsDefaultOrchestratorInstallWithoutDaemon|MacCommandInterpolatesLANOnlyWithoutDaemon|MacCommandRejectsLANOnlyWithConnectivity)|Connect(CommandWithoutDeviceSuggestsNearbyWorker|NearbyBeginsPairingWithOnlyNearbyWorker|ConfirmInfersTheOnlyActionableRequest)|DisconnectCommandRevokesSelectedDevice|DoctorCommand(ReportsOfflinePairedWorker|PointsAtWorkerSetupForMissingWorkers|PrintsStartAdviceWhenDaemonIsNotRunning))$'

echo "==> Project snapshot ignore and output safety"
go test ./internal/snapshot -run 'Test(BuildResolvesProjectAppliesNestedIgnoreRulesAndStoresChunks|BuildDeclaredCollectsOnlyExactOutputsAndDeduplicatesOverlap|BuildDeclaredRejectsMissingReservedAndSymlinkOutputs|ManifestIdentityIsCanonicalAndRejectsUnsafePaths)$'

echo "==> Remote pre-submit worker capability checks"
go test ./internal/app/orchestrator -run 'TestRemoteJobService(AutoSubmitSkipsWorkersMissingPlannedTool|SubmitRejectsSelectedWorkerMissingPlannedToolBeforeSnapshot|SubmitRejectsSelectedWorkerUnsupportedExecutorBeforeSnapshot|SubmitRejectsSelectedWorkerMissingRequiredToolsBeforeSnapshot|UnavailableWorkerPreservesDialCause)$'

echo "==> Control Center UI planning, device, and setup logic"
npm test --prefix apps/control-center -- --test-name-pattern='mapDevices|workerReadinessSummary|friendlyJobFailure|runReadiness|launchAgentStatus|installLaunchAgent|planControlCenterTask|suggestTasks|outputValidation|deviceLabel'

echo "==> Control Center dependency audit"
npm audit --prefix apps/control-center

if [ "$(uname -s)" = "Darwin" ]; then
    echo "==> Setup-guide screenshots"
    npm run screenshots:docs --prefix apps/control-center
else
    echo "==> Skipping setup-guide screenshots; Electron screenshot capture is validated on macOS."
fi

echo "Local launch validation passed."
