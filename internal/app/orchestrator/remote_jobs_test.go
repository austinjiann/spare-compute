package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"
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
	"github.com/austinjiann/spare-compute/internal/transfer"
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
				case *computehopv1.RemoteRequest_GetWorkerStatus:
					return workerStatusResponse("linux", "amd64", 8, 16<<30, "echo"), nil
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

func TestRemoteJobServiceAutoSelectsSingleActiveWorker(t *testing.T) {
	peer := activeWorkerPeer(t, 10, "Build PC")
	nearby := nearbyWorker(t, "Build PC", 47823)
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
			if target.Announcement.Name != "Build PC" || pinned.DeviceID != peer.DeviceID {
				t.Fatalf("target = %#v; peer = %#v", target, pinned)
			}
			return &remoteCallerStub{call: func(
				_ context.Context,
				request *computehopv1.RemoteRequest,
			) (*computehopv1.RemoteResponse, error) {
				if request.GetGetWorkerStatus() != nil {
					return workerStatusResponse("linux", "amd64", 8, 16<<30, "echo"), nil
				}
				if request.GetSubmitJob() == nil {
					t.Fatalf("request = %#v", request)
				}
				return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_SubmitJob{
					SubmitJob: &computehopv1.SubmitJobResponse{Job: message},
				}}, nil
			}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Submit(context.Background(), "auto", want.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if !dialed || got.ID != want.ID {
		t.Fatalf("job = %#v; dialed = %t", got, dialed)
	}
	remembered, err := placements.Get(context.Background(), want.ID)
	if err != nil || remembered.WorkerID != peer.DeviceID {
		t.Fatalf("placement = %#v, %v", remembered, err)
	}
}

func TestRemoteJobServiceAutoSelectorChoosesHighestResourceWorker(t *testing.T) {
	buildPeer := activeWorkerPeer(t, 12, "Build PC")
	renderPeer := activeWorkerPeer(t, 13, "Render PC")
	want := queuedJobForTest()
	message, err := mapper.JobToRemoteProto(want)
	if err != nil {
		t.Fatal(err)
	}
	placements := newRemotePlacementStub()
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby: stubDeviceController{list: func(context.Context) (device.DiscoverySnapshot, error) {
			return device.DiscoverySnapshot{Available: true, Devices: []device.NearbyDevice{
				nearbyWorkerWithResources(t, "Build PC", 47823, 8, 16<<30),
				nearbyWorkerWithResources(t, "Render PC", 47824, 32, 64<<30),
			}}, nil
		}},
		Trust: remoteTrustStub{peers: []trust.Peer{
			buildPeer,
			renderPeer,
		}},
		Placements: placements,
		Dialer: remoteDialerFunc(func(
			_ context.Context,
			target device.NearbyDevice,
			pinned trust.Peer,
		) (remoteprotocol.Caller, error) {
			return &remoteCallerStub{call: func(
				_ context.Context,
				request *computehopv1.RemoteRequest,
			) (*computehopv1.RemoteResponse, error) {
				if request.GetGetWorkerStatus() != nil {
					if pinned.DeviceID == buildPeer.DeviceID {
						return workerStatusResponse("linux", "amd64", 8, 16<<30, "echo"), nil
					}
					if pinned.DeviceID == renderPeer.DeviceID {
						return workerStatusResponse("linux", "amd64", 32, 64<<30, "echo"), nil
					}
					t.Fatalf("status target = %#v; peer = %#v", target, pinned)
				}
				if request.GetSubmitJob() == nil || target.Announcement.Name != "Render PC" ||
					pinned.DeviceID != renderPeer.DeviceID {
					t.Fatalf("submit target = %#v; peer = %#v; request = %#v", target, pinned, request)
				}
				return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_SubmitJob{
					SubmitJob: &computehopv1.SubmitJobResponse{Job: message},
				}}, nil
			}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Submit(context.Background(), "auto", want.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID {
		t.Fatalf("job = %#v", got)
	}
	remembered, err := placements.Get(context.Background(), want.ID)
	if err != nil || remembered.WorkerID != renderPeer.DeviceID {
		t.Fatalf("placement = %#v, %v", remembered, err)
	}
}

func TestRemoteJobServiceAutoSelectorUsesAuthenticatedWorkerStatus(t *testing.T) {
	buildPeer := activeWorkerPeer(t, 12, "Build PC")
	renderPeer := activeWorkerPeer(t, 13, "Render PC")
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby: stubDeviceController{list: func(context.Context) (device.DiscoverySnapshot, error) {
			return device.DiscoverySnapshot{Available: true, Devices: []device.NearbyDevice{
				nearbyWorkerWithResources(t, "Build PC", 47823, 4, 8<<30),
				nearbyWorkerWithResources(t, "Render PC", 47824, 4, 8<<30),
			}}, nil
		}},
		Trust:      remoteTrustStub{peers: []trust.Peer{buildPeer, renderPeer}},
		Placements: newRemotePlacementStub(),
		Dialer: remoteDialerFunc(func(
			_ context.Context,
			_ device.NearbyDevice,
			pinned trust.Peer,
		) (remoteprotocol.Caller, error) {
			return &remoteCallerStub{call: func(
				_ context.Context,
				request *computehopv1.RemoteRequest,
			) (*computehopv1.RemoteResponse, error) {
				if request.GetGetWorkerStatus() == nil {
					t.Fatalf("request = %#v", request)
				}
				if pinned.DeviceID == buildPeer.DeviceID {
					return workerStatusResponse("linux", "amd64", 8, 16<<30), nil
				}
				if pinned.DeviceID == renderPeer.DeviceID {
					return workerStatusResponse("linux", "amd64", 32, 64<<30), nil
				}
				t.Fatalf("peer = %#v", pinned)
				return nil, nil
			}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.resolveTrustedWorker(context.Background(), "auto")
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceID != renderPeer.DeviceID {
		t.Fatalf("worker = %s, want %s", got.DeviceID.Short(), renderPeer.DeviceID.Short())
	}
}

func TestRemoteJobServiceAutoSubmitSkipsWorkersMissingPlannedTool(t *testing.T) {
	buildPeer := activeWorkerPeer(t, 12, "Build PC")
	renderPeer := activeWorkerPeer(t, 13, "Render PC")
	want := queuedJobForTest()
	want.Spec.Executable = "go"
	want.Spec.Arguments = []string{"test", "./..."}
	message, err := mapper.JobToRemoteProto(want)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby: stubDeviceController{list: func(context.Context) (device.DiscoverySnapshot, error) {
			return device.DiscoverySnapshot{Available: true, Devices: []device.NearbyDevice{
				nearbyWorkerWithResources(t, "Build PC", 47823, 64, 128<<30),
				nearbyWorkerWithResources(t, "Render PC", 47824, 8, 16<<30),
			}}, nil
		}},
		Trust:      remoteTrustStub{peers: []trust.Peer{buildPeer, renderPeer}},
		Placements: newRemotePlacementStub(),
		Dialer: remoteDialerFunc(func(
			_ context.Context,
			_ device.NearbyDevice,
			pinned trust.Peer,
		) (remoteprotocol.Caller, error) {
			return &remoteCallerStub{call: func(
				_ context.Context,
				request *computehopv1.RemoteRequest,
			) (*computehopv1.RemoteResponse, error) {
				if request.GetGetWorkerStatus() != nil {
					if pinned.DeviceID == buildPeer.DeviceID {
						return workerStatusResponse("linux", "amd64", 64, 128<<30, "node"), nil
					}
					if pinned.DeviceID == renderPeer.DeviceID {
						return workerStatusResponse("linux", "amd64", 8, 16<<30, "go"), nil
					}
				}
				if request.GetSubmitJob() == nil || pinned.DeviceID != renderPeer.DeviceID {
					t.Fatalf("request = %#v; peer = %s", request, pinned.DeviceID.Short())
				}
				return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_SubmitJob{
					SubmitJob: &computehopv1.SubmitJobResponse{Job: message},
				}}, nil
			}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Submit(context.Background(), "auto", want.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID {
		t.Fatalf("job = %#v", got)
	}
}

func TestRemoteJobServiceAutoSubmitRejectsUnsupportedExecutor(t *testing.T) {
	buildPeer := activeWorkerPeer(t, 52, "Build PC")
	renderPeer := activeWorkerPeer(t, 53, "Render PC")
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby: stubDeviceController{list: func(context.Context) (device.DiscoverySnapshot, error) {
			return device.DiscoverySnapshot{Available: true, Devices: []device.NearbyDevice{
				nearbyWorkerWithResources(t, "Build PC", 47823, 64, 128<<30),
				nearbyWorkerWithResources(t, "Render PC", 47824, 8, 16<<30),
			}}, nil
		}},
		Trust:      remoteTrustStub{peers: []trust.Peer{buildPeer, renderPeer}},
		Placements: newRemotePlacementStub(),
		Dialer: remoteDialerFunc(func(
			_ context.Context,
			_ device.NearbyDevice,
			_ trust.Peer,
		) (remoteprotocol.Caller, error) {
			return &remoteCallerStub{call: func(
				_ context.Context,
				request *computehopv1.RemoteRequest,
			) (*computehopv1.RemoteResponse, error) {
				if request.GetGetWorkerStatus() == nil {
					t.Fatalf("request = %#v", request)
				}
				return workerStatusResponse("linux", "amd64", 8, 16<<30, "docker"), nil
			}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := queuedJobForTest().Spec.Clone()
	spec.Executable = "docker"
	spec.Arguments = []string{"run", "alpine", "echo", "hello"}
	spec.Executor = job.ExecutorContainer
	spec.ContainerImage = "alpine:latest"
	_, err = service.Submit(context.Background(), "auto", spec)
	if !errors.Is(err, ErrRemoteWorkerIncompatible) ||
		!strings.Contains(err.Error(), "no active paired worker supports container execution") {
		t.Fatalf("error = %v", err)
	}
}

func TestRemoteJobServiceSubmitRejectsSelectedWorkerMissingPlannedToolBeforeSnapshot(t *testing.T) {
	peer := activeWorkerPeer(t, 14, "Node PC")
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby:     stubDeviceController{},
		Trust:      remoteTrustStub{peers: []trust.Peer{peer}},
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
				if request.GetGetWorkerStatus() == nil {
					t.Fatalf("request = %#v", request)
				}
				return workerStatusResponse("linux", "amd64", 8, 16<<30, "node"), nil
			}}, nil
		}),
		Snapshots: projectSnapshotterStub{err: errors.New("snapshot should not be built")},
		Content:   snapshotContentStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := queuedJobForTest().Spec.Clone()
	spec.Executable = "go"
	spec.Arguments = []string{"test", "./..."}
	spec.WorkingDirectory = "/local/project"
	_, err = service.Submit(context.Background(), "Node PC", spec)
	if !errors.Is(err, ErrRemoteWorkerIncompatible) ||
		!strings.Contains(err.Error(), "Node PC does not report Go") ||
		!strings.Contains(err.Error(), "install Go") {
		t.Fatalf("error = %v", err)
	}
}

func TestRemoteJobServiceSubmitRejectsSelectedWorkerUnsupportedExecutorBeforeSnapshot(t *testing.T) {
	peer := activeWorkerPeer(t, 15, "Native Worker")
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby:     stubDeviceController{},
		Trust:      remoteTrustStub{peers: []trust.Peer{peer}},
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
				if request.GetGetWorkerStatus() == nil {
					t.Fatalf("request = %#v", request)
				}
				return workerStatusResponse("linux", "amd64", 8, 16<<30, "docker"), nil
			}}, nil
		}),
		Snapshots: projectSnapshotterStub{err: errors.New("snapshot should not be built")},
		Content:   snapshotContentStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := queuedJobForTest().Spec.Clone()
	spec.Executable = "docker"
	spec.Arguments = []string{"run", "alpine", "echo", "hello"}
	spec.Executor = job.ExecutorContainer
	spec.ContainerImage = "alpine:latest"
	spec.WorkingDirectory = "/local/project"
	_, err = service.Submit(context.Background(), "Native Worker", spec)
	if !errors.Is(err, ErrRemoteWorkerIncompatible) ||
		!strings.Contains(err.Error(), "Native Worker does not support container execution") {
		t.Fatalf("error = %v", err)
	}
}

func TestRemoteJobServiceSubmitRejectsSelectedWorkerMissingRequiredToolsBeforeSnapshot(t *testing.T) {
	peer := activeWorkerPeer(t, 16, "Small Builder")
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby:     stubDeviceController{},
		Trust:      remoteTrustStub{peers: []trust.Peer{peer}},
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
				if request.GetGetWorkerStatus() == nil {
					t.Fatalf("request = %#v", request)
				}
				return workerStatusResponse("linux", "amd64", 8, 16<<30, "make"), nil
			}}, nil
		}),
		Snapshots: projectSnapshotterStub{err: errors.New("snapshot should not be built")},
		Content:   snapshotContentStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := queuedJobForTest().Spec.Clone()
	spec.Executable = "make"
	spec.Arguments = []string{"pr-check"}
	spec.RequiredToolIDs = []string{"docker", "go", "make"}
	spec.WorkingDirectory = "/local/project"
	_, err = service.Submit(context.Background(), "Small Builder", spec)
	if !errors.Is(err, ErrRemoteWorkerIncompatible) ||
		!strings.Contains(err.Error(), "Small Builder does not report Docker and Go") ||
		!strings.Contains(err.Error(), "install Docker and Go") {
		t.Fatalf("error = %v", err)
	}
}

func TestRemoteJobServiceAutoSelectorUsesCachedResourceHints(t *testing.T) {
	buildPeer := activeWorkerPeer(t, 12, "Build PC")
	renderPeer := activeWorkerPeer(t, 13, "Render PC")
	buildPeer = peerWithResourceHints(buildPeer, 8, 16<<30)
	renderPeer = peerWithResourceHints(renderPeer, 32, 64<<30)
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby:     stubDeviceController{},
		Trust:      remoteTrustStub{peers: []trust.Peer{buildPeer, renderPeer}},
		Placements: newRemotePlacementStub(),
		Dialer: remoteDialerFunc(func(
			context.Context,
			device.NearbyDevice,
			trust.Peer,
		) (remoteprotocol.Caller, error) {
			t.Fatal("cached selector resolution should not open a remote connection")
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.resolveTrustedWorker(context.Background(), "auto")
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceID != renderPeer.DeviceID {
		t.Fatalf("worker = %s, want %s", got.DeviceID.Short(), renderPeer.DeviceID.Short())
	}
}

func TestRemoteJobServiceAutoSelectorBreaksTiesByStableDeviceID(t *testing.T) {
	left := activeWorkerPeer(t, 12, "Build PC")
	right := activeWorkerPeer(t, 13, "Render PC")
	want := left
	if string(right.DeviceID) < string(left.DeviceID) {
		want = right
	}
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby:     stubDeviceController{},
		Trust:      remoteTrustStub{peers: []trust.Peer{left, right}},
		Placements: newRemotePlacementStub(),
		Dialer: remoteDialerFunc(func(
			context.Context,
			device.NearbyDevice,
			trust.Peer,
		) (remoteprotocol.Caller, error) {
			t.Fatal("tie-break resolution should not open a remote connection")
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.resolveTrustedWorker(context.Background(), "auto")
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceID != want.DeviceID {
		t.Fatalf("worker = %s, want %s", got.DeviceID.Short(), want.DeviceID.Short())
	}
}

func TestRemoteJobServiceAutoSelectorExplainsNoActiveWorkers(t *testing.T) {
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby:     stubDeviceController{},
		Trust:      remoteTrustStub{},
		Placements: newRemotePlacementStub(),
		Dialer: remoteDialerFunc(func(
			context.Context,
			device.NearbyDevice,
			trust.Peer,
		) (remoteprotocol.Caller, error) {
			t.Fatal("empty automatic selection opened a remote connection")
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Submit(context.Background(), "auto", queuedJobForTest().Spec)
	if !errors.Is(err, ErrRemoteWorkerUnavailable) ||
		!strings.Contains(err.Error(), "computehop connect nearby") ||
		!strings.Contains(err.Error(), "computehop devices") {
		t.Fatalf("error = %v", err)
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
	for _, want := range []string{
		"Offline PC is not reachable",
		"Start ComputeHop on that worker",
		"same LAN",
		"computehop smoke",
		"computehop setup vps",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q; missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "%!") || strings.Contains(err.Error(), "Last error") {
		t.Fatalf("error leaked formatting details: %q", err)
	}
}

func TestRemoteJobServiceUnavailableWorkerPreservesDialCause(t *testing.T) {
	peer := activeWorkerPeer(t, 10, "Travel PC")
	cause := errors.New("remote connectivity path is unavailable")
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby:     stubDeviceController{},
		Trust:      remoteTrustStub{peers: []trust.Peer{peer}},
		Placements: newRemotePlacementStub(),
		Dialer: remoteDialerFunc(func(
			context.Context,
			device.NearbyDevice,
			trust.Peer,
		) (remoteprotocol.Caller, error) {
			t.Fatal("offline worker was dialed over LAN")
			return nil, nil
		}),
		Remote: pairedRemoteDialerFunc(func(context.Context, trust.Peer) (remoteprotocol.Caller, error) {
			return nil, cause
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Submit(context.Background(), "Travel PC", queuedJobForTest().Spec)
	if !errors.Is(err, ErrRemoteWorkerUnavailable) || !errors.Is(err, cause) {
		t.Fatalf("error = %v", err)
	}
	for _, want := range []string{
		"Travel PC is not reachable",
		"computehop smoke",
		"computehop setup vps",
		fmt.Sprintf("Last error: %v", cause),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q; missing %q", err, want)
		}
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
	contents := bytes.Repeat([]byte("package main\n"), 8_192)
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
	firstID := want.ID
	secondID, err := job.ParseID("019abcdf-0123-4567-89ab-000000000222")
	if err != nil {
		t.Fatal(err)
	}
	cached := false
	uploads := 0
	submissions := 0
	progress := newRemoteProgressStub()
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby: stubDeviceController{}, Trust: remoteTrustStub{peers: []trust.Peer{peer}},
		Placements: newRemotePlacementStub(), Progress: progress,
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
				case *computehopv1.RemoteRequest_GetWorkerStatus:
					return workerStatusResponse("linux", "amd64", 8, 16<<30, "echo"), nil
				case *computehopv1.RemoteRequest_CheckSnapshot:
					missing := []string(nil)
					if !cached {
						missing = []string{string(digest)}
					}
					return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_CheckSnapshot{
						CheckSnapshot: &computehopv1.CheckSnapshotResponse{
							MissingChunkDigests: missing,
							AcceptedChunkEncodings: []computehopv1.ChunkEncoding{
								computehopv1.ChunkEncoding_CHUNK_ENCODING_ZSTD,
								computehopv1.ChunkEncoding_CHUNK_ENCODING_IDENTITY,
							},
						},
					}}, nil
				case *computehopv1.RemoteRequest_PutChunk:
					put := request.GetPutChunk()
					encoding, mapErr := mapper.ChunkEncodingFromRemoteProto(put.GetEncoding())
					decoded, decodeErr := transfer.DecodeChunk(transfer.Chunk{
						Encoding: encoding, Data: put.GetData(), UncompressedSize: put.GetUncompressedSize(),
					})
					if put.GetEncoding() != computehopv1.ChunkEncoding_CHUNK_ENCODING_ZSTD ||
						mapErr != nil || decodeErr != nil || put.GetDigest() != string(digest) ||
						!bytes.Equal(decoded, contents) {
						t.Fatalf("put request = %#v", put)
					}
					uploads++
					cached = true
					return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_PutChunk{
						PutChunk: &computehopv1.PutChunkResponse{Digest: string(digest)},
					}}, nil
				case *computehopv1.RemoteRequest_SubmitJob:
					submitted := request.GetSubmitJob()
					submittedID, parseErr := job.ParseID(submitted.GetJobId())
					if submitted.GetSnapshot() == nil || submitted.GetWorkingSubdirectory() != "src" ||
						submitted.GetSpec().GetWorkingDirectory() != "" || parseErr != nil {
						t.Fatalf("submit request = %#v", submitted)
					}
					current := want
					current.ID = submittedID
					jobMessage, messageErr := mapper.JobToRemoteProto(current)
					if messageErr != nil {
						t.Fatal(messageErr)
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
	for index, id := range []job.ID{firstID, secondID} {
		got, err := service.SubmitWithID(context.Background(), "Build PC", id, spec)
		if err != nil {
			t.Fatalf("SubmitWithID(%d) error = %v", index, err)
		}
		if got.ID != id {
			t.Fatalf("SubmitWithID(%d) ID = %s, want %s", index, got.ID, id)
		}
	}
	if uploads != 1 || submissions != 2 {
		t.Fatalf("uploads = %d, submissions = %d", uploads, submissions)
	}
	if !progress.saw(job.ProgressSnapshot) || !progress.saw(job.ProgressUpload) ||
		progress.clears != 2 || len(progress.values) != 0 {
		t.Fatalf("progress history = %#v, clears = %d, values = %#v", progress.history, progress.clears, progress.values)
	}
}

func TestRemoteJobServiceSkipsSnapshotForEmptyWorkingDirectory(t *testing.T) {
	peer := activeWorkerPeer(t, 18, "Utility PC")
	want := queuedJobForTest()
	want.Spec.WorkingDirectory = ""
	message, err := mapper.JobToRemoteProto(want)
	if err != nil {
		t.Fatal(err)
	}
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
				if request.GetGetWorkerStatus() != nil {
					return workerStatusResponse("linux", "amd64", 8, 16<<30, "echo"), nil
				}
				submit := request.GetSubmitJob()
				if submit == nil {
					t.Fatalf("request = %#v", request)
				}
				if submit.GetSnapshot() != nil ||
					submit.GetWorkingSubdirectory() != "" ||
					submit.GetSpec().GetWorkingDirectory() != "" {
					t.Fatalf("submit request = %#v", submit)
				}
				submissions++
				return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_SubmitJob{
					SubmitJob: &computehopv1.SubmitJobResponse{Job: message},
				}}, nil
			}}, nil
		}),
		Snapshots: projectSnapshotterStub{err: errors.New("snapshot should not be built")},
		Content:   snapshotContentStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Submit(context.Background(), "Utility PC", want.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if submissions != 1 || got.ID != want.ID {
		t.Fatalf("submissions = %d; job = %#v", submissions, got)
	}
}

func TestRemoteJobServiceDownloadsOnlyMissingArtifactChunksAndRestores(t *testing.T) {
	peer := activeWorkerPeer(t, 23, "Render PC")
	contents := bytes.Repeat([]byte("rendered output\n"), 8_192)
	digest := snapshot.Sum(contents)
	wireChunk, err := transfer.EncodeChunk(contents, []transfer.ChunkEncoding{transfer.EncodingZstd})
	if err != nil {
		t.Fatal(err)
	}
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
	acknowledgments := 0
	progress := newRemoteProgressStub()
	destination := filepath.Join(t.TempDir(), "results")
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby: stubDeviceController{}, Trust: remoteTrustStub{peers: []trust.Peer{peer}},
		Placements: placements, Progress: progress,
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
					if request.GetGetArtifactChunk().GetDigest() != string(digest) ||
						len(request.GetGetArtifactChunk().GetAcceptedEncodings()) != 2 {
						t.Fatalf("chunk request = %#v", request.GetGetArtifactChunk())
					}
					return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_GetArtifactChunk{
						GetArtifactChunk: &computehopv1.GetArtifactChunkResponse{
							Digest: string(digest), Data: wireChunk.Data,
							Encoding:         computehopv1.ChunkEncoding_CHUNK_ENCODING_ZSTD,
							UncompressedSize: wireChunk.UncompressedSize,
						},
					}}, nil
				case *computehopv1.RemoteRequest_AcknowledgeJobArtifacts:
					acknowledgments++
					if request.GetAcknowledgeJobArtifacts().GetJobId() != string(want.ID) {
						t.Fatalf("acknowledgment request = %#v", request.GetAcknowledgeJobArtifacts())
					}
					return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_AcknowledgeJobArtifacts{
						AcknowledgeJobArtifacts: &computehopv1.AcknowledgeJobArtifactsResponse{JobId: string(want.ID)},
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
	if downloads != 1 || restores != 2 || acknowledgments != 2 {
		t.Fatalf("downloads = %d, restores = %d, acknowledgments = %d", downloads, restores, acknowledgments)
	}
	if !progress.saw(job.ProgressDownload) || !progress.saw(job.ProgressRestore) || progress.clears != 2 {
		t.Fatalf("progress history = %#v, clears = %d", progress.history, progress.clears)
	}
}

func TestRemoteJobServiceOverlaysLocalTransferProgress(t *testing.T) {
	peer := activeWorkerPeer(t, 24, "Render PC")
	want := queuedJobForTest()
	message, err := mapper.JobToRemoteProto(want)
	if err != nil {
		t.Fatal(err)
	}
	placements := newRemotePlacementStub()
	if err := placements.Create(context.Background(), placement.Placement{
		JobID: want.ID, WorkerID: peer.DeviceID, PlacedAt: want.CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	progress := newRemoteProgressStub()
	if err := progress.SetProgress(context.Background(), want.ID, job.Progress{
		Phase: job.ProgressDownload, CompletedBytes: 10, TotalBytes: 20,
		UpdatedAt: want.UpdatedAt.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	service, err := NewRemoteJobService(RemoteDependencies{
		Nearby: stubDeviceController{}, Trust: remoteTrustStub{peers: []trust.Peer{peer}},
		Placements: placements, Progress: progress,
		Dialer: remoteDialerFunc(func(context.Context, device.NearbyDevice, trust.Peer) (remoteprotocol.Caller, error) {
			return nil, errors.New("no LAN path")
		}),
		Remote: pairedRemoteDialerFunc(func(context.Context, trust.Peer) (remoteprotocol.Caller, error) {
			return &remoteCallerStub{call: func(
				context.Context,
				*computehopv1.RemoteRequest,
			) (*computehopv1.RemoteResponse, error) {
				return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_GetJob{
					GetJob: &computehopv1.GetJobResponse{Job: message},
				}}, nil
			}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(context.Background(), "", want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Progress == nil || got.Progress.Phase != job.ProgressDownload ||
		got.Progress.CompletedBytes != 10 || got.Progress.TotalBytes != 20 {
		t.Fatalf("progress = %#v", got.Progress)
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
func (stub remoteTrustStub) UpdateHints(_ context.Context, id device.ID, hints trust.PeerHints) (trust.Peer, error) {
	if err := hints.Validate(); err != nil {
		return trust.Peer{}, err
	}
	for _, peer := range stub.peers {
		if peer.DeviceID != id {
			continue
		}
		peer = peer.Clone()
		peer.Platform = hints.Platform
		peer.Architecture = hints.Architecture
		peer.LogicalCPUCount = hints.LogicalCPUCount
		peer.TotalMemoryBytes = hints.TotalMemoryBytes
		peer.ToolIDs = append([]string(nil), hints.ToolIDs...)
		observedAt := hints.ObservedAt.UTC()
		peer.HintsObservedAt = &observedAt
		return peer, nil
	}
	return trust.Peer{}, trust.ErrNotFound
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

type remoteProgressStub struct {
	values  map[job.ID]job.Progress
	history []job.Progress
	clears  int
}

func newRemoteProgressStub() *remoteProgressStub {
	return &remoteProgressStub{values: make(map[job.ID]job.Progress)}
}

func (stub *remoteProgressStub) SetProgress(_ context.Context, id job.ID, progress job.Progress) error {
	if !id.Valid() {
		return job.ErrInvalidID
	}
	if err := progress.Validate(); err != nil {
		return err
	}
	stub.values[id] = progress
	stub.history = append(stub.history, progress)
	return nil
}

func (stub *remoteProgressStub) GetProgress(_ context.Context, id job.ID) (*job.Progress, error) {
	progress, ok := stub.values[id]
	if !ok {
		return nil, nil
	}
	return &progress, nil
}

func (stub *remoteProgressStub) ClearProgress(_ context.Context, id job.ID) error {
	delete(stub.values, id)
	stub.clears++
	return nil
}

func (stub *remoteProgressStub) saw(phase job.ProgressPhase) bool {
	for _, progress := range stub.history {
		if progress.Phase == phase {
			return true
		}
	}
	return false
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

func peerWithResourceHints(peer trust.Peer, logicalCPUCount uint32, totalMemoryBytes uint64) trust.Peer {
	peer.LogicalCPUCount = logicalCPUCount
	peer.TotalMemoryBytes = totalMemoryBytes
	observedAt := peer.UpdatedAt.Add(time.Minute)
	peer.HintsObservedAt = &observedAt
	return peer
}

func workerStatusResponse(platform, architecture string, logicalCPUCount uint32, totalMemoryBytes uint64, toolIDs ...string) *computehopv1.RemoteResponse {
	return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_GetWorkerStatus{
		GetWorkerStatus: &computehopv1.GetWorkerStatusResponse{
			Platform: platform, Arch: architecture,
			LogicalCpuCount: logicalCPUCount, TotalMemoryBytes: totalMemoryBytes,
			ToolIds:            append([]string(nil), toolIDs...),
			SupportedExecutors: []computehopv1.Executor{computehopv1.Executor_EXECUTOR_NATIVE},
		},
	}}
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

func nearbyWorkerWithResources(
	t *testing.T,
	name string,
	port uint16,
	logicalCPUCount uint32,
	totalMemoryBytes uint64,
) device.NearbyDevice {
	t.Helper()
	nearby := nearbyWorker(t, name, port)
	nearby.Announcement.LogicalCPUCount = logicalCPUCount
	nearby.Announcement.TotalMemoryBytes = totalMemoryBytes
	return nearby
}
