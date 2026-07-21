package rendezvous_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	connectivityv1 "github.com/austinjiann/spare-compute/gen/go/computehop/connectivity/v1"
	"github.com/austinjiann/spare-compute/internal/connectivity"
	"github.com/austinjiann/spare-compute/internal/connectivity/rendezvous"
	"github.com/austinjiann/spare-compute/internal/trust"
	"google.golang.org/protobuf/proto"
)

func TestServicePublishesPresenceAndRelaysOpaqueSignals(t *testing.T) {
	now := time.Date(2026, time.July, 21, 2, 0, 0, 0, time.UTC)
	service := newService(t, rendezvous.Config{Now: func() time.Time { return now }})
	access := accessForTest(t, now, 1)

	orchestratorPresence := &connectivityv1.PublishPresenceRequest{
		ProtocolVersion: rendezvous.ProtocolVersion,
		Role:            connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR,
		Generation:      1, EncryptedPayload: []byte("encrypted-orchestrator-candidates"),
	}
	response := &connectivityv1.PublishPresenceResponse{}
	requestProto(t, service, access, "presence", orchestratorPresence, http.StatusOK, response)
	if response.GetPeer() != nil || response.GetExpiresAtUnixNano() <= now.UnixNano() {
		t.Fatalf("first presence response = %#v", response)
	}

	workerPresence := &connectivityv1.PublishPresenceRequest{
		ProtocolVersion: rendezvous.ProtocolVersion,
		Role:            connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER,
		Generation:      1, EncryptedPayload: []byte("encrypted-worker-candidates"),
	}
	response.Reset()
	requestProto(t, service, access, "presence", workerPresence, http.StatusOK, response)
	if response.GetPeer().GetRole() != connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR ||
		!bytes.Equal(response.GetPeer().GetEncryptedPayload(), orchestratorPresence.GetEncryptedPayload()) {
		t.Fatalf("worker peer presence = %#v", response.GetPeer())
	}

	sent := &connectivityv1.SendSignalResponse{}
	requestProto(t, service, access, "signals", &connectivityv1.SendSignalRequest{
		ProtocolVersion:  rendezvous.ProtocolVersion,
		Sender:           connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR,
		Recipient:        connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER,
		EncryptedPayload: []byte("encrypted-ice-check"),
	}, http.StatusOK, sent)
	if sent.GetSequence() != 1 || sent.GetExpiresAtUnixNano() <= now.UnixNano() {
		t.Fatalf("signal response = %#v", sent)
	}

	polled := &connectivityv1.PollSignalsResponse{}
	requestProto(t, service, access, "poll", &connectivityv1.PollSignalsRequest{
		ProtocolVersion: rendezvous.ProtocolVersion,
		Recipient:       connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER,
	}, http.StatusOK, polled)
	if len(polled.GetSignals()) != 1 || polled.GetNextSequence() != 1 ||
		polled.GetSignals()[0].GetSender() != connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR ||
		!bytes.Equal(polled.GetSignals()[0].GetEncryptedPayload(), []byte("encrypted-ice-check")) {
		t.Fatalf("poll response = %#v", polled)
	}

	polled.Reset()
	requestProto(t, service, access, "poll", &connectivityv1.PollSignalsRequest{
		ProtocolVersion: rendezvous.ProtocolVersion,
		Recipient:       connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER,
		AfterSequence:   1,
	}, http.StatusOK, polled)
	if len(polled.GetSignals()) != 0 || polled.GetNextSequence() != 1 {
		t.Fatalf("acknowledged poll response = %#v", polled)
	}
}

func TestServiceRejectsWrongCredentialStalePresenceAndUnsupportedVersion(t *testing.T) {
	now := time.Date(2026, time.July, 21, 2, 0, 0, 0, time.UTC)
	service := newService(t, rendezvous.Config{Now: func() time.Time { return now }})
	access := accessForTest(t, now, 2)
	request := &connectivityv1.PublishPresenceRequest{
		ProtocolVersion: rendezvous.ProtocolVersion,
		Role:            connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR,
		Generation:      2, EncryptedPayload: []byte("ciphertext"),
	}
	requestProto(t, service, access, "presence", request, http.StatusOK, &connectivityv1.PublishPresenceResponse{})

	wrong := accessForTest(t, now, 3)
	wrong.RouteID = access.RouteID
	requestProto(t, service, wrong, "presence", request, http.StatusUnauthorized, &connectivityv1.ErrorResponse{})

	request.Generation = 1
	requestProto(t, service, access, "presence", request, http.StatusConflict, &connectivityv1.ErrorResponse{})

	request.Generation = 3
	request.ProtocolVersion = rendezvous.ProtocolVersion + 1
	errorResponse := &connectivityv1.ErrorResponse{}
	requestProto(t, service, access, "presence", request, http.StatusUpgradeRequired, errorResponse)
	if errorResponse.GetCode() != connectivityv1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_UNSUPPORTED_VERSION {
		t.Fatalf("version error = %#v", errorResponse)
	}
}

