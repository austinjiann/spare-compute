package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/app/worker"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/placement"
	"github.com/austinjiann/spare-compute/internal/protocol/mapper"
	remoteprotocol "github.com/austinjiann/spare-compute/internal/protocol/remote"
	"github.com/austinjiann/spare-compute/internal/trust"
)

const remoteDialTimeout = 12 * time.Second

var (
	ErrMissingRemoteDependency    = errors.New("remote job service dependency is required")
	ErrRemoteWorkerUnavailable    = errors.New("paired worker is not available on this LAN")
	ErrRemotePlacementPersistence = errors.New("remote job was accepted but its placement could not be saved")
)

// RemoteDialer opens a key-pinned job-control connection to one LAN observation.
type RemoteDialer interface {
	DialRemote(context.Context, device.NearbyDevice, trust.Peer) (remoteprotocol.Caller, error)
}

// RemoteDependencies configure explicit paired-worker job routing.
type RemoteDependencies struct {
	Nearby     DeviceController
	Trust      trust.Repository
	Placements placement.Repository
	Dialer     RemoteDialer
}

// RemoteJobService routes operations to a current LAN observation whose live
// certificate matches an explicit or durably remembered worker pin.
type RemoteJobService struct {
	nearby     DeviceController
	trust      trust.Repository
	placements placement.Repository
	dialer     RemoteDialer
}

// NewRemoteJobService constructs the orchestrator-side remote job controller.
func NewRemoteJobService(dependencies RemoteDependencies) (*RemoteJobService, error) {
	if dependencies.Nearby == nil || dependencies.Trust == nil ||
		dependencies.Placements == nil || dependencies.Dialer == nil {
		return nil, ErrMissingRemoteDependency
	}
	return &RemoteJobService{
		nearby: dependencies.Nearby, trust: dependencies.Trust,
		placements: dependencies.Placements, dialer: dependencies.Dialer,
	}, nil
}

func (service *RemoteJobService) Submit(
	ctx context.Context,
	selector string,
	spec job.Spec,
) (job.Job, error) {
	message, err := mapper.SpecToRemoteProto(spec)
	if err != nil {
		return job.Job{}, err
	}
	peer, err := service.resolveTrustedWorker(ctx, selector)
	if err != nil {
		return job.Job{}, err
	}
	response, err := service.call(ctx, peer, &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_SubmitJob{SubmitJob: &computehopv1.SubmitJobRequest{Spec: message}},
	})
	if err != nil {
		return job.Job{}, err
	}
	result := response.GetSubmitJob()
	if result == nil {
		return job.Job{}, remoteprotocol.ErrInvalidMessage
	}
	value, err := mapper.JobFromRemoteProto(result.GetJob())
	if err != nil {
		return job.Job{}, err
	}
	if err := service.placements.Create(ctx, placement.Placement{
		JobID: value.ID, WorkerID: peer.DeviceID, PlacedAt: value.CreatedAt,
	}); err != nil {
		return job.Job{}, fmt.Errorf(
			"%w: job %s is running on %s; use --device %s for job operations: %v",
			ErrRemotePlacementPersistence, value.ID, peer.Name, peer.DeviceID.Short(), err,
		)
	}
	return value, nil
}

func (service *RemoteJobService) Get(
	ctx context.Context,
	selector string,
	id job.ID,
) (job.Job, error) {
	peer, err := service.resolveJobWorker(ctx, selector, id)
	if err != nil {
		return job.Job{}, err
	}
	response, err := service.call(ctx, peer, &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_GetJob{GetJob: &computehopv1.GetJobRequest{JobId: string(id)}},
	})
	if err != nil {
		return job.Job{}, err
	}
	result := response.GetGetJob()
	if result == nil {
		return job.Job{}, remoteprotocol.ErrInvalidMessage
	}
	return mapper.JobFromRemoteProto(result.GetJob())
}

func (service *RemoteJobService) List(
	ctx context.Context,
	selector string,
	options job.ListOptions,
) ([]job.Job, error) {
	states, err := mapper.StatesToRemoteProto(options.States)
	if err != nil {
		return nil, err
	}
	peer, err := service.resolveTrustedWorker(ctx, selector)
	if err != nil {
		return nil, err
	}
	response, err := service.call(ctx, peer, &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_ListJobs{ListJobs: &computehopv1.ListJobsRequest{
			States: states, Limit: uint32(options.Limit),
		}},
	})
	if err != nil {
		return nil, err
	}
	result := response.GetListJobs()
	if result == nil {
		return nil, remoteprotocol.ErrInvalidMessage
	}
	values := make([]job.Job, len(result.GetJobs()))
	for index, message := range result.GetJobs() {
		values[index], err = mapper.JobFromRemoteProto(message)
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func (service *RemoteJobService) Cancel(
	ctx context.Context,
	selector string,
	id job.ID,
) (job.Job, error) {
	peer, err := service.resolveJobWorker(ctx, selector, id)
	if err != nil {
		return job.Job{}, err
	}
	response, err := service.call(ctx, peer, &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_CancelJob{CancelJob: &computehopv1.CancelJobRequest{JobId: string(id)}},
	})
	if err != nil {
		return job.Job{}, err
	}
	result := response.GetCancelJob()
	if result == nil {
		return job.Job{}, remoteprotocol.ErrInvalidMessage
	}
	return mapper.JobFromRemoteProto(result.GetJob())
}

