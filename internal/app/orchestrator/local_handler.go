// Package orchestrator coordinates control-plane use cases owned by the Mac daemon.
package orchestrator

import (
	"context"
	"errors"
	"fmt"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/app/worker"
	"github.com/austinjiann/spare-compute/internal/artifact"
	"github.com/austinjiann/spare-compute/internal/connectivity/remoteconn"
	"github.com/austinjiann/spare-compute/internal/contentcache"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/protocol/mapper"
	remoteprotocol "github.com/austinjiann/spare-compute/internal/protocol/remote"
	"github.com/austinjiann/spare-compute/internal/trust"
)

const maximumLocalListLimit = 500

var (
	ErrMissingJobController     = errors.New("local handler job controller is required")
	ErrMissingRemoteController  = errors.New("local handler remote job controller is required")
	ErrMissingDeviceController  = errors.New("local handler device controller is required")
	ErrMissingPairingController = errors.New("local handler pairing controller is required")
	ErrInvalidConnectivity      = errors.New("local handler accepts at most one connectivity controller")
	ErrInvalidLocalDevice       = errors.New("invalid local handler device info")
)

// JobController is the narrow application boundary exposed over local IPC.
type JobController interface {
	Submit(context.Context, job.Spec) (job.Job, error)
	Get(context.Context, job.ID) (job.Job, error)
	List(context.Context, job.ListOptions) ([]job.Job, error)
	Cancel(context.Context, job.ID) (job.Job, error)
	ReadLogs(context.Context, job.ID, uint64, int) (worker.JobLogs, error)
}

// JobIDController is implemented by local job services that can accept a
// client-provided durable job ID.
type JobIDController interface {
	SubmitWithID(context.Context, job.ID, job.Spec) (job.Job, error)
}

// JobProgressController exposes progress records that may exist before or
// independent of a durable local job row.
type JobProgressController interface {
	GetProgress(context.Context, job.ID) (*job.Progress, error)
}

// PairedJobController routes explicit operations to one trusted LAN worker.
type PairedJobController interface {
	Submit(context.Context, string, job.Spec) (job.Job, error)
	Get(context.Context, string, job.ID) (job.Job, error)
	List(context.Context, string, job.ListOptions) ([]job.Job, error)
	Cancel(context.Context, string, job.ID) (job.Job, error)
	ReadLogs(context.Context, string, job.ID, uint64, int) (worker.JobLogs, error)
}

// PairedJobIDController is implemented by remote job services that can forward
// a client-provided durable job ID to compatible workers.
type PairedJobIDController interface {
	SubmitWithID(context.Context, string, job.ID, job.Spec) (job.Job, error)
}

// PairedJobProgressController exposes orchestrator-owned transfer/preparation
// progress for worker-owned jobs.
type PairedJobProgressController interface {
	GetProgress(context.Context, string, job.ID) (*job.Progress, error)
}

// LocalArtifactController restores outputs owned by this daemon.
type LocalArtifactController interface {
	RestoreArtifacts(context.Context, job.ID, string) (artifact.RestoreResult, error)
}

// PairedArtifactController fetches and restores outputs owned by a paired worker.
type PairedArtifactController interface {
	FetchArtifacts(context.Context, string, job.ID, string) (artifact.RestoreResult, error)
}

// DeviceController is the narrow nearby-device boundary exposed over local IPC.
type DeviceController interface {
	ListNearby(context.Context) (device.DiscoverySnapshot, error)
}

// PairingController is the narrow trust ceremony boundary exposed over local IPC.
type PairingController interface {
	Begin(context.Context, string) (trust.Pairing, error)
	ListPairings(context.Context) ([]trust.Pairing, error)
	Confirm(context.Context, string) (trust.Pairing, error)
	Reject(context.Context, string) (trust.Pairing, error)
	ListTrusted(context.Context) ([]trust.Peer, error)
	Unpair(context.Context, string) (trust.Peer, error)
}

type TrustedHintRefresher interface {
	RefreshTrustedHints(context.Context, device.DiscoverySnapshot) ([]trust.Peer, error)
}

// ConnectivityController exposes secret-free reachability state for local UI.
type ConnectivityController interface {
	States() []remoteconn.State
}

