package icepath_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/connectivity/icepath"
	"github.com/austinjiann/spare-compute/internal/device"
)

func TestHostCandidatesCreatePacketConnection(t *testing.T) {
	orchestrator, err := icepath.NewSession(icepath.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = orchestrator.Close() })
	worker, err := icepath.NewSession(icepath.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Close() })

	gatherContext, stopGather := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopGather()
	orchestratorDescription, err := orchestrator.Gather(gatherContext)
	if err != nil {
		t.Fatal(err)
	}
	workerDescription, err := worker.Gather(gatherContext)
	if err != nil {
		t.Fatal(err)
	}

	type connectionResult struct {
		connection *icepath.PacketConn
		err        error
	}
	connectContext, stopConnect := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopConnect()
	orchestratorResult := make(chan connectionResult, 1)
	go func() {
		connection, err := orchestrator.Connect(connectContext, device.RoleOrchestrator, workerDescription)
		orchestratorResult <- connectionResult{connection: connection, err: err}
	}()
	workerConnection, err := worker.Connect(connectContext, device.RoleWorker, orchestratorDescription)
	if err != nil {
		t.Fatal(err)
	}
	orchestratorConnectionResult := <-orchestratorResult
	if orchestratorConnectionResult.err != nil {
		t.Fatal(orchestratorConnectionResult.err)
	}
	orchestratorConnection := orchestratorConnectionResult.connection
	t.Cleanup(func() { _ = orchestratorConnection.Close() })
	t.Cleanup(func() { _ = workerConnection.Close() })

	deadline := time.Now().Add(5 * time.Second)
	if err := orchestratorConnection.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := workerConnection.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	message := []byte("ComputeHop over ICE")
	if _, err := orchestratorConnection.WriteTo(message, workerConnection.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1500)
	read, source, err := workerConnection.ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:read]) != string(message) || source == nil {
		t.Fatalf("received %q from %v", buffer[:read], source)
	}
	path, err := orchestratorConnection.SelectedPath()
	if err != nil {
		t.Fatal(err)
	}
	if path.Kind != "host" || path.LocalAddr == "" || path.RemoteAddr == "" {
		t.Fatalf("selected path = %#v", path)
	}
}

func TestDescriptionValidationRejectsUnboundedOrNonUDPInput(t *testing.T) {
	valid := icepath.Description{
		Ufrag: "abcd", Password: "abcdefghijklmnopqrstuv",
		Candidates: []string{"1 1 udp 2130706431 192.0.2.1 5000 typ host"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	largeCandidates := repeatedCandidates(64)
	if err := (icepath.Description{
		Ufrag: valid.Ufrag, Password: valid.Password, Candidates: largeCandidates[:1],
	}).Validate(); err != nil {
		t.Fatalf("individual large candidate should be valid: %v", err)
	}
	tests := []icepath.Description{
		{},
		{Ufrag: valid.Ufrag, Password: valid.Password, Candidates: []string{"invalid"}},
		{Ufrag: valid.Ufrag, Password: valid.Password, Candidates: []string{
			"1 1 tcp 2130706431 192.0.2.1 5000 typ host tcptype passive",
		}},
		{Ufrag: valid.Ufrag, Password: valid.Password, Candidates: []string{
			valid.Candidates[0], valid.Candidates[0],
		}},
		{Ufrag: valid.Ufrag, Password: valid.Password, Candidates: largeCandidates},
	}
	for index, description := range tests {
		if err := description.Validate(); !errors.Is(err, icepath.ErrInvalidDescription) {
			t.Fatalf("case %d error = %v", index, err)
		}
	}
}

func repeatedCandidates(count int) []string {
	candidates := make([]string, 0, count)
	for index := range count {
		candidates = append(candidates, fmt.Sprintf(
			"%d 1 udp 2130706431 192.0.2.1 %d typ host test-extension %s%d",
			index+1, 5000+index, strings.Repeat("a", 320), index,
		))
	}
	return candidates
}

func TestRelayOnlyRequiresAuthenticatedTURNServer(t *testing.T) {
	if _, err := icepath.NewSession(icepath.Config{RelayOnly: true}); !errors.Is(err, icepath.ErrInvalidConfiguration) {
		t.Fatalf("missing TURN error = %v", err)
	}
	if _, err := icepath.NewSession(icepath.Config{Servers: []icepath.Server{
		{URI: "turn:127.0.0.1:3478?transport=udp"},
	}, RelayOnly: true}); !errors.Is(err, icepath.ErrInvalidConfiguration) {
		t.Fatalf("missing credentials error = %v", err)
	}
	if _, err := icepath.NewSession(icepath.Config{Servers: []icepath.Server{
		{URI: "stun:127.0.0.1:3478", Username: "unexpected"},
	}}); !errors.Is(err, icepath.ErrInvalidConfiguration) {
		t.Fatalf("STUN credentials error = %v", err)
	}
}

func TestPacketConnSatisfiesNetworkContract(_ *testing.T) {
	var _ net.PacketConn = (*icepath.PacketConn)(nil)
}
