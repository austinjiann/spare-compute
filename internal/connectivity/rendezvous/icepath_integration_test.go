package rendezvous_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"
	"time"

	connectivityv1 "github.com/austinjiann/spare-compute/gen/go/computehop/connectivity/v1"
	"github.com/austinjiann/spare-compute/internal/connectivity"
	"github.com/austinjiann/spare-compute/internal/connectivity/icepath"
	"github.com/austinjiann/spare-compute/internal/connectivity/rendezvous"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/trust"
)

func TestEncryptedRendezvousDescriptionsEstablishICEPath(t *testing.T) {
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
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
	secret := trust.ConnectivitySecret(bytes.Repeat([]byte{10}, trust.ConnectivitySecretBytes))
	access, err := connectivity.DeriveAccess(secret, now)
	if err != nil {
		t.Fatal(err)
	}

	orchestratorSession, orchestratorPresence := gatheredPresence(t, now, 1)
	workerSession, workerPresence := gatheredPresence(t, now, 1)
	orchestratorCiphertext, err := icepath.SealPresence(
		secret, access.RouteID, device.RoleOrchestrator, orchestratorPresence,
	)
	if err != nil {
		t.Fatal(err)
	}
	workerCiphertext, err := icepath.SealPresence(
		secret, access.RouteID, device.RoleWorker, workerPresence,
	)
	if err != nil {
		t.Fatal(err)

	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	first, err := client.PublishPresence(
		ctx, access, connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR,
		orchestratorPresence.Generation, orchestratorCiphertext,
	)
	if err != nil || first.GetPeer() != nil {
		t.Fatalf("first publish = %#v, %v", first, err)
	}
	workerResponse, err := client.PublishPresence(
		ctx, access, connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER,
		workerPresence.Generation, workerCiphertext,
	)
	if err != nil || workerResponse.GetPeer() == nil {
		t.Fatalf("worker publish = %#v, %v", workerResponse, err)
	}
	orchestratorResponse, err := client.PublishPresence(
		ctx, access, connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR,
		orchestratorPresence.Generation, orchestratorCiphertext,
	)
	if err != nil || orchestratorResponse.GetPeer() == nil {
		t.Fatalf("orchestrator refresh = %#v, %v", orchestratorResponse, err)
	}

	remoteOrchestrator, err := icepath.OpenPresence(
		secret, access.RouteID, device.RoleOrchestrator,
		workerResponse.GetPeer().GetGeneration(), workerResponse.GetPeer().GetEncryptedPayload(), now,
	)
	if err != nil {
		t.Fatal(err)
	}
	remoteWorker, err := icepath.OpenPresence(
		secret, access.RouteID, device.RoleWorker,
		orchestratorResponse.GetPeer().GetGeneration(), orchestratorResponse.GetPeer().GetEncryptedPayload(), now,
	)
	if err != nil {
		t.Fatal(err)
	}

	type connectionResult struct {
		connection *icepath.PacketConn
		err        error
	}
	orchestratorResult := make(chan connectionResult, 1)
	go func() {
		connection, err := orchestratorSession.Connect(
			ctx, device.RoleOrchestrator, remoteWorker.Description,
		)
		orchestratorResult <- connectionResult{connection: connection, err: err}
	}()
	workerConnection, err := workerSession.Connect(ctx, device.RoleWorker, remoteOrchestrator.Description)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerConnection.Close() })
	result := <-orchestratorResult
	if result.err != nil {
		t.Fatal(result.err)
	}
	t.Cleanup(func() { _ = result.connection.Close() })
	if _, err := result.connection.WriteTo([]byte("rendezvous to ICE"), workerConnection.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	read, _, err := workerConnection.ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:read]) != "rendezvous to ICE" {
		t.Fatalf("received %q", buffer[:read])
	}
}

func gatheredPresence(t *testing.T, now time.Time, generation uint64) (*icepath.Session, icepath.Presence) {
	t.Helper()
	session, err := icepath.NewSession(icepath.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	description, err := session.Gather(ctx)
	if err != nil {
		t.Fatal(err)
	}
	presence, err := icepath.NewPresence(description, generation, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return session, presence
}
