package quic

import (
	"context"
	"testing"
	"time"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/connectivity/icepath"
	"github.com/austinjiann/spare-compute/internal/device"
	remoteprotocol "github.com/austinjiann/spare-compute/internal/protocol/remote"
)

func TestRemoteControlRunsOverICEWithExistingIdentityPins(t *testing.T) {
	worker := localDeviceForTransportTest(t, 31, "Remote Worker", device.RoleWorker)
	orchestrator := localDeviceForTransportTest(t, 32, "Remote Mac", device.RoleOrchestrator)
	workerPeer := trustedTransportPeer(t, worker, 33)
	orchestratorPeer := trustedTransportPeer(t, orchestrator, 34)
	workerTrust := newTransportTrustRepository(orchestratorPeer)
	workerEndpoint, err := Listen("127.0.0.1:0", worker, workerTrust)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerEndpoint.Close() })
	orchestratorEndpoint, err := Listen(
		"127.0.0.1:0", orchestrator, newTransportTrustRepository(workerPeer),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = orchestratorEndpoint.Close() })

	orchestratorConnection, workerConnection := selectedICEConnections(t)
	orchestratorPath, err := orchestratorEndpoint.NewRemotePath(
		orchestratorConnection, orchestratorConnection.RemoteAddr(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = orchestratorPath.Close() })
	workerPath, err := workerEndpoint.NewRemotePath(workerConnection, workerConnection.RemoteAddr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerPath.Close() })

	handler := remoteprotocol.HandlerFunc(func(
		_ context.Context,
		request *computehopv1.RemoteRequest,
	) *computehopv1.RemoteResponse {
		return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_GetJob{
			GetJob: &computehopv1.GetJobResponse{Job: controlTestJob(request.GetGetJob().GetJobId())},
		}}
	})
	serveContext, stopServing := context.WithCancel(context.Background())
	serveResult := make(chan error, 1)
	go func() { serveResult <- workerPath.Serve(serveContext, handler) }()
	t.Cleanup(func() {
		stopServing()
		select {
		case err := <-serveResult:
			if err != nil {
				t.Errorf("Serve() error = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve() did not stop")
		}
	})

	dialContext, stopDial := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopDial()
	caller, err := orchestratorPath.Dial(dialContext, workerPeer)
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	response, err := caller.Call(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_GetJob{
			GetJob: &computehopv1.GetJobRequest{JobId: "ice-job"},
		},
	})
	if err != nil || response.GetGetJob().GetJob().GetId() != "ice-job" {
		t.Fatalf("Call() = %#v, %v", response, err)
	}

	if _, err := workerTrust.Revoke(context.Background(), orchestratorPeer.DeviceID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := caller.Call(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_GetJob{
			GetJob: &computehopv1.GetJobRequest{JobId: "revoked"},
		},
	}); err == nil {
		t.Fatal("revoked orchestrator reused an ICE control path")
	}
}

func selectedICEConnections(t *testing.T) (*icepath.PacketConn, *icepath.PacketConn) {
	t.Helper()
	orchestratorSession, err := icepath.NewSession(icepath.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = orchestratorSession.Close() })
	workerSession, err := icepath.NewSession(icepath.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerSession.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	orchestratorDescription, err := orchestratorSession.Gather(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workerDescription, err := workerSession.Gather(ctx)
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		connection *icepath.PacketConn
		err        error
	}
	orchestratorResult := make(chan result, 1)
	go func() {
		connection, err := orchestratorSession.Connect(
			ctx, device.RoleOrchestrator, workerDescription,
		)
		orchestratorResult <- result{connection: connection, err: err}
	}()
	workerConnection, err := workerSession.Connect(ctx, device.RoleWorker, orchestratorDescription)
	if err != nil {
		t.Fatal(err)
	}
	resultValue := <-orchestratorResult
	if resultValue.err != nil {
		t.Fatal(resultValue.err)
	}
	return resultValue.connection, workerConnection
}
