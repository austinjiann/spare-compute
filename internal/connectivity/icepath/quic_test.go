package icepath_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"testing"
	"time"

	quicgo "github.com/quic-go/quic-go"

	"github.com/austinjiann/spare-compute/internal/connectivity/icepath"
	"github.com/austinjiann/spare-compute/internal/device"
)

func TestQUICRunsOverSelectedICEPacketConnection(t *testing.T) {
	orchestratorConnection, workerConnection := connectHostPair(t)
	certificate := transportCertificate(t)
	serverTLS := &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate},
		NextProtos: []string{"computehop-ice-test/1"},
	}
	listener, err := quicgo.Listen(workerConnection, serverTLS, &quicgo.Config{MaxIdleTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	message := []byte("authenticated QUIC can use the ICE packet path")
	serverResult := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		connection, err := listener.Accept(ctx)
		if err != nil {
			serverResult <- err
			return
		}
		stream, err := connection.AcceptStream(ctx)
		if err != nil {
			serverResult <- err
			return
		}
		contents := make([]byte, len(message))
		_, err = io.ReadFull(stream, contents)
		if err == nil && string(contents) != string(message) {
			err = io.ErrUnexpectedEOF
		}
		serverResult <- err
	}()

	clientTLS := &tls.Config{
		MinVersion: tls.VersionTLS13, NextProtos: []string{"computehop-ice-test/1"},
		InsecureSkipVerify: true, // This transport-only test does not exercise ComputeHop's pinned certificate verifier.
	}
	client, err := quicgo.Dial(
		ctx, orchestratorConnection, orchestratorConnection.RemoteAddr(), clientTLS,
		&quicgo.Config{MaxIdleTimeout: 5 * time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.CloseWithError(0, "test complete") })
	stream, err := client.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write(message); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

func connectHostPair(t *testing.T) (*icepath.PacketConn, *icepath.PacketConn) {
	t.Helper()
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	orchestratorDescription, err := orchestrator.Gather(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workerDescription, err := worker.Gather(ctx)
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		connection *icepath.PacketConn
		err        error
	}
	orchestratorResult := make(chan result, 1)
	go func() {
		connection, err := orchestrator.Connect(ctx, device.RoleOrchestrator, workerDescription)
		orchestratorResult <- result{connection: connection, err: err}
	}()
	workerConnection, err := worker.Connect(ctx, device.RoleWorker, orchestratorDescription)
	if err != nil {
		t.Fatal(err)
	}
	resultValue := <-orchestratorResult
	if resultValue.err != nil {
		t.Fatal(resultValue.err)
	}
	t.Cleanup(func() { _ = resultValue.connection.Close() })
	t.Cleanup(func() { _ = workerConnection.Close() })
	return resultValue.connection, workerConnection
}

func transportCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ComputeHop ICE test"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	encoded, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{encoded}, PrivateKey: privateKey}
}