// LocalDeviceInfo is the secret-free local daemon identity exposed to local UI.
type LocalDeviceInfo struct {
	DeviceID           device.ID
	Name               string
	Role               device.Role
	Platform           string
	Architecture       string
	LogicalCPUCount    uint32
	TotalMemoryBytes   uint64
	ToolIDs            []string
	SupportedExecutors []string
}

func (info LocalDeviceInfo) Validate() error {
	if !info.DeviceID.Valid() || device.ValidateName(info.Name) != nil ||
		(info.Role != device.RoleWorker && info.Role != device.RoleOrchestrator) ||
		info.LogicalCPUCount > 4096 {
		return ErrInvalidLocalDevice
	}
	return nil
}

// LocalHandler maps authenticated protocol requests to application use cases.
type LocalHandler struct {
	jobs         JobController
	remote       PairedJobController
	devices      DeviceController
	pairings     PairingController
	connectivity ConnectivityController
	local        LocalDeviceInfo
	version      string
}

// NewLocalHandler constructs the local orchestrator control handler.
func NewLocalHandler(
	jobs JobController,
	remote PairedJobController,
	devices DeviceController,
	pairings PairingController,
	version string,
	connectivity ...ConnectivityController,
) (*LocalHandler, error) {
	if jobs == nil {
		return nil, ErrMissingJobController
	}
	if remote == nil {
		return nil, ErrMissingRemoteController
	}
	if devices == nil {
		return nil, ErrMissingDeviceController
	}
	if pairings == nil {
		return nil, ErrMissingPairingController
	}
	if len(connectivity) > 1 {
		return nil, ErrInvalidConnectivity
	}
	var connectivityController ConnectivityController
	if len(connectivity) == 1 {
		connectivityController = connectivity[0]
	}
	return &LocalHandler{
		jobs: jobs, remote: remote, devices: devices, pairings: pairings,
		connectivity: connectivityController, version: version,
	}, nil
}

// NewLocalHandlerWithLocalDevice constructs a local control handler that can
// identify the local daemon in health/status responses.
func NewLocalHandlerWithLocalDevice(
	jobs JobController,
	remote PairedJobController,
	devices DeviceController,
	pairings PairingController,
	local LocalDeviceInfo,
	version string,
	connectivity ...ConnectivityController,
) (*LocalHandler, error) {
	handler, err := NewLocalHandler(jobs, remote, devices, pairings, version, connectivity...)
	if err != nil {
		return nil, err
	}
	if err := handler.SetLocalDevice(local); err != nil {
		return nil, err
	}
	return handler, nil
}

// SetLocalDevice updates the secret-free local daemon identity exposed over
// authenticated local IPC.
func (handler *LocalHandler) SetLocalDevice(local LocalDeviceInfo) error {
	if err := local.Validate(); err != nil {
		return err
	}
	handler.local = local
	return nil
}

// Handle executes one already-authenticated local request.
func (handler *LocalHandler) Handle(ctx context.Context, request *localv1.Request) *localv1.Response {
	switch operation := request.GetOperation().(type) {
	case *localv1.Request_Ping:
		ping := &localv1.PingResponse{
			DaemonVersion: handler.version,
		}
		if handler.local.Name != "" {
			ping.DeviceId = string(handler.local.DeviceID)
			ping.DeviceName = handler.local.Name
			ping.Role = localDeviceRoleToProto(handler.local.Role)
			ping.Platform = handler.local.Platform
			ping.Arch = handler.local.Architecture
			ping.LogicalCpuCount = handler.local.LogicalCPUCount
			ping.TotalMemoryBytes = handler.local.TotalMemoryBytes
			ping.ToolIds = append([]string(nil), handler.local.ToolIDs...)
			ping.SupportedExecutors = supportedExecutorsToLocalProto(handler.local.SupportedExecutors)
		}
		return &localv1.Response{Result: &localv1.Response_Ping{Ping: ping}}
	case *localv1.Request_SubmitJob:
		return handler.submit(ctx, operation.SubmitJob)
	case *localv1.Request_GetJob:
		return handler.get(ctx, operation.GetJob)
	case *localv1.Request_ListJobs:
		return handler.list(ctx, operation.ListJobs)
	case *localv1.Request_CancelJob:
		return handler.cancel(ctx, operation.CancelJob)
	case *localv1.Request_ReadJobLogs:
		return handler.readLogs(ctx, operation.ReadJobLogs)
	case *localv1.Request_ListDevices:
		return handler.listDevices(ctx, operation.ListDevices)
	case *localv1.Request_BeginPairing:
		return handler.beginPairing(ctx, operation.BeginPairing)
	case *localv1.Request_ListPairings:
		return handler.listPairings(ctx, operation.ListPairings)
	case *localv1.Request_ConfirmPairing:
		return handler.confirmPairing(ctx, operation.ConfirmPairing)
	case *localv1.Request_RejectPairing:
		return handler.rejectPairing(ctx, operation.RejectPairing)
	case *localv1.Request_ListTrustedDevices:
		return handler.listTrustedDevices(ctx, operation.ListTrustedDevices)
	case *localv1.Request_UnpairDevice:
		return handler.unpairDevice(ctx, operation.UnpairDevice)
	case *localv1.Request_FetchArtifacts:
		return handler.fetchArtifacts(ctx, operation.FetchArtifacts)
	case *localv1.Request_GetJobProgress:
		return handler.getJobProgress(ctx, operation.GetJobProgress)
	default:
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "unsupported local operation")
	}
}

