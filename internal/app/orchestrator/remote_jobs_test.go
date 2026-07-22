package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/artifact"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/placement"
	"github.com/austinjiann/spare-compute/internal/protocol/mapper"
	remoteprotocol "github.com/austinjiann/spare-compute/internal/protocol/remote"
	"github.com/austinjiann/spare-compute/internal/snapshot"
	"github.com/austinjiann/spare-compute/internal/trust"
)

func TestRemoteJobServiceRoutesOnlyThroughSelectedWorkerPin(t *testing.T) {
	peer := activeWorkerPeer(t, 8, "Gaming PC")
	nearby := nearbyWorker(t, "Gaming PC", 47823)
	want := queuedJobForTest()
	message, err := mapper.JobToRemoteProto(want)
	if err != nil {
		t.Fatal(err)
	}
	dialed := false
	placements := newRemotePlacementStub()
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby: stubDeviceController{list: func(context.Context) (device.DiscoverySnapshot, error) {
			return device.DiscoverySnapshot{Available: true, Devices: []device.NearbyDevice{nearby}}, nil
		}},
		Trust:      remoteTrustStub{peers: []trust.Peer{peer}},
		Placements: placements,
		Dialer: remoteDialerFunc(func(
			_ context.Context,
			target device.NearbyDevice,
			pinned trust.Peer,
		) (remoteprotocol.Caller, error) {
			dialed = true
			if target.Announcement.PresenceID != nearby.Announcement.PresenceID ||
				pinned.DeviceID != peer.DeviceID {
				t.Fatalf("target = %#v; peer = %#v", target, pinned)
			}
			return &remoteCallerStub{call: func(
				_ context.Context,
				request *computehopv1.RemoteRequest,
			) (*computehopv1.RemoteResponse, error) {
				switch request.GetOperation().(type) {
				case *computehopv1.RemoteRequest_SubmitJob:
					if request.GetSubmitJob().GetSpec().GetExecutable() != "echo" {
						t.Fatalf("request = %#v", request)
					}
					return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_SubmitJob{
						SubmitJob: &computehopv1.SubmitJobResponse{Job: message},
					}}, nil
				case *computehopv1.RemoteRequest_GetJob:
					return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_GetJob{
						GetJob: &computehopv1.GetJobResponse{Job: message},
					}}, nil
				default:
					t.Fatalf("unexpected request = %#v", request)
					return nil, nil
				}
			}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Submit(context.Background(), "Gaming PC", want.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if !dialed || got.ID != want.ID || got.State != want.State {
		t.Fatalf("job = %#v; dialed = %t", got, dialed)
	}
	remembered, err := placements.Get(context.Background(), want.ID)
	if err != nil || remembered.WorkerID != peer.DeviceID {
		t.Fatalf("placement = %#v, %v", remembered, err)
	}
	got, err = service.Get(context.Background(), "", want.ID)
	if err != nil || got.ID != want.ID {
		t.Fatalf("Get(remembered) = %#v, %v", got, err)
	}
}

func TestRemoteJobServiceRequiresAnActiveNearbyPin(t *testing.T) {
	peer := activeWorkerPeer(t, 9, "Offline PC")
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby:     stubDeviceController{},
		Trust:      remoteTrustStub{peers: []trust.Peer{peer}},
		Placements: newRemotePlacementStub(),
		Dialer: remoteDialerFunc(func(
			context.Context,
			device.NearbyDevice,
			trust.Peer,
		) (remoteprotocol.Caller, error) {
			t.Fatal("offline worker was dialed")
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.List(context.Background(), "Offline PC", job.ListOptions{Limit: 10})
	if !errors.Is(err, ErrRemoteWorkerUnavailable) {
		t.Fatalf("error = %v", err)
	}
}

func TestRemoteJobServiceFallsBackToSupervisedPathWithoutLAN(t *testing.T) {
	peer := activeWorkerPeer(t, 11, "Remote PC")
	want := queuedJobForTest()
	message, err := mapper.JobToRemoteProto(want)
	if err != nil {
		t.Fatal(err)
	}
	remoteDialed := false
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby:     stubDeviceController{},
		Trust:      remoteTrustStub{peers: []trust.Peer{peer}},
		Placements: newRemotePlacementStub(),
		Dialer: remoteDialerFunc(func(
			context.Context,
			device.NearbyDevice,
			trust.Peer,
		) (remoteprotocol.Caller, error) {
			t.Fatal("LAN dialer received an absent observation")
			return nil, nil
		}),
		Remote: pairedRemoteDialerFunc(func(
			_ context.Context,
			pinned trust.Peer,
		) (remoteprotocol.Caller, error) {
			remoteDialed = true
			if pinned.DeviceID != peer.DeviceID {
				t.Fatalf("peer = %#v", pinned)
			}
			return &remoteCallerStub{call: func(
				_ context.Context,
				request *computehopv1.RemoteRequest,
			) (*computehopv1.RemoteResponse, error) {
				if request.GetListJobs() == nil {
					t.Fatalf("request = %#v", request)
				}
				return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_ListJobs{
					ListJobs: &computehopv1.ListJobsResponse{Jobs: []*computehopv1.Job{message}},
				}}, nil
			}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := service.List(context.Background(), "Remote PC", job.ListOptions{Limit: 10})
	if err != nil || !remoteDialed || len(jobs) != 1 || jobs[0].ID != want.ID {
		t.Fatalf("List() = %#v, %v; remote dialed = %t", jobs, err, remoteDialed)
	}
}

func TestRemoteJobServiceTransfersOnlyMissingSnapshotChunks(t *testing.T) {
	peer := activeWorkerPeer(t, 17, "Build PC")
	contents := []byte("package main\n")
	digest := snapshot.Sum(contents)
	manifest := snapshot.Manifest{
		Version: snapshot.ManifestVersion,
		Files: []snapshot.File{{
			Path: "src/main.go", Mode: 0o644, Size: int64(len(contents)),
			Chunks: []snapshot.Chunk{{Digest: digest, Size: uint32(len(contents))}},
		}},
		TotalBytes: int64(len(contents)),
	}
	want := queuedJobForTest()
	want.Spec.WorkingDirectory = "/worker/jobs/workspace/src"
	jobMessage, err := mapper.JobToRemoteProto(want)
	if err != nil {
		t.Fatal(err)
	}
	cached := false
	uploads := 0
	submissions := 0
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby: stubDeviceController{}, Trust: remoteTrustStub{peers: []trust.Peer{peer}},
		Placements: newRemotePlacementStub(),
		Dialer: remoteDialerFunc(func(context.Context, device.NearbyDevice, trust.Peer) (remoteprotocol.Caller, error) {
			t.Fatal("absent LAN path was dialed")
			return nil, nil
		}),
		Remote: pairedRemoteDialerFunc(func(context.Context, trust.Peer) (remoteprotocol.Caller, error) {
			return &remoteCallerStub{call: func(
				_ context.Context,
				request *computehopv1.RemoteRequest,
			) (*computehopv1.RemoteResponse, error) {
				switch request.GetOperation().(type) {
				case *computehopv1.RemoteRequest_CheckSnapshot:
					missing := []string(nil)
					if !cached {
						missing = []string{string(digest)}
					}
					return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_CheckSnapshot{
						CheckSnapshot: &computehopv1.CheckSnapshotResponse{MissingChunkDigests: missing},
					}}, nil
				case *computehopv1.RemoteRequest_PutChunk:
					put := request.GetPutChunk()
					if put.GetDigest() != string(digest) || string(put.GetData()) != string(contents) {
						t.Fatalf("put request = %#v", put)
					}
					uploads++
					cached = true
					return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_PutChunk{
						PutChunk: &computehopv1.PutChunkResponse{Digest: string(digest)},
					}}, nil
				case *computehopv1.RemoteRequest_SubmitJob:
					submitted := request.GetSubmitJob()
					if submitted.GetSnapshot() == nil || submitted.GetWorkingSubdirectory() != "src" ||
						submitted.GetSpec().GetWorkingDirectory() != "" {
						t.Fatalf("submit request = %#v", submitted)
					}
					submissions++
					return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_SubmitJob{
						SubmitJob: &computehopv1.SubmitJobResponse{Job: jobMessage},
					}}, nil
				default:
					t.Fatalf("unexpected request = %#v", request)
					return nil, nil
				}
			}}, nil
		}),
		Snapshots: projectSnapshotterStub{result: snapshot.Result{
			Root: "/local/project", WorkingSubdirectory: "src", Manifest: manifest,
		}},
		Content: snapshotContentStub{contents: map[snapshot.Digest][]byte{digest: contents}},
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := want.Spec.Clone()
	spec.WorkingDirectory = "/local/project/src"
	for run := 0; run < 2; run++ {
		if _, err := service.Submit(context.Background(), "Build PC", spec); err != nil {
			t.Fatalf("Submit(%d) error = %v", run, err)
		}
	}
	if uploads != 1 || submissions != 2 {
		t.Fatalf("uploads = %d, submissions = %d", uploads, submissions)
	}
}

func TestRemoteJobServiceDownloadsOnlyMissingArtifactChunksAndRestores(t *testing.T) {
	peer := activeWorkerPeer(t, 23, "Render PC")
	contents := []byte("rendered output")
	digest := snapshot.Sum(contents)
	manifest := snapshot.Manifest{
		Version: snapshot.ManifestVersion,
		Files: []snapshot.File{{
			Path: "dist/render.png", Mode: 0o644, Size: int64(len(contents)),
			Chunks: []snapshot.Chunk{{Digest: digest, Size: uint32(len(contents))}},
		}},
		TotalBytes: int64(len(contents)),
	}
	manifestMessage, err := mapper.ManifestToRemoteProto(manifest)
	if err != nil {
		t.Fatal(err)
	}
	want := queuedJobForTest()
	want.State = job.StateSucceeded
	want.Spec.Outputs = []string{"dist/render.png"}
	jobMessage, err := mapper.JobToRemoteProto(want)
	if err != nil {
		t.Fatal(err)
	}
	placements := newRemotePlacementStub()
	if err := placements.Create(context.Background(), placement.Placement{
		JobID: want.ID, WorkerID: peer.DeviceID, PlacedAt: want.CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	cached := false
	downloads := 0
	restores := 0
	destination := filepath.Join(t.TempDir(), "results")
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby: stubDeviceController{}, Trust: remoteTrustStub{peers: []trust.Peer{peer}},
		Placements: placements,
		Dialer: remoteDialerFunc(func(context.Context, device.NearbyDevice, trust.Peer) (remoteprotocol.Caller, error) {
			return nil, errors.New("no LAN path")
		}),
		Remote: pairedRemoteDialerFunc(func(context.Context, trust.Peer) (remoteprotocol.Caller, error) {
			return &remoteCallerStub{call: func(
				_ context.Context,
				request *computehopv1.RemoteRequest,
			) (*computehopv1.RemoteResponse, error) {
				switch request.GetOperation().(type) {
				case *computehopv1.RemoteRequest_GetJobArtifacts:
					return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_GetJobArtifacts{
						GetJobArtifacts: &computehopv1.GetJobArtifactsResponse{
							Job: jobMessage, Artifacts: manifestMessage,
							CollectedAtUnixNano: time.Unix(1_900_000_000, 0).UnixNano(),
						},
					}}, nil
				case *computehopv1.RemoteRequest_GetArtifactChunk:
					downloads++
					if request.GetGetArtifactChunk().GetDigest() != string(digest) {
						t.Fatalf("chunk request = %#v", request.GetGetArtifactChunk())
					}
					return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_GetArtifactChunk{
						GetArtifactChunk: &computehopv1.GetArtifactChunkResponse{
							Digest: string(digest), Data: contents,
						},
					}}, nil
				default:
					t.Fatalf("unexpected request = %#v", request)
					return nil, nil
				}
			}}, nil
		}),
		ArtifactContent: artifactContentStub{
			missing: func(_ context.Context, digests []snapshot.Digest) ([]snapshot.Digest, error) {
				if len(digests) != 1 || digests[0] != digest {
					t.Fatalf("digests = %#v", digests)
				}
				if cached {
					return nil, nil
				}
				return []snapshot.Digest{digest}, nil
			},
			put: func(_ context.Context, got snapshot.Digest, data []byte) error {
				if got != digest || !bytes.Equal(data, contents) {
					t.Fatalf("Put(%s, %q)", got, data)
				}
				cached = true
				return nil
			},
		},
		Artifacts: artifactRestorerStub{restore: func(
			_ context.Context,
			bundle artifact.Bundle,
			gotDestination string,
		) (artifact.RestoreResult, error) {
			restores++
			if bundle.JobID != want.ID || gotDestination != destination {
				t.Fatalf("Restore(%s, %q)", bundle.JobID, gotDestination)
			}
			return artifact.RestoreResult{Destination: destination, Restored: []string{"dist/render.png"}}, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := service.FetchArtifacts(context.Background(), "", want.ID, destination)
		if err != nil || len(result.Restored) != 1 {
			t.Fatalf("FetchArtifacts(%d) = %#v, %v", attempt, result, err)
		}
	}
	if downloads != 1 || restores != 2 {
		t.Fatalf("downloads = %d, restores = %d", downloads, restores)
	}
}

func TestRemoteJobServiceRejectsRevokedRememberedWorker(t *testing.T) {
	peer := activeWorkerPeer(t, 10, "Revoked PC")
	revokedAt := peer.UpdatedAt.Add(time.Minute)
	peer.State = trust.StateRevoked
	peer.UpdatedAt = revokedAt
	peer.RevokedAt = &revokedAt
	want := queuedJobForTest()
	placements := newRemotePlacementStub()
	if err := placements.Create(context.Background(), placement.Placement{
		JobID: want.ID, WorkerID: peer.DeviceID, PlacedAt: want.CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby:     stubDeviceController{},
		Trust:      remoteTrustStub{peers: []trust.Peer{peer}},
		Placements: placements,
		Dialer: remoteDialerFunc(func(
			context.Context,
			device.NearbyDevice,
			trust.Peer,
		) (remoteprotocol.Caller, error) {
			t.Fatal("revoked remembered worker was dialed")
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(context.Background(), "", want.ID); !errors.Is(err, ErrRemoteWorkerUnavailable) {
		t.Fatalf("Get() error = %v, want ErrRemoteWorkerUnavailable", err)
	}
}

func TestRemoteJobServiceReportsUnknownRememberedJob(t *testing.T) {
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby:     stubDeviceController{},
		Trust:      remoteTrustStub{},
		Placements: newRemotePlacementStub(),
		Dialer: remoteDialerFunc(func(
			context.Context,
			device.NearbyDevice,
			trust.Peer,
		) (remoteprotocol.Caller, error) {
			t.Fatal("unknown job dialed a worker")
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := queuedJobForTest()
	if _, err := service.Get(context.Background(), "", want.ID); !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("Get() error = %v, want job.ErrNotFound", err)
	}
}

type remoteDialerFunc func(context.Context, device.NearbyDevice, trust.Peer) (remoteprotocol.Caller, error)

func (function remoteDialerFunc) DialRemote(
	ctx context.Context,
	target device.NearbyDevice,
	peer trust.Peer,
) (remoteprotocol.Caller, error) {
	return function(ctx, target, peer)
}

type pairedRemoteDialerFunc func(context.Context, trust.Peer) (remoteprotocol.Caller, error)

func (function pairedRemoteDialerFunc) DialRemotePeer(
	ctx context.Context,
	peer trust.Peer,
) (remoteprotocol.Caller, error) {
	return function(ctx, peer)
}

type remoteCallerStub struct {
	call func(context.Context, *computehopv1.RemoteRequest) (*computehopv1.RemoteResponse, error)
}

type projectSnapshotterStub struct {
	result snapshot.Result
	err    error
}

func (stub projectSnapshotterStub) Build(context.Context, string) (snapshot.Result, error) {
	return stub.result, stub.err
}

type snapshotContentStub struct {
	contents map[snapshot.Digest][]byte
}

type artifactContentStub struct {
	missing func(context.Context, []snapshot.Digest) ([]snapshot.Digest, error)
	put     func(context.Context, snapshot.Digest, []byte) error
}

func (stub artifactContentStub) Missing(
	ctx context.Context,
	digests []snapshot.Digest,
) ([]snapshot.Digest, error) {
	return stub.missing(ctx, digests)
}

func (stub artifactContentStub) Put(ctx context.Context, digest snapshot.Digest, data []byte) error {
	return stub.put(ctx, digest, data)
}

type artifactRestorerStub struct {
	restore func(context.Context, artifact.Bundle, string) (artifact.RestoreResult, error)
}

func (stub artifactRestorerStub) Restore(
	ctx context.Context,
	bundle artifact.Bundle,
	destination string,
) (artifact.RestoreResult, error) {
	return stub.restore(ctx, bundle, destination)
}

func (stub snapshotContentStub) Read(_ context.Context, digest snapshot.Digest) ([]byte, error) {
	contents, ok := stub.contents[digest]
	if !ok {
		return nil, errors.New("missing local content")
	}
	return append([]byte(nil), contents...), nil
}

func (caller *remoteCallerStub) Call(
	ctx context.Context,
	request *computehopv1.RemoteRequest,
) (*computehopv1.RemoteResponse, error) {
	return caller.call(ctx, request)
}

func (*remoteCallerStub) Close() error { return nil }

type remoteTrustStub struct {
	peers []trust.Peer
}

func (stub remoteTrustStub) Activate(context.Context, trust.Peer) error {
	return errors.New("not implemented")
}
func (stub remoteTrustStub) Get(_ context.Context, id device.ID) (trust.Peer, error) {
	for _, peer := range stub.peers {
		if peer.DeviceID == id {
			return peer.Clone(), nil
		}
	}
	return trust.Peer{}, trust.ErrNotFound
}
func (stub remoteTrustStub) List(context.Context) ([]trust.Peer, error) {
	result := make([]trust.Peer, len(stub.peers))
	for index, peer := range stub.peers {
		result[index] = peer.Clone()
	}
	return result, nil
}
func (stub remoteTrustStub) Revoke(context.Context, device.ID, time.Time) (trust.Peer, error) {
	return trust.Peer{}, errors.New("not implemented")
}

type remotePlacementStub struct {
	values map[job.ID]placement.Placement
}

func newRemotePlacementStub() *remotePlacementStub {
	return &remotePlacementStub{values: make(map[job.ID]placement.Placement)}
}

func (stub *remotePlacementStub) Create(_ context.Context, value placement.Placement) error {
	if err := value.Validate(); err != nil {
		return err
	}
	if existing, ok := stub.values[value.JobID]; ok && existing.WorkerID != value.WorkerID {
		return placement.ErrConflict
	}
	stub.values[value.JobID] = value
	return nil
}

func (stub *remotePlacementStub) Get(_ context.Context, id job.ID) (placement.Placement, error) {
	value, ok := stub.values[id]
	if !ok {
		return placement.Placement{}, placement.ErrNotFound
	}
	return value, nil
}

func activeWorkerPeer(t *testing.T, seed byte, name string) trust.Peer {
	t.Helper()
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{seed}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{seed + 1}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	return trust.Peer{
		PairID: pairID, DeviceID: identity.ID(), PublicKey: identity.PublicKey(),
		Name: name, Role: device.RoleWorker, State: trust.StateActive,
		PairedAt: now, UpdatedAt: now,
	}
}

func nearbyWorker(t *testing.T, name string, port uint16) device.NearbyDevice {
	t.Helper()
	presence, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{6}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	observation := device.Observation{
		Key: "worker", Announcement: device.Announcement{
			PresenceID: presence, Name: name, Role: device.RoleWorker,
			ProtocolVersion: device.DiscoveryProtocolVersion, Port: port, EndpointReady: true,
		},
		Instance: name, HostName: "worker.local.", Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.10")},
		SeenAt: now, ExpiresAt: now.Add(time.Minute),
	}
	return device.NearbyDevice{Observation: observation, FirstSeenAt: now}
}