func TestServiceExpiresPresenceSignalsAndRoutes(t *testing.T) {
	now := time.Date(2026, time.July, 21, 2, 0, 0, 0, time.UTC)
	service := newService(t, rendezvous.Config{
		Now: func() time.Time { return now }, PresenceLifetime: 10 * time.Second,
		SignalLifetime: 10 * time.Second, RouteLifetime: time.Minute,
	})
	access := accessForTest(t, now, 4)
	publishBoth(t, service, access)
	requestProto(t, service, access, "signals", validSignalRequest(), http.StatusOK, &connectivityv1.SendSignalResponse{})

	now = now.Add(11 * time.Second)
	requestProto(t, service, access, "poll", &connectivityv1.PollSignalsRequest{
		ProtocolVersion: rendezvous.ProtocolVersion,
		Recipient:       connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER,
	}, http.StatusConflict, &connectivityv1.ErrorResponse{})

	now = now.Add(time.Minute)
	requestProto(t, service, access, "poll", &connectivityv1.PollSignalsRequest{
		ProtocolVersion: rendezvous.ProtocolVersion,
		Recipient:       connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER,
	}, http.StatusNotFound, &connectivityv1.ErrorResponse{})
}

func TestServiceBoundsQueuesAndAllowsAcknowledgedCapacityToBeReused(t *testing.T) {
	now := time.Date(2026, time.July, 21, 2, 0, 0, 0, time.UTC)
	service := newService(t, rendezvous.Config{
		Now: func() time.Time { return now }, MaxSignalsPerRoute: 1,
	})
	access := accessForTest(t, now, 5)
	publishBoth(t, service, access)
	requestProto(t, service, access, "signals", validSignalRequest(), http.StatusOK, &connectivityv1.SendSignalResponse{})
	requestProto(t, service, access, "signals", validSignalRequest(), http.StatusTooManyRequests, &connectivityv1.ErrorResponse{})
	requestProto(t, service, access, "poll", &connectivityv1.PollSignalsRequest{
		ProtocolVersion: rendezvous.ProtocolVersion,
		Recipient:       connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER,
		AfterSequence:   1,
	}, http.StatusOK, &connectivityv1.PollSignalsResponse{})
	requestProto(t, service, access, "signals", validSignalRequest(), http.StatusOK, &connectivityv1.SendSignalResponse{})
}

func TestServiceBoundsRoutesPayloadsAndPerRouteRequests(t *testing.T) {
	now := time.Date(2026, time.July, 21, 2, 0, 0, 0, time.UTC)
	service := newService(t, rendezvous.Config{
		Now: func() time.Time { return now }, MaxRoutes: 1,
		MaxPayloadBytes: 4, MaxRequestsPerRoute: 2,
	})
	first := accessForTest(t, now, 7)
	presence := &connectivityv1.PublishPresenceRequest{
		ProtocolVersion: rendezvous.ProtocolVersion,
		Role:            connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR,
		Generation:      1, EncryptedPayload: []byte("four"),
	}
	requestProto(t, service, first, "presence", presence, http.StatusOK, &connectivityv1.PublishPresenceResponse{})

	tooLarge := proto.Clone(presence).(*connectivityv1.PublishPresenceRequest)
	tooLarge.Generation = 2
	tooLarge.EncryptedPayload = []byte("five!")
	requestProto(t, service, first, "presence", tooLarge, http.StatusBadRequest, &connectivityv1.ErrorResponse{})

	requestProto(t, service, first, "presence", presence, http.StatusOK, &connectivityv1.PublishPresenceResponse{})
	requestProto(t, service, first, "presence", presence, http.StatusTooManyRequests, &connectivityv1.ErrorResponse{})

	second := accessForTest(t, now, 8)
	requestProto(t, service, second, "presence", presence, http.StatusTooManyRequests, &connectivityv1.ErrorResponse{})
}