func localDeviceRoleToProto(role device.Role) localv1.DeviceRole {
	switch role {
	case device.RoleWorker:
		return localv1.DeviceRole_DEVICE_ROLE_WORKER
	case device.RoleOrchestrator:
		return localv1.DeviceRole_DEVICE_ROLE_ORCHESTRATOR
	default:
		return localv1.DeviceRole_DEVICE_ROLE_UNSPECIFIED
	}
}

func supportedExecutorsToLocalProto(values []string) []localv1.Executor {
	result := make([]localv1.Executor, 0, len(values))
	for _, value := range values {
		switch job.Executor(value) {
		case job.ExecutorNative:
			result = append(result, localv1.Executor_EXECUTOR_NATIVE)
		case job.ExecutorContainer:
			result = append(result, localv1.Executor_EXECUTOR_CONTAINER)
		}
	}
	return result
}

func (handler *LocalHandler) fetchArtifacts(
	ctx context.Context,
	request *localv1.FetchArtifactsRequest,
) *localv1.Response {
	if request == nil || request.GetDestination() == "" {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "artifact destination is required")
	}
	id, err := job.ParseID(request.GetJobId())
	if err != nil {
		return errorResponse(err)
	}
	remoteOperation, err := handler.shouldRouteRemotely(ctx, request.GetDeviceSelector(), id)
	if err != nil {
		return errorResponse(err)
	}
	var result artifact.RestoreResult
	if remoteOperation {
		controller, ok := handler.remote.(PairedArtifactController)
		if !ok {
			return errorResponse(worker.ErrArtifactsDisabled)
		}
		result, err = controller.FetchArtifacts(
			ctx, request.GetDeviceSelector(), id, request.GetDestination(),
		)
	} else {
		controller, ok := handler.jobs.(LocalArtifactController)
		if !ok {
			return errorResponse(worker.ErrArtifactsDisabled)
		}
		result, err = controller.RestoreArtifacts(ctx, id, request.GetDestination())
	}
	if err != nil {
		return errorResponse(err)
	}
	result = artifact.NormalizeResult(result)
	if err := result.Validate(); err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_FetchArtifacts{
		FetchArtifacts: &localv1.FetchArtifactsResponse{
			Destination:       result.Destination,
			RestoredFileCount: uint32(len(result.Restored)),
			ConflictFileCount: uint32(len(result.Conflicts)),
		},
	}}
}

func (handler *LocalHandler) listDevices(
	ctx context.Context,
	request *localv1.ListDevicesRequest,
) *localv1.Response {
	if request == nil {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "device list request is required")
	}
	snapshot, err := handler.devices.ListNearby(ctx)
	if err != nil {
		return errorResponse(err)
	}
	message, err := mapper.DiscoverySnapshotToProto(snapshot)
	if err != nil {
		return errorResponse(err)
	}
	trusted, err := handler.listTrustedWithHints(ctx, snapshot)
	if err != nil {
		return errorResponse(err)
	}
	message.TrustedDevices, err = handler.trustedPeersToProto(trusted)
	if err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_ListDevices{ListDevices: message}}
}