func (service *RemoteJobService) ReadLogs(
	ctx context.Context,
	selector string,
	id job.ID,
	after uint64,
	limit int,
) (worker.JobLogs, error) {
	if limit < 0 || limit > joblogging.MaximumPageLimit {
		return worker.JobLogs{}, joblogging.ErrInvalidPage
	}
	peer, err := service.resolveJobWorker(ctx, selector, id)
	if err != nil {
		return worker.JobLogs{}, err
	}
	response, err := service.call(ctx, peer, &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_ReadJobLogs{ReadJobLogs: &computehopv1.ReadJobLogsRequest{
			JobId: string(id), AfterSequence: after, Limit: uint32(limit),
		}},
	})
	if err != nil {
		return worker.JobLogs{}, err
	}
	result := response.GetReadJobLogs()
	if result == nil {
		return worker.JobLogs{}, remoteprotocol.ErrInvalidMessage
	}
	value, err := mapper.JobFromRemoteProto(result.GetJob())
	if err != nil {
		return worker.JobLogs{}, err
	}
	records, err := mapper.LogRecordsFromRemoteProto(result.GetRecords())
	if err != nil {
		return worker.JobLogs{}, err
	}
	if len(records) > 0 && records[0].Sequence <= after {
		return worker.JobLogs{}, joblogging.ErrInvalidRecord
	}
	return worker.JobLogs{
		Job: value, Page: joblogging.Page{Records: records, HasMore: result.GetHasMore()},
	}, nil
}

func (service *RemoteJobService) call(
	ctx context.Context,
	peer trust.Peer,
	request *computehopv1.RemoteRequest,
) (*computehopv1.RemoteResponse, error) {
	candidates, err := service.nearbyCandidates(ctx, peer)
	if err != nil {
		return nil, err
	}
	dialContext := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		dialContext, cancel = context.WithTimeout(ctx, remoteDialTimeout)
		defer cancel()
	}
	var failures []error
	for _, candidate := range candidates {
		caller, dialErr := service.dialer.DialRemote(dialContext, candidate, peer)
		if dialErr != nil {
			failures = append(failures, dialErr)
			continue
		}
		response, callErr := caller.Call(ctx, request)
		closeErr := caller.Close()
		if callErr != nil {
			return nil, errors.Join(callErr, closeErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return response, nil
	}
	return nil, fmt.Errorf("%w: %s: %v", ErrRemoteWorkerUnavailable, peer.Name, errors.Join(failures...))
}

func (service *RemoteJobService) resolveJobWorker(
	ctx context.Context,
	selector string,
	id job.ID,
) (trust.Peer, error) {
	if strings.TrimSpace(selector) != "" {
		return service.resolveTrustedWorker(ctx, selector)
	}
	remembered, err := service.placements.Get(ctx, id)
	if errors.Is(err, placement.ErrNotFound) {
		return trust.Peer{}, fmt.Errorf("%w: %s", job.ErrNotFound, id)
	}
	if err != nil {
		return trust.Peer{}, err
	}
	peer, err := service.trust.Get(ctx, remembered.WorkerID)
	if err != nil {
		if !errors.Is(err, trust.ErrNotFound) {
			return trust.Peer{}, err
		}
		return trust.Peer{}, fmt.Errorf(
			"%w: remembered worker %s is not actively paired",
			ErrRemoteWorkerUnavailable, remembered.WorkerID.Short(),
		)
	}
	if peer.State != trust.StateActive || peer.Role != device.RoleWorker {
		return trust.Peer{}, fmt.Errorf(
			"%w: remembered worker %s is not actively paired",
			ErrRemoteWorkerUnavailable, remembered.WorkerID.Short(),
		)
	}
	return peer, nil
}

func (service *RemoteJobService) resolveTrustedWorker(ctx context.Context, selector string) (trust.Peer, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return trust.Peer{}, trust.ErrNotFound
	}
	peers, err := service.trust.List(ctx)
	if err != nil {
		return trust.Peer{}, err
	}
	matches := make([]trust.Peer, 0, 1)
	for _, peer := range peers {
		id := string(peer.DeviceID)
		if peer.State == trust.StateActive && peer.Role == device.RoleWorker &&
			(peer.Name == selector || id == selector || strings.HasPrefix(id, selector)) {
			matches = append(matches, peer)
		}
	}
	switch len(matches) {
	case 0:
		return trust.Peer{}, fmt.Errorf("%w: active worker %s", trust.ErrNotFound, selector)
	case 1:
		return matches[0], nil
	default:
		return trust.Peer{}, fmt.Errorf("%w: %s matches %d active workers", trust.ErrConflict, selector, len(matches))
	}
}

func (service *RemoteJobService) nearbyCandidates(ctx context.Context, peer trust.Peer) ([]device.NearbyDevice, error) {
	snapshot, err := service.nearby.ListNearby(ctx)
	if err != nil {
		return nil, err
	}
	candidates := make([]device.NearbyDevice, 0, 1)
	for _, nearby := range snapshot.Devices {
		if nearby.Announcement.Name == peer.Name && nearby.Announcement.Role == device.RoleWorker &&
			nearby.Announcement.EndpointReady {
			candidates = append(candidates, nearby)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrRemoteWorkerUnavailable, peer.Name)
	}
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].SeenAt.After(candidates[right].SeenAt)
	})
	return candidates, nil
}