func TestServiceHandlesConcurrentIdempotentPresenceRefreshes(t *testing.T) {
	now := time.Date(2026, time.July, 21, 2, 0, 0, 0, time.UTC)
	service := newService(t, rendezvous.Config{Now: func() time.Time { return now }})
	server := httptest.NewServer(service)
	defer server.Close()
	access := accessForTest(t, now, 6)
	message := &connectivityv1.PublishPresenceRequest{
		ProtocolVersion: rendezvous.ProtocolVersion,
		Role:            connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR,
		Generation:      1, EncryptedPayload: []byte("same-ciphertext"),
	}
	contents, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}

	const attempts = 24
	errorsByAttempt := make(chan error, attempts)
	var wait sync.WaitGroup
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			request, err := http.NewRequestWithContext(
				context.Background(), http.MethodPost,
				server.URL+"/v1/rendezvous/"+access.RouteID+"/presence",
				bytes.NewReader(contents),
			)
			if err != nil {
				errorsByAttempt <- err
				return
			}
			request.Header.Set("Authorization", "Bearer "+access.Token)
			request.Header.Set("Content-Type", rendezvous.ProtobufMediaType)
			response, err := http.DefaultClient.Do(request)
			if err == nil {
				defer response.Body.Close()
				if response.StatusCode != http.StatusOK {
					err = fmt.Errorf("status %d", response.StatusCode)
				}
			}
			errorsByAttempt <- err
		}()
	}
	wait.Wait()
	close(errorsByAttempt)
	for err := range errorsByAttempt {
		if err != nil {
			t.Fatalf("concurrent refresh error = %v", err)
		}
	}
}

func TestServiceHealthDoesNotRequirePairCredentials(t *testing.T) {
	service := newService(t, rendezvous.Config{})
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	service.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "ok\n" ||
		response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("health response = %#v %q", response.Result(), response.Body.String())
	}
}

func newService(t *testing.T, config rendezvous.Config) *rendezvous.Service {
	t.Helper()
	service, err := rendezvous.New(config)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func accessForTest(t *testing.T, at time.Time, seed byte) connectivity.Access {
	t.Helper()
	access, err := connectivity.DeriveAccess(
		trust.ConnectivitySecret(bytes.Repeat([]byte{seed}, trust.ConnectivitySecretBytes)), at,
	)
	if err != nil {
		t.Fatal(err)
	}
	return access
}

func publishBoth(t *testing.T, service *rendezvous.Service, access connectivity.Access) {
	t.Helper()
	for _, role := range []connectivityv1.EndpointRole{
		connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR,
		connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER,
	} {
		requestProto(t, service, access, "presence", &connectivityv1.PublishPresenceRequest{
			ProtocolVersion: rendezvous.ProtocolVersion, Role: role,
			Generation: 1, EncryptedPayload: []byte("ciphertext"),
		}, http.StatusOK, &connectivityv1.PublishPresenceResponse{})
	}
}

func validSignalRequest() *connectivityv1.SendSignalRequest {
	return &connectivityv1.SendSignalRequest{
		ProtocolVersion:  rendezvous.ProtocolVersion,
		Sender:           connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR,
		Recipient:        connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER,
		EncryptedPayload: []byte("signal-ciphertext"),
	}
}

func requestProto(
	t *testing.T,
	service *rendezvous.Service,
	access connectivity.Access,
	operation string,
	requestMessage proto.Message,
	wantStatus int,
	responseMessage proto.Message,
) {
	t.Helper()
	contents, err := proto.Marshal(requestMessage)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/rendezvous/"+access.RouteID+"/"+operation,
		bytes.NewReader(contents),
	)
	request.Header.Set("Authorization", "Bearer "+access.Token)
	request.Header.Set("Content-Type", rendezvous.ProtobufMediaType)
	response := httptest.NewRecorder()
	service.ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf("%s status = %d, want %d; body = %x", operation, response.Code, wantStatus, response.Body.Bytes())
	}
	if response.Header().Get("Content-Type") != rendezvous.ProtobufMediaType {
		t.Fatalf("%s content type = %q", operation, response.Header().Get("Content-Type"))
	}
	if err := proto.Unmarshal(response.Body.Bytes(), responseMessage); err != nil {
		t.Fatalf("decode %s response: %v", operation, err)
	}
}
