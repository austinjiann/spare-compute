package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/app/worker"
	"github.com/austinjiann/spare-compute/internal/artifact"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/placement"
	"github.com/austinjiann/spare-compute/internal/protocol/mapper"
	remoteprotocol "github.com/austinjiann/spare-compute/internal/protocol/remote"
	"github.com/austinjiann/spare-compute/internal/snapshot"
	"github.com/austinjiann/spare-compute/internal/transfer"
	"github.com/austinjiann/spare-compute/internal/trust"
)

const (
	remoteDialTimeout      = 12 * time.Second
	snapshotPreflightBatch = 4_096
)

var (
	ErrMissingRemoteDependency    = errors.New("remote job service dependency is required")
	ErrRemoteWorkerUnavailable    = errors.New("paired worker is unavailable")
	ErrRemotePlacementPersistence = errors.New("remote job was accepted but its placement could not be saved")
	ErrRemoteSnapshotUnavailable  = errors.New("remote project snapshot is unavailable")
)

// RemoteDialer opens a key-pinned job-control connection to one LAN observation.
type RemoteDialer interface {
	DialRemote(context.Context, device.NearbyDevice, trust.Peer) (remoteprotocol.Caller, error)
}

// PairedRemoteDialer opens a key-pinned connection over a supervised non-LAN
// path for the selected durable peer.
type PairedRemoteDialer interface {
	DialRemotePeer(context.Context, trust.Peer) (remoteprotocol.Caller, error)
}

// ProjectSnapshotter builds a stable project manifest into the local cache.
type ProjectSnapshotter interface {
	Build(context.Context, string) (snapshot.Result, error)
}

// SnapshotContent loads one locally verified chunk for transfer.
type SnapshotContent interface {
	Read(context.Context, snapshot.Digest) ([]byte, error)
}

// ArtifactContent receives verified chunks returned by a worker.
type ArtifactContent interface {
	Missing(context.Context, []snapshot.Digest) ([]snapshot.Digest, error)
	Put(context.Context, snapshot.Digest, []byte) error
}

// ArtifactRestorer stages and places a verified bundle without overwrites.
type ArtifactRestorer interface {
	Restore(context.Context, artifact.Bundle, string) (artifact.RestoreResult, error)
}

// RemoteDependencies configure explicit paired-worker job routing.
type RemoteDependencies struct {
	Nearby          DeviceController
	Trust           trust.Repository
	Placements      placement.Repository
	Dialer          RemoteDialer
	Remote          PairedRemoteDialer
	Snapshots       ProjectSnapshotter
	Content         SnapshotContent
	ArtifactContent ArtifactContent
	Artifacts       ArtifactRestorer
}

// RemoteJobService prefers current LAN observations, then falls back to a
// supervised path. Every route must match the durable worker identity pin.
type RemoteJobService struct {
	nearby          DeviceController
	trust           trust.Repository
	placements      placement.Repository
	dialer          RemoteDialer
	remote          PairedRemoteDialer
	snapshots       ProjectSnapshotter
	content         SnapshotContent
	artifactContent ArtifactContent
	artifacts       ArtifactRestorer
}

// NewRemoteJobService constructs the orchestrator-side remote job controller.
func NewRemoteJobService(dependencies RemoteDependencies) (*RemoteJobService, error) {
	if dependencies.Nearby == nil || dependencies.Trust == nil ||
		dependencies.Placements == nil || dependencies.Dialer == nil ||
		(dependencies.Snapshots == nil) != (dependencies.Content == nil) {
		return nil, ErrMissingRemoteDependency
	}
	if (dependencies.ArtifactContent == nil) != (dependencies.Artifacts == nil) {
		return nil, ErrMissingRemoteDependency
	}
	return &RemoteJobService{
		nearby: dependencies.Nearby, trust: dependencies.Trust,
		placements: dependencies.Placements, dialer: dependencies.Dialer,
		remote:    dependencies.Remote,
		snapshots: dependencies.Snapshots, content: dependencies.Content,
		artifactContent: dependencies.ArtifactContent, artifacts: dependencies.Artifacts,
	}, nil
}