func (handler *LocalHandler) listTrustedWithHints(
	ctx context.Context,
	snapshot device.DiscoverySnapshot,
) ([]trust.Peer, error) {
	if refresher, ok := handler.pairings.(TrustedHintRefresher); ok {
		return refresher.RefreshTrustedHints(ctx, snapshot)
	}
	return handler.pairings.ListTrusted(ctx)
}

func (handler *LocalHandler) beginPairing(ctx context.Context, request *localv1.BeginPairingRequest) *localv1.Response {
	if request == nil || request.GetDeviceSelector() == "" {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "device selector is required")
	}
	value, err := handler.pairings.Begin(ctx, request.GetDeviceSelector())
	if err != nil {
		return errorResponse(err)
	}
	message, err := mapper.PairingToProto(value)
	if err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_BeginPairing{
		BeginPairing: &localv1.BeginPairingResponse{Pairing: message},
	}}
}

func (handler *LocalHandler) listPairings(ctx context.Context, request *localv1.ListPairingsRequest) *localv1.Response {
	if request == nil {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "pairing list request is required")
	}
	values, err := handler.pairings.ListPairings(ctx)
	if err != nil {
		return errorResponse(err)
	}
	messages := make([]*localv1.Pairing, len(values))
	for index, value := range values {
		messages[index], err = mapper.PairingToProto(value)
		if err != nil {
			return errorResponse(err)
		}
	}
	return &localv1.Response{Result: &localv1.Response_ListPairings{
		ListPairings: &localv1.ListPairingsResponse{Pairings: messages},
	}}
}

func (handler *LocalHandler) confirmPairing(ctx context.Context, request *localv1.ConfirmPairingRequest) *localv1.Response {
	if request == nil || request.GetPairingSelector() == "" {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "pairing selector is required")
	}
	value, err := handler.pairings.Confirm(ctx, request.GetPairingSelector())
	if err != nil {
		return errorResponse(err)
	}
	message, err := mapper.PairingToProto(value)
	if err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_ConfirmPairing{
		ConfirmPairing: &localv1.ConfirmPairingResponse{Pairing: message},
	}}
}

func (handler *LocalHandler) rejectPairing(ctx context.Context, request *localv1.RejectPairingRequest) *localv1.Response {
	if request == nil || request.GetPairingSelector() == "" {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "pairing selector is required")
	}
	value, err := handler.pairings.Reject(ctx, request.GetPairingSelector())
	if err != nil {
		return errorResponse(err)
	}
	message, err := mapper.PairingToProto(value)
	if err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_RejectPairing{
		RejectPairing: &localv1.RejectPairingResponse{Pairing: message},
	}}
}

func (handler *LocalHandler) listTrustedDevices(
	ctx context.Context,
	request *localv1.ListTrustedDevicesRequest,
) *localv1.Response {
	if request == nil {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "trusted-device list request is required")
	}
	values, err := handler.pairings.ListTrusted(ctx)
	if err != nil {
		return errorResponse(err)
	}
	messages, err := handler.trustedPeersToProto(values)
	if err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_ListTrustedDevices{
		ListTrustedDevices: &localv1.ListTrustedDevicesResponse{Devices: messages},
	}}
}

func (handler *LocalHandler) unpairDevice(ctx context.Context, request *localv1.UnpairDeviceRequest) *localv1.Response {
	if request == nil || request.GetDeviceSelector() == "" {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "device selector is required")
	}
	peer, err := handler.pairings.Unpair(ctx, request.GetDeviceSelector())
	if err != nil {
		return errorResponse(err)
	}
	message, err := mapper.TrustedPeerToProto(peer)
	if err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_UnpairDevice{
		UnpairDevice: &localv1.UnpairDeviceResponse{Device: message},
	}}
}

func (handler *LocalHandler) trustedPeersToProto(values []trust.Peer) ([]*localv1.TrustedDevice, error) {
	messages := make([]*localv1.TrustedDevice, len(values))
	states := make(map[device.ID]remoteconn.State)
	if handler.connectivity != nil {
		for _, state := range handler.connectivity.States() {
			states[state.DeviceID] = state
		}
	}
	var err error
	for index, value := range values {
		messages[index], err = mapper.TrustedPeerToProto(value)
		if err != nil {
			return nil, err
		}
		applyConnectivityState(messages[index], value, states[value.DeviceID], handler.connectivity != nil)
	}
	return messages, nil
}

