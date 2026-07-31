package remoteconn_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/connectivity/remoteconn"
	"github.com/austinjiann/spare-compute/internal/connectivity/rendezvous"
	"github.com/austinjiann/spare-compute/internal/device"
	quictransport "github.com/austinjiann/spare-compute/internal/infra/transport/quic"
	remoteprotocol "github.com/austinjiann/spare-compute/internal/protocol/remote"
	"github.com/austinjiann/spare-compute/internal/session"
	"github.com/austinjiann/spare-compute/internal/trust"
)

func TestManagersExchangePresenceAndRunPinnedRemoteControl(t *testing.T) {
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	service, err := rendezvous.New(rendezvous.Config{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service)
	t.Cleanup(server.Close)
	client, err := rendezvous.NewClient(rendezvous.ClientConfig{
		BaseURL: server.URL, HTTPClient: server.Client(),
		Now: func() time.Time { return now }, AllowLoopbackHTTP: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	orchestrator := testLocalDevice(t, 1, "Orchestrator", device.RoleOrchestrator)
	worker := testLocalDevice(t, 2, "Worker", device.RoleWorker)
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{3}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	secret := trust.ConnectivitySecret(bytes.Repeat([]byte{4}, trust.ConnectivitySecretBytes))
	workerPeer := testTrustedPeer(worker, pairID, secret, now)
	orchestratorPeer := testTrustedPeer(orchestrator, pairID, secret, now)
	orchestratorTrust := newTestTrustRepository(workerPeer)
	workerTrust := newTestTrustRepository(orchestratorPeer)
	orchestratorEndpoint, err := quictransport.Listen("127.0.0.1:0", orchestrator, orchestratorTrust)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = orchestratorEndpoint.Close() })
	workerEndpoint, err := quictransport.Listen("127.0.0.1:0", worker, workerTrust)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerEndpoint.Close() })

	orchestratorManager, err := remoteconn.NewManager(testManagerConfig(
		device.RoleOrchestrator,
		orchestratorTrust,
		client,
		func(connection net.PacketConn, remote net.Addr) (remoteconn.Path, error) {
			return orchestratorEndpoint.NewRemotePath(connection, remote)
		},
		func() time.Time { return now },
	))
	if err != nil {
		t.Fatal(err)
	}
	workerManager, err := remoteconn.NewManager(testManagerConfig(
		device.RoleWorker,
		workerTrust,
		client,
		func(connection net.PacketConn, remote net.Addr) (remoteconn.Path, error) {
			return workerEndpoint.NewRemotePath(connection, remote)
		},
		func() time.Time { return now },
	))
	if err != nil {
		t.Fatal(err)
	}
	handler := remoteprotocol.HandlerFunc(func(
		_ context.Context,
		request *computehopv1.RemoteRequest,
	) *computehopv1.RemoteResponse {
		return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_GetJob{
			GetJob: &computehopv1.GetJobResponse{Job: &computehopv1.Job{
				Id: request.GetGetJob().GetJobId(),
				Spec: &computehopv1.JobSpec{
					Executable: "echo", Executor: computehopv1.Executor_EXECUTOR_NATIVE,
				},
				State:             computehopv1.JobState_JOB_STATE_SUCCEEDED,
				CreatedAtUnixNano: 1, UpdatedAtUnixNano: 2,
			}},
		}}
	})

	runContext, stopManagers := context.WithCancel(context.Background())
	runResults := make(chan error, 2)
	go func() { runResults <- workerManager.Run(runContext, handler) }()
	go func() { runResults <- orchestratorManager.Run(runContext, nil) }()
	t.Cleanup(func() {
		stopManagers()
		for range 2 {
			select {
			case err := <-runResults:
				if err != nil {
					t.Errorf("Run() error = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("manager did not stop")
			}
		}
	})

	dialContext, stopDial := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopDial()
	caller, err := orchestratorManager.DialRemotePeer(dialContext, workerPeer)
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	response, err := caller.Call(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_GetJob{
			GetJob: &computehopv1.GetJobRequest{JobId: "managed-remote-job"},
		},
	})
	if err != nil || response.GetGetJob().GetJob().GetId() != "managed-remote-job" {
		t.Fatalf("Call() = %#v, %v", response, err)
	}
	states := orchestratorManager.States()
	if len(states) != 1 || states[0].Status != remoteconn.StatusConnected || states[0].PathKind != "host" {
		t.Fatalf("states = %#v", states)
	}
}