// FetchArtifacts downloads only missing verified output chunks and restores
// them without overwriting local files.
func (service *RemoteJobService) FetchArtifacts(
	ctx context.Context,
	selector string,
	id job.ID,
	destination string,
) (artifact.RestoreResult, error) {
	if service.artifactContent == nil || service.artifacts == nil {
		return artifact.RestoreResult{}, worker.ErrArtifactsDisabled
	}
	peer, err := service.resolveJobWorker(ctx, selector, id)
	if err != nil {
		return artifact.RestoreResult{}, err
	}
	caller, err := service.open(ctx, peer)
	if err != nil {
		return artifact.RestoreResult{}, err
	}
	defer caller.Close()
	response, err := caller.Call(ctx, &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_GetJobArtifacts{GetJobArtifacts: &computehopv1.GetJobArtifactsRequest{
			JobId: string(id),
		}},
	})
	if err != nil {
		return artifact.RestoreResult{}, err
	}
	result := response.GetGetJobArtifacts()
	if result == nil || result.GetArtifacts() == nil || result.GetCollectedAtUnixNano() <= 0 {
		return artifact.RestoreResult{}, remoteprotocol.ErrInvalidMessage
	}
	current, err := mapper.JobFromRemoteProto(result.GetJob())
	if err != nil || current.ID != id || current.State != job.StateSucceeded {
		return artifact.RestoreResult{}, remoteprotocol.ErrInvalidMessage
	}
	manifest, err := mapper.ManifestFromRemoteProto(result.GetArtifacts())
	if err != nil {
		return artifact.RestoreResult{}, err
	}
	bundle := artifact.Bundle{
		JobID: id, Manifest: manifest,
		CollectedAt: time.Unix(0, result.GetCollectedAtUnixNano()).UTC(),
	}
	if err := bundle.Validate(); err != nil {
		return artifact.RestoreResult{}, err
	}
	missing, err := service.artifactContent.Missing(ctx, manifest.Digests())
	if err != nil {
		return artifact.RestoreResult{}, err
	}
	acceptedEncodings, err := mapper.ChunkEncodingsToRemoteProto(transfer.SupportedChunkEncodings())
	if err != nil {
		return artifact.RestoreResult{}, err
	}
	for _, digest := range missing {
		response, err := caller.Call(ctx, &computehopv1.RemoteRequest{
			Operation: &computehopv1.RemoteRequest_GetArtifactChunk{GetArtifactChunk: &computehopv1.GetArtifactChunkRequest{
				JobId: string(id), Digest: string(digest), AcceptedEncodings: acceptedEncodings,
			}},
		})
		if err != nil {
			return artifact.RestoreResult{}, fmt.Errorf("download artifact chunk %s: %w", digest, err)
		}
		chunk := response.GetGetArtifactChunk()
		if chunk == nil || chunk.GetDigest() != string(digest) {
			return artifact.RestoreResult{}, remoteprotocol.ErrInvalidMessage
		}
		encoding, err := mapper.ChunkEncodingFromRemoteProto(chunk.GetEncoding())
		if err != nil {
			return artifact.RestoreResult{}, remoteprotocol.ErrInvalidMessage
		}
		contents, err := transfer.DecodeChunk(transfer.Chunk{
			Encoding: encoding, Data: chunk.GetData(), UncompressedSize: chunk.GetUncompressedSize(),
		})
		if err != nil || snapshot.Sum(contents) != digest {
			return artifact.RestoreResult{}, remoteprotocol.ErrInvalidMessage
		}
		if err := service.artifactContent.Put(ctx, digest, contents); err != nil {
			return artifact.RestoreResult{}, err
		}
	}
	return service.artifacts.Restore(ctx, bundle, destination)
}

func (service *RemoteJobService) Submit(
	ctx context.Context,
	selector string,
	spec job.Spec,
) (job.Job, error) {
	peer, err := service.resolveTrustedWorker(ctx, selector)
	if err != nil {
		return job.Job{}, err
	}
	if service.snapshots != nil {
		return service.submitSnapshot(ctx, peer, spec)
	}
	message, err := mapper.SpecToRemoteProto(spec)
	if err != nil {
		return job.Job{}, err
	}
	response, err := service.call(ctx, peer, &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_SubmitJob{SubmitJob: &computehopv1.SubmitJobRequest{Spec: message}},
	})
	if err != nil {
		return job.Job{}, err
	}
	return service.acceptSubmitted(ctx, peer, response)
}

func (service *RemoteJobService) submitSnapshot(
	ctx context.Context,
	peer trust.Peer,
	spec job.Spec,
) (job.Job, error) {
	if strings.TrimSpace(spec.WorkingDirectory) == "" {
		return job.Job{}, fmt.Errorf(
			"%w: choose the local project folder with --working-directory",
			ErrRemoteSnapshotUnavailable,
		)
	}
	project, err := service.snapshots.Build(ctx, spec.WorkingDirectory)
	if err != nil {
		return job.Job{}, fmt.Errorf("create project snapshot: %w", err)
	}
	manifestMessage, err := mapper.ManifestToRemoteProto(project.Manifest)
	if err != nil {
		return job.Job{}, err
	}
	manifestID, err := project.Manifest.ID()
	if err != nil {
		return job.Job{}, err
	}
	caller, err := service.open(ctx, peer)
	if err != nil {
		return job.Job{}, err
	}
	defer caller.Close()
	missing, accepted, err := service.preflightSnapshot(ctx, caller, manifestID, project.Manifest.Digests())
	if err != nil {
		return job.Job{}, err
	}
	for _, digest := range missing {
		contents, err := service.content.Read(ctx, digest)
		if err != nil {
			return job.Job{}, fmt.Errorf("read project chunk %s: %w", digest, err)
		}
		encoded, err := transfer.EncodeChunk(contents, accepted)
		if err != nil {
			return job.Job{}, fmt.Errorf("encode project chunk %s: %w", digest, err)
		}
		encoding, err := mapper.ChunkEncodingToRemoteProto(encoded.Encoding)
		if err != nil {
			return job.Job{}, err
		}
		response, err := caller.Call(ctx, &computehopv1.RemoteRequest{
			Operation: &computehopv1.RemoteRequest_PutChunk{PutChunk: &computehopv1.PutChunkRequest{
				Digest: string(digest), Data: encoded.Data,
				Encoding: encoding, UncompressedSize: encoded.UncompressedSize,
			}},
		})
		if err != nil {
			return job.Job{}, fmt.Errorf("transfer project chunk %s: %w", digest, err)
		}
		if response.GetPutChunk() == nil || response.GetPutChunk().GetDigest() != string(digest) {
			return job.Job{}, remoteprotocol.ErrInvalidMessage
		}
	}
	remoteSpec := spec.Clone()
	remoteSpec.WorkingDirectory = ""
	specMessage, err := mapper.SpecToRemoteProto(remoteSpec)
	if err != nil {
		return job.Job{}, err
	}
	response, err := caller.Call(ctx, &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_SubmitJob{SubmitJob: &computehopv1.SubmitJobRequest{
			Spec: specMessage, Snapshot: manifestMessage,
			WorkingSubdirectory: project.WorkingSubdirectory,
		}},
	})
	if err != nil {
		return job.Job{}, err
	}
	return service.acceptSubmitted(ctx, peer, response)
}