func applyConnectivityState(
	message *localv1.TrustedDevice,
	peer trust.Peer,
	state remoteconn.State,
	enabled bool,
) {
	if message == nil {
		return
	}
	if !enabled || peer.State != trust.StateActive {
		message.ConnectivityState = localv1.ConnectivityState_CONNECTIVITY_STATE_DISABLED
		return
	}
	if !peer.ConnectivitySecret.Valid() {
		message.ConnectivityState = localv1.ConnectivityState_CONNECTIVITY_STATE_UNAVAILABLE
		message.ConnectivityError = "re-pair this device to enable remote connectivity"
		return
	}
	switch state.Status {
	case remoteconn.StatusConnected:
		message.ConnectivityState = localv1.ConnectivityState_CONNECTIVITY_STATE_CONNECTED
	case remoteconn.StatusUnavailable:
		message.ConnectivityState = localv1.ConnectivityState_CONNECTIVITY_STATE_UNAVAILABLE
	default:
		message.ConnectivityState = localv1.ConnectivityState_CONNECTIVITY_STATE_CONNECTING
	}
	message.ConnectivityPath = state.PathKind
	message.ConnectivityError = state.LastError
	if !state.UpdatedAt.IsZero() {
		message.ConnectivityUpdatedAtUnixNano = state.UpdatedAt.UTC().UnixNano()
	}
}

func (handler *LocalHandler) readLogs(
	ctx context.Context,
	request *localv1.ReadJobLogsRequest,
) *localv1.Response {
	if request == nil {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "log request is required")
	}
	if request.GetLimit() > joblogging.MaximumPageLimit {
		return failureResponse(
			localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			fmt.Sprintf("job log limit cannot exceed %d", joblogging.MaximumPageLimit),
		)
	}
	id, err := job.ParseID(request.GetJobId())
	if err != nil {
		return errorResponse(err)
	}
	remoteOperation, err := handler.shouldRouteRemotely(ctx, request.GetDeviceSelector(), id)
	if err != nil {
		return errorResponse(err)
	}
	var result worker.JobLogs
	if remoteOperation {
		result, err = handler.remote.ReadLogs(
			ctx, request.GetDeviceSelector(), id, request.GetAfterSequence(), int(request.GetLimit()),
		)
	} else {
		result, err = handler.jobs.ReadLogs(ctx, id, request.GetAfterSequence(), int(request.GetLimit()))
	}
	if err != nil {
		return errorResponse(err)
	}
	jobMessage, err := mapper.JobToProto(result.Job)
	if err != nil {
		return errorResponse(err)
	}
	records, err := mapper.LogRecordsToProto(result.Page.Records)
	if err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_ReadJobLogs{ReadJobLogs: &localv1.ReadJobLogsResponse{
		Job:     jobMessage,
		Records: records,
		HasMore: result.Page.HasMore,
	}}}
}

func (handler *LocalHandler) submit(
	ctx context.Context,
	request *localv1.SubmitJobRequest,
) *localv1.Response {
	if request == nil {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "submit request is required")
	}
	spec, err := mapper.SpecFromProto(request.GetSpec())
	if err != nil {
		return errorResponse(err)
	}
	var requestedID job.ID
	if request.GetJobId() != "" {
		requestedID, err = job.ParseID(request.GetJobId())
		if err != nil {
			return errorResponse(err)
		}
	}
	var value job.Job
	if request.GetDeviceSelector() == "" {
		if requestedID.Valid() {
			controller, ok := handler.jobs.(JobIDController)
			if !ok {
				return failureResponse(
					localv1.ErrorCode_ERROR_CODE_UNSUPPORTED_VERSION,
					"local daemon does not support client-provided job IDs",
				)
			}
			value, err = controller.SubmitWithID(ctx, requestedID, spec)
		} else {
			value, err = handler.jobs.Submit(ctx, spec)
		}
	} else {
		if requestedID.Valid() {
			controller, ok := handler.remote.(PairedJobIDController)
			if !ok {
				return failureResponse(
					localv1.ErrorCode_ERROR_CODE_UNSUPPORTED_VERSION,
					"remote job controller does not support client-provided job IDs",
				)
			}
			value, err = controller.SubmitWithID(ctx, request.GetDeviceSelector(), requestedID, spec)
		} else {
			value, err = handler.remote.Submit(ctx, request.GetDeviceSelector(), spec)
		}
	}
	if err != nil {
		return errorResponse(err)
	}
	message, err := mapper.JobToProto(value)
	if err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_SubmitJob{SubmitJob: &localv1.SubmitJobResponse{
		Job: message,
	}}}
}