func testManagerConfig(
	role device.Role,
	repository trust.Repository,
	client *rendezvous.Client,
	newPath remoteconn.PathFactory,
	now func() time.Time,
) remoteconn.Config {
	return remoteconn.Config{
		LocalRole: role, Trust: repository, Client: client, NewPath: newPath, Now: now,
		TrustRefreshInterval: 100 * time.Millisecond,
		PublishInterval:      100 * time.Millisecond,
		RetryDelay:           100 * time.Millisecond,
		GatherTimeout:        5 * time.Second,
		ExchangeTimeout:      5 * time.Second,
		ConnectTimeout:       5 * time.Second,
	}
}

func testLocalDevice(t *testing.T, seed byte, name string, role device.Role) session.LocalDevice {
	t.Helper()
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{seed}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	presence, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{seed + 10}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	return session.LocalDevice{Identity: identity, Name: name, Role: role, PresenceID: presence}
}

func testTrustedPeer(
	local session.LocalDevice,
	pairID trust.PairID,
	secret trust.ConnectivitySecret,
	now time.Time,
) trust.Peer {
	return trust.Peer{
		PairID: pairID, DeviceID: local.Identity.ID(), PublicKey: local.Identity.PublicKey(),
		ConnectivitySecret: append(trust.ConnectivitySecret(nil), secret...),
		Name:               local.Name, Role: local.Role, State: trust.StateActive,
		PairedAt: now, UpdatedAt: now,
	}
}

type testTrustRepository struct {
	mu    sync.Mutex
	peers map[device.ID]trust.Peer
}

func newTestTrustRepository(peers ...trust.Peer) *testTrustRepository {
	repository := &testTrustRepository{peers: make(map[device.ID]trust.Peer)}
	for _, peer := range peers {
		repository.peers[peer.DeviceID] = peer.Clone()
	}
	return repository
}

func (repository *testTrustRepository) Activate(_ context.Context, peer trust.Peer) error {
	if err := peer.Validate(); err != nil {
		return err
	}
	repository.mu.Lock()
	repository.peers[peer.DeviceID] = peer.Clone()
	repository.mu.Unlock()
	return nil
}

func (repository *testTrustRepository) Get(_ context.Context, id device.ID) (trust.Peer, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	peer, exists := repository.peers[id]
	if !exists {
		return trust.Peer{}, trust.ErrNotFound
	}
	return peer.Clone(), nil
}

func (repository *testTrustRepository) List(context.Context) ([]trust.Peer, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	peers := make([]trust.Peer, 0, len(repository.peers))
	for _, peer := range repository.peers {
		peers = append(peers, peer.Clone())
	}
	return peers, nil
}

func (repository *testTrustRepository) Revoke(
	_ context.Context,
	id device.ID,
	at time.Time,
) (trust.Peer, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	peer, exists := repository.peers[id]
	if !exists {
		return trust.Peer{}, trust.ErrNotFound
	}
	if at.IsZero() {
		return trust.Peer{}, errors.New("revocation time is required")
	}
	peer.State = trust.StateRevoked
	peer.UpdatedAt = at.UTC()
	peer.RevokedAt = &peer.UpdatedAt
	peer.ConnectivitySecret = nil
	repository.peers[id] = peer
	return peer.Clone(), nil
}

func (repository *testTrustRepository) UpdateHints(
	_ context.Context,
	id device.ID,
	hints trust.PeerHints,
) (trust.Peer, error) {
	if err := hints.Validate(); err != nil {
		return trust.Peer{}, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	peer, exists := repository.peers[id]
	if !exists {
		return trust.Peer{}, trust.ErrNotFound
	}
	peer.Platform = hints.Platform
	peer.Architecture = hints.Architecture
	peer.LogicalCPUCount = hints.LogicalCPUCount
	peer.TotalMemoryBytes = hints.TotalMemoryBytes
	observedAt := hints.ObservedAt.UTC()
	peer.HintsObservedAt = &observedAt
	repository.peers[id] = peer
	return peer.Clone(), nil
}