func (service *RemoteJobService) preflightSnapshot(
	ctx context.Context,
	caller remoteprotocol.Caller,
	manifestID snapshot.Digest,
	digests []snapshot.Digest,
) ([]snapshot.Digest, []transfer.ChunkEncoding, error) {
	missing := make(map[snapshot.Digest]struct{})
	var accepted []transfer.ChunkEncoding
	for offset := 0; offset < len(digests); offset += snapshotPreflightBatch {
		end := min(offset+snapshotPreflightBatch, len(digests))
		encoded := make([]string, end-offset)
		allowed := make(map[snapshot.Digest]struct{}, end-offset)
		for index, digest := range digests[offset:end] {
			encoded[index] = string(digest)
			allowed[digest] = struct{}{}
		}
		response, err := caller.Call(ctx, &computehopv1.RemoteRequest{
			Operation: &computehopv1.RemoteRequest_CheckSnapshot{CheckSnapshot: &computehopv1.CheckSnapshotRequest{
				ManifestId: string(manifestID), ChunkDigests: encoded,
			}},
		})
		if err != nil {
			return nil, nil, fmt.Errorf("preflight project snapshot: %w", err)
		}
		preflight := response.GetCheckSnapshot()
		if preflight == nil {
			return nil, nil, remoteprotocol.ErrInvalidMessage
		}
		batchAccepted, err := mapper.ChunkEncodingsFromRemoteProto(preflight.GetAcceptedChunkEncodings())
		if err != nil {
			return nil, nil, remoteprotocol.ErrInvalidMessage
		}
		if accepted == nil {
			accepted = batchAccepted
		} else if !slices.Equal(accepted, batchAccepted) {
			return nil, nil, remoteprotocol.ErrInvalidMessage
		}
		for _, value := range preflight.GetMissingChunkDigests() {
			digest, err := snapshot.ParseDigest(value)
			if err != nil {
				return nil, nil, remoteprotocol.ErrInvalidMessage
			}
			if _, ok := allowed[digest]; !ok {
				return nil, nil, remoteprotocol.ErrInvalidMessage
			}
			missing[digest] = struct{}{}
		}
	}
	result := make([]snapshot.Digest, 0, len(missing))
	for _, digest := range digests {
		if _, ok := missing[digest]; ok {
			result = append(result, digest)
		}
	}
	return result, accepted, nil
}

func (service *RemoteJobService) acceptSubmitted(
	ctx context.Context,
	peer trust.Peer,
	response *computehopv1.RemoteResponse,
) (job.Job, error) {
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
	caller, err := service.open(ctx, peer)
	if err != nil {
		return nil, err
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

func (service *RemoteJobService) open(
	ctx context.Context,
	peer trust.Peer,
) (remoteprotocol.Caller, error) {
	candidates, err := service.nearbyCandidates(ctx, peer)
	var failures []error
	if err != nil {
		failures = append(failures, fmt.Errorf("inspect LAN paths: %w", err))
	}
	dialContext := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		dialContext, cancel = context.WithTimeout(ctx, remoteDialTimeout)
		defer cancel()
	}
	for _, candidate := range candidates {
		caller, dialErr := service.dialer.DialRemote(dialContext, candidate, peer)
		if dialErr != nil {
			failures = append(failures, dialErr)
			continue
		}
		return caller, nil
	}
	if service.remote != nil {
		caller, dialErr := service.remote.DialRemotePeer(dialContext, peer)
		if dialErr != nil {
			failures = append(failures, dialErr)
		} else {
			return caller, nil
		}
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
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].SeenAt.After(candidates[right].SeenAt)
	})
	return candidates, nil
}