func (handler *LocalHandler) get(ctx context.Context, request *localv1.GetJobRequest) *localv1.Response {
	if request == nil {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "get request is required")
	}
	id, err := job.ParseID(request.GetJobId())
	if err != nil {
		return errorResponse(err)
	}
	remoteOperation, err := handler.shouldRouteRemotely(ctx, request.GetDeviceSelector(), id)
	if err != nil {
		return errorResponse(err)
	}
	var value job.Job
	if remoteOperation {
		value, err = handler.remote.Get(ctx, request.GetDeviceSelector(), id)
	} else {
		value, err = handler.jobs.Get(ctx, id)
	}
	if err != nil {
		return errorResponse(err)
	}
	message, err := mapper.JobToProto(value)
	if err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_GetJob{GetJob: &localv1.GetJobResponse{
		Job: message,
	}}}
}

func (handler *LocalHandler) getJobProgress(
	ctx context.Context,
	request *localv1.GetJobProgressRequest,
) *localv1.Response {
	if request == nil {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "progress request is required")
	}
	id, err := job.ParseID(request.GetJobId())
	if err != nil {
		return errorResponse(err)
	}
	var progress *job.Progress
	if request.GetDeviceSelector() == "" {
		controller, ok := handler.jobs.(JobProgressController)
		if ok {
			progress, err = controller.GetProgress(ctx, id)
		}
	} else {
		controller, ok := handler.remote.(PairedJobProgressController)
		if ok {
			progress, err = controller.GetProgress(ctx, request.GetDeviceSelector(), id)
		}
	}
	if err != nil {
		return errorResponse(err)
	}
	response := &localv1.GetJobProgressResponse{}
	if progress != nil {
		message, err := mapper.ProgressToProto(*progress)
		if err != nil {
			return errorResponse(err)
		}
		response.Progress = message
	}
	return &localv1.Response{Result: &localv1.Response_GetJobProgress{GetJobProgress: response}}
}

func (handler *LocalHandler) list(ctx context.Context, request *localv1.ListJobsRequest) *localv1.Response {
	if request == nil {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "list request is required")
	}
	if request.GetLimit() > maximumLocalListLimit {
		return failureResponse(
			localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			fmt.Sprintf("job list limit cannot exceed %d", maximumLocalListLimit),
		)
	}
	states, err := mapper.StatesFromProto(request.GetStates())
	if err != nil {
		return errorResponse(err)
	}
	options := job.ListOptions{States: states, Limit: int(request.GetLimit())}
	var values []job.Job
	if request.GetDeviceSelector() == "" {
		values, err = handler.jobs.List(ctx, options)
	} else {
		values, err = handler.remote.List(ctx, request.GetDeviceSelector(), options)
	}
	if err != nil {
		return errorResponse(err)
	}
	messages := make([]*localv1.Job, len(values))
	for index, value := range values {
		messages[index], err = mapper.JobToProto(value)
		if err != nil {
			return errorResponse(err)
		}
	}
	return &localv1.Response{Result: &localv1.Response_ListJobs{ListJobs: &localv1.ListJobsResponse{
		Jobs: messages,
	}}}
}

func (handler *LocalHandler) cancel(ctx context.Context, request *localv1.CancelJobRequest) *localv1.Response {
	if request == nil {
		return failureResponse(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "cancel request is required")
	}
	id, err := job.ParseID(request.GetJobId())
	if err != nil {
		return errorResponse(err)
	}
	remoteOperation, err := handler.shouldRouteRemotely(ctx, request.GetDeviceSelector(), id)
	if err != nil {
		return errorResponse(err)
	}
	var value job.Job
	if remoteOperation {
		value, err = handler.remote.Cancel(ctx, request.GetDeviceSelector(), id)
	} else {
		value, err = handler.jobs.Cancel(ctx, id)
	}
	if err != nil {
		return errorResponse(err)
	}
	message, err := mapper.JobToProto(value)
	if err != nil {
		return errorResponse(err)
	}
	return &localv1.Response{Result: &localv1.Response_CancelJob{CancelJob: &localv1.CancelJobResponse{
		Job: message,
	}}}
}

