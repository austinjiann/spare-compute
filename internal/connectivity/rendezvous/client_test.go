package rendezvous_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	connectivityv1 "github.com/austinjiann/spare-compute/gen/go/computehop/connectivity/v1"
	"github.com/austinjiann/spare-compute/internal/connectivity"
	"github.com/austinjiann/spare-compute/internal/connectivity/rendezvous"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/trust"
)

func TestClientEncryptedPresenceAndSignals(t *testing.T) {
	now := time.Date(2026, time.July, 22, 6, 0, 0, 0, time.UTC)
	service, err := rendezvous.New(rendezvous.Config{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service)
	defer server.Close()
	client, err := rendezvous.NewClient(rendezvous.ClientConfig{
		BaseURL: server.URL, HTTPClient: server.Client(), Now: func() time.Time { return now }, AllowLoopbackHTTP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	secret := trust.ConnectivitySecret(bytes.Repeat([]byte{6}, trust.ConnectivitySecretBytes))
	access, err := connectivity.DeriveAccess(secret, now)
	if err != nil {
		t.Fatal(err)
	}

	orchestratorPresence, err := connectivity.SealPresence(
		secret, access.RouteID, device.RoleOrchestrator, 1, []byte("orchestrator candidates"),
	)
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.PublishPresence(
		context.Background(), access,
		connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR, 1, orchestratorPresence,
	)
	if err != nil || first.GetPeer() != nil {
		t.Fatalf("first presence = %#v, %v", first, err)
	}

	workerPresence, err := connectivity.SealPresence(
		secret, access.RouteID, device.RoleWorker, 7, []byte("worker candidates"),
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.PublishPresence(
		context.Background(), access, connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER, 7, workerPresence,
	)
	if err != nil || second.GetPeer() == nil {
		t.Fatalf("second presence = %#v, %v", second, err)
	}
	openedPresence, err := connectivity.OpenPresence(
		secret, access.RouteID, device.RoleOrchestrator,
		second.GetPeer().GetGeneration(), second.GetPeer().GetEncryptedPayload(),
	)
	if err != nil || string(openedPresence) != "orchestrator candidates" {
		t.Fatalf("opened presence = %q, %v", openedPresence, err)
	}

	encryptedSignal, err := connectivity.SealSignal(
		secret, access.RouteID, device.RoleOrchestrator, []byte("ICE offer"),
	)
	if err != nil {
		t.Fatal(err)
	}
	sent, err := client.SendSignal(
		context.Background(), access,
		connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR,
		connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER,
		encryptedSignal,
	)
	if err != nil || sent.GetSequence() == 0 {
		t.Fatalf("send = %#v, %v", sent, err)
	}
	polled, err := client.PollSignals(
		context.Background(), access, connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER, 0, 32,
	)
	if err != nil || len(polled.GetSignals()) != 1 {
		t.Fatalf("poll = %#v, %v", polled, err)
	}
	openedSignal, err := connectivity.OpenSignal(
		secret, access.RouteID, device.RoleOrchestrator, polled.GetSignals()[0].GetEncryptedPayload(),
	)
	if err != nil || string(openedSignal) != "ICE offer" {
		t.Fatalf("opened signal = %q, %v", openedSignal, err)
	}
}

func TestClientMapsServiceErrorsAndRejectsExpiredAccess(t *testing.T) {
	now := time.Date(2026, time.July, 22, 6, 0, 0, 0, time.UTC)
	service, err := rendezvous.New(rendezvous.Config{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service)
	defer server.Close()
	client, err := rendezvous.NewClient(rendezvous.ClientConfig{
		BaseURL: server.URL, HTTPClient: server.Client(), Now: func() time.Time { return now }, AllowLoopbackHTTP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	secret := trust.ConnectivitySecret(bytes.Repeat([]byte{8}, trust.ConnectivitySecretBytes))
	access, err := connectivity.DeriveAccess(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := connectivity.SealPresence(secret, access.RouteID, device.RoleWorker, 1, []byte("presence"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.PublishPresence(
		context.Background(), access, connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER, 1, payload,
	); err != nil {
		t.Fatal(err)
	}
	wrongAccess := access
	wrongAccess.Token = access.RouteID
	_, err = client.PublishPresence(
		context.Background(), wrongAccess, connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER, 2, payload,
	)
	var remoteError *rendezvous.RemoteError
	if !errors.As(err, &remoteError) || remoteError.StatusCode != http.StatusUnauthorized ||
		remoteError.Code != connectivityv1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_UNAUTHENTICATED {
		t.Fatalf("remote error = %#v, %v", remoteError, err)
	}

	expiredClient, err := rendezvous.NewClient(rendezvous.ClientConfig{
		BaseURL: server.URL, HTTPClient: server.Client(),
		Now: func() time.Time { return access.ExpiresAt }, AllowLoopbackHTTP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expiredClient.PublishPresence(
		context.Background(), access, connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER, 2, payload,
	); !errors.Is(err, rendezvous.ErrExpiredAccess) {
		t.Fatalf("expired error = %v", err)
	}
}

func TestClientRequiresHTTPSAndRefusesRedirects(t *testing.T) {
	if _, err := rendezvous.NewClient(rendezvous.ClientConfig{BaseURL: "http://example.com"}); !errors.Is(err, rendezvous.ErrInvalidClientConfiguration) {
		t.Fatalf("HTTP configuration error = %v", err)
	}

	now := time.Date(2026, time.July, 22, 6, 0, 0, 0, time.UTC)
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected = true
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client, err := rendezvous.NewClient(rendezvous.ClientConfig{
		BaseURL: redirect.URL, HTTPClient: redirect.Client(),
		Now: func() time.Time { return now }, AllowLoopbackHTTP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	secret := trust.ConnectivitySecret(bytes.Repeat([]byte{2}, trust.ConnectivitySecretBytes))
	access, err := connectivity.DeriveAccess(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.PublishPresence(
		context.Background(), access, connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER, 1, []byte("opaque"),
	)
	if !errors.Is(err, rendezvous.ErrInvalidServiceResponse) || redirected {
		t.Fatalf("redirect error = %v, redirected = %v", err, redirected)
	}
}

func TestClientNetworkErrorDoesNotExposeAnonymousRoute(t *testing.T) {
	now := time.Date(2026, time.July, 22, 6, 0, 0, 0, time.UTC)
	client, err := rendezvous.NewClient(rendezvous.ClientConfig{
		BaseURL: "https://connect.example.com",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network offline")
		})},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	secret := trust.ConnectivitySecret(bytes.Repeat([]byte{9}, trust.ConnectivitySecretBytes))
	access, err := connectivity.DeriveAccess(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.PublishPresence(
		context.Background(), access,
		connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER, 1, []byte("opaque"),
	)
	if err == nil || !strings.Contains(err.Error(), "network offline") ||
		strings.Contains(err.Error(), access.RouteID) {
		t.Fatalf("network error = %q", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