func (handler *LocalHandler) shouldRouteRemotely(
	ctx context.Context,
	selector string,
	id job.ID,
) (bool, error) {
	if selector != "" {
		return true, nil
	}
	_, err := handler.jobs.Get(ctx, id)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, job.ErrNotFound) {
		return true, nil
	}
	return false, err
}

func errorResponse(err error) *localv1.Response {
	var remoteError *remoteprotocol.Error
	if errors.As(err, &remoteError) {
		code := localv1.ErrorCode_ERROR_CODE_INTERNAL
		switch remoteError.Code {
		case computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT:
			code = localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT
		case computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_NOT_FOUND:
			code = localv1.ErrorCode_ERROR_CODE_NOT_FOUND
		case computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_CONFLICT:
			code = localv1.ErrorCode_ERROR_CODE_CONFLICT
		case computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_JOB_TERMINAL:
			code = localv1.ErrorCode_ERROR_CODE_JOB_TERMINAL
		case computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_UNAUTHENTICATED:
			code = localv1.ErrorCode_ERROR_CODE_UNAUTHENTICATED
		case computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_UNSUPPORTED_VERSION:
			code = localv1.ErrorCode_ERROR_CODE_UNSUPPORTED_VERSION
		}
		return failureResponse(code, remoteError.Message)
	}
	code := localv1.ErrorCode_ERROR_CODE_INTERNAL
	message := "internal daemon error"
	switch {
	case errors.Is(err, job.ErrInvalidID),
		errors.Is(err, job.ErrInvalidSpec),
		errors.Is(err, job.ErrInvalidJob),
		errors.Is(err, job.ErrInvalidState),
		errors.Is(err, job.ErrInvalidTransition),
		errors.Is(err, job.ErrInvalidProgress),
		errors.Is(err, joblogging.ErrInvalidPage),
		errors.Is(err, joblogging.ErrInvalidRecord),
		errors.Is(err, trust.ErrInvalidPairID),
		errors.Is(err, trust.ErrInvalidPairing),
		errors.Is(err, trust.ErrInvalidPeer),
		errors.Is(err, device.ErrInvalidID),
		errors.Is(err, artifact.ErrInvalidBundle),
		errors.Is(err, artifact.ErrInvalidDestination):
		code = localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT
		message = err.Error()
	case errors.Is(err, job.ErrNotFound), errors.Is(err, joblogging.ErrNotFound),
		errors.Is(err, trust.ErrNotFound), errors.Is(err, trust.ErrPairingNotFound),
		errors.Is(err, ErrNearbyDeviceNotFound), errors.Is(err, artifact.ErrNotFound):
		code = localv1.ErrorCode_ERROR_CODE_NOT_FOUND
		message = err.Error()
	case errors.Is(err, job.ErrConflict), errors.Is(err, trust.ErrConflict), errors.Is(err, artifact.ErrConflict),
		errors.Is(err, ErrNearbyDeviceAmbiguous), errors.Is(err, contentcache.ErrQuotaExceeded),
		errors.Is(err, contentcache.ErrReservationLimit), errors.Is(err, ErrRemoteWorkerIncompatible):
		code = localv1.ErrorCode_ERROR_CODE_CONFLICT
		message = err.Error()
	case errors.Is(err, worker.ErrJobTerminal):
		code = localv1.ErrorCode_ERROR_CODE_JOB_TERMINAL
		message = err.Error()
	case errors.Is(err, worker.ErrArtifactsDisabled), errors.Is(err, worker.ErrArtifactsNotReady):
		code = localv1.ErrorCode_ERROR_CODE_CONFLICT
		message = err.Error()
	case errors.Is(err, trust.ErrPairingUnavailable):
		code = localv1.ErrorCode_ERROR_CODE_PAIRING_UNAVAILABLE
		message = err.Error()
	case errors.Is(err, ErrRemoteWorkerUnavailable):
		code = localv1.ErrorCode_ERROR_CODE_DEVICE_UNAVAILABLE
		message = err.Error()
	case errors.Is(err, ErrRemotePlacementPersistence):
		message = err.Error()
	}
	return failureResponse(code, message)
}

func failureResponse(code localv1.ErrorCode, message string) *localv1.Response {
	return &localv1.Response{Error: &localv1.Error{Code: code, Message: message}}
}
