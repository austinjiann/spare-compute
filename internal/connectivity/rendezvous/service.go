// Package rendezvous implements the hosted, payload-opaque connectivity
// service used by already-paired ComputeHop devices.
package rendezvous

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	connectivityv1 "github.com/austinjiann/spare-compute/gen/go/computehop/connectivity/v1"
	"google.golang.org/protobuf/proto"
)

const (
	ProtocolVersion   = 1
	ProtobufMediaType = "application/x-protobuf"

	defaultPresenceLifetime    = 90 * time.Second
	defaultSignalLifetime      = 2 * time.Minute
	defaultRouteLifetime       = 6 * time.Minute
	defaultMaxRoutes           = 10_000
	defaultMaxSignalsPerRoute  = 64
	defaultMaxPayloadBytes     = 32 << 10
	defaultMaxRequestsPerRoute = 240
	maximumPollLimit           = 64
	defaultPollLimit           = 32
)

var ErrInvalidConfiguration = errors.New("invalid rendezvous configuration")

// Config bounds all in-memory state held by the anonymous service.
type Config struct {
	Now                 func() time.Time
	PresenceLifetime    time.Duration
	SignalLifetime      time.Duration
	RouteLifetime       time.Duration
	MaxRoutes           int
	MaxSignalsPerRoute  int
	MaxPayloadBytes     int
	MaxRequestsPerRoute int
}

// Service stores only short-lived opaque payloads and credential digests.
type Service struct {
	now                 func() time.Time
	presenceLifetime    time.Duration
	signalLifetime      time.Duration
	routeLifetime       time.Duration
	maxRoutes           int
	maxSignalsPerRoute  int
	maxPayloadBytes     int
	maxRequestBytes     int64
	maxRequestsPerRoute int

	mu     sync.Mutex
	routes map[string]*route
}

type route struct {
	authDigest   [sha256.Size]byte
	expiresAt    time.Time
	windowStart  time.Time
	requestCount int
	nextSequence uint64
	presences    map[endpointRole]storedPresence
	signals      map[endpointRole][]storedSignal
}

type endpointRole uint8

const (
	roleOrchestrator endpointRole = iota + 1
	roleWorker
)

type storedPresence struct {
	role       endpointRole
	generation uint64
	payload    []byte
	expiresAt  time.Time
}

type storedSignal struct {
	sequence  uint64
	sender    endpointRole
	payload   []byte
	createdAt time.Time
	expiresAt time.Time
}

type serviceError struct {
	status  int
	code    connectivityv1.RendezvousErrorCode
	message string
}

// New constructs a bounded in-memory rendezvous service.
func New(config Config) (*Service, error) {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.PresenceLifetime == 0 {
		config.PresenceLifetime = defaultPresenceLifetime
	}
	if config.SignalLifetime == 0 {
		config.SignalLifetime = defaultSignalLifetime
	}
	if config.RouteLifetime == 0 {
		config.RouteLifetime = defaultRouteLifetime
	}
	if config.MaxRoutes == 0 {
		config.MaxRoutes = defaultMaxRoutes
	}
	if config.MaxSignalsPerRoute == 0 {
		config.MaxSignalsPerRoute = defaultMaxSignalsPerRoute
	}
	if config.MaxPayloadBytes == 0 {
		config.MaxPayloadBytes = defaultMaxPayloadBytes
	}
	if config.MaxRequestsPerRoute == 0 {
		config.MaxRequestsPerRoute = defaultMaxRequestsPerRoute
	}
	if config.PresenceLifetime <= 0 || config.SignalLifetime <= 0 ||
		config.RouteLifetime < time.Minute || config.RouteLifetime > 15*time.Minute ||
		config.PresenceLifetime > config.RouteLifetime || config.SignalLifetime > config.RouteLifetime ||
		config.MaxRoutes < 1 || config.MaxRoutes > 1_000_000 ||
		config.MaxSignalsPerRoute < 1 || config.MaxSignalsPerRoute > 1024 ||
		config.MaxPayloadBytes < 1 || config.MaxPayloadBytes > 1<<20 ||
		config.MaxRequestsPerRoute < 1 || config.MaxRequestsPerRoute > 100_000 {
		return nil, ErrInvalidConfiguration
	}
	return &Service{
		now: config.Now, presenceLifetime: config.PresenceLifetime,
		signalLifetime: config.SignalLifetime, routeLifetime: config.RouteLifetime,
		maxRoutes: config.MaxRoutes, maxSignalsPerRoute: config.MaxSignalsPerRoute,
		maxPayloadBytes:     config.MaxPayloadBytes,
		maxRequestBytes:     int64(config.MaxPayloadBytes + (8 << 10)),
		maxRequestsPerRoute: config.MaxRequestsPerRoute,
		routes:              make(map[string]*route),
	}, nil
}

// ServeHTTP exposes health and three bounded Protobuf operations.
func (service *Service) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	setResponseHeaders(writer.Header())
	if request.URL.Path == "/healthz" {
		if request.Method != http.MethodGet {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok\n"))
		return
	}
	if request.Method != http.MethodPost {
		service.writeError(writer, serviceError{
			status:  http.StatusMethodNotAllowed,
			code:    connectivityv1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_INVALID_ARGUMENT,
			message: "rendezvous operations require POST",
		})
		return
	}
	routeID, operation, ok := parsePath(request.URL.Path)
	if !ok || !validOpaqueValue(routeID) {
		service.writeError(writer, invalidArgument("invalid rendezvous path"))
		return
	}
	token, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok {
		service.writeError(writer, unauthenticated())
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != ProtobufMediaType {
		service.writeError(writer, invalidArgument("content type must be application/x-protobuf"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, service.maxRequestBytes+1))
	if err != nil || int64(len(body)) > service.maxRequestBytes {
		service.writeError(writer, invalidArgument("request body is too large"))
		return
	}
	digest := sha256.Sum256(token)
	switch operation {
	case "presence":
		service.publishPresence(writer, routeID, digest, body)
	case "signals":
		service.sendSignal(writer, routeID, digest, body)
	case "poll":
		service.pollSignals(writer, routeID, digest, body)
	default:
		service.writeError(writer, invalidArgument("invalid rendezvous operation"))
	}
}

func (service *Service) publishPresence(
	writer http.ResponseWriter,
	routeID string,
	authDigest [sha256.Size]byte,
	body []byte,
) {
	request := &connectivityv1.PublishPresenceRequest{}
	if err := proto.Unmarshal(body, request); err != nil {
		service.writeError(writer, invalidArgument("invalid presence request"))
		return
	}
	if protocolErr := validateProtocol(request.GetProtocolVersion()); protocolErr != nil {
		service.writeError(writer, *protocolErr)
		return
	}
	role, roleOK := roleFromProto(request.GetRole())
	if !roleOK || request.GetGeneration() == 0 ||
		!service.validPayload(request.GetEncryptedPayload()) {
		service.writeError(writer, invalidArgument("invalid presence fields"))
		return
	}

	now := service.now().UTC()
	service.mu.Lock()
	defer service.mu.Unlock()
	service.pruneLocked(now)
	value, routeErr := service.routeLocked(routeID, authDigest, now, true)
	if routeErr != nil {
		service.writeError(writer, *routeErr)
		return
	}
	existing, exists := value.presences[role]
	if exists && (request.GetGeneration() < existing.generation ||
		(request.GetGeneration() == existing.generation &&
			!bytes.Equal(request.GetEncryptedPayload(), existing.payload))) {
		service.writeError(writer, serviceError{
			status:  http.StatusConflict,
			code:    connectivityv1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_CONFLICT,
			message: "presence generation is stale or reused with different payload",
		})
		return
	}
	expiresAt := minTime(now.Add(service.presenceLifetime), value.expiresAt)
	value.presences[role] = storedPresence{
		role: role, generation: request.GetGeneration(),
		payload: append([]byte(nil), request.GetEncryptedPayload()...), expiresAt: expiresAt,
	}
	response := &connectivityv1.PublishPresenceResponse{ExpiresAtUnixNano: expiresAt.UnixNano()}
	if peer, ok := value.presences[oppositeRole(role)]; ok && peer.expiresAt.After(now) {
		response.Peer = presenceToProto(peer)
	}
	service.writeProto(writer, http.StatusOK, response)
}

func (service *Service) sendSignal(
	writer http.ResponseWriter,
	routeID string,
	authDigest [sha256.Size]byte,
	body []byte,
) {
	request := &connectivityv1.SendSignalRequest{}
	if err := proto.Unmarshal(body, request); err != nil {
		service.writeError(writer, invalidArgument("invalid signal request"))
		return
	}
	if protocolErr := validateProtocol(request.GetProtocolVersion()); protocolErr != nil {
		service.writeError(writer, *protocolErr)
		return
	}
	sender, senderOK := roleFromProto(request.GetSender())
	recipient, recipientOK := roleFromProto(request.GetRecipient())
	if !senderOK || !recipientOK || recipient != oppositeRole(sender) ||
		!service.validPayload(request.GetEncryptedPayload()) {
		service.writeError(writer, invalidArgument("invalid signal fields"))
		return
	}

	now := service.now().UTC()
	service.mu.Lock()
	defer service.mu.Unlock()
	service.pruneLocked(now)
	value, routeErr := service.routeLocked(routeID, authDigest, now, false)
	if routeErr != nil {
		service.writeError(writer, *routeErr)
		return
	}
	if senderPresence, ok := value.presences[sender]; !ok || !senderPresence.expiresAt.After(now) {
		service.writeError(writer, serviceError{
			status:  http.StatusConflict,
			code:    connectivityv1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_CONFLICT,
			message: "sender must publish current presence before signaling",
		})
		return
	}
	queue := value.signals[recipient]
	if len(queue) >= service.maxSignalsPerRoute {
		service.writeError(writer, exhausted("signal queue is full"))
		return
	}
	value.nextSequence++
	expiresAt := minTime(now.Add(service.signalLifetime), value.expiresAt)
	value.signals[recipient] = append(queue, storedSignal{
		sequence: value.nextSequence, sender: sender,
		payload:   append([]byte(nil), request.GetEncryptedPayload()...),
		createdAt: now, expiresAt: expiresAt,
	})
	service.writeProto(writer, http.StatusOK, &connectivityv1.SendSignalResponse{
		Sequence: value.nextSequence, ExpiresAtUnixNano: expiresAt.UnixNano(),
	})
}

func (service *Service) pollSignals(
	writer http.ResponseWriter,
	routeID string,
	authDigest [sha256.Size]byte,
	body []byte,
) {
	request := &connectivityv1.PollSignalsRequest{}
	if err := proto.Unmarshal(body, request); err != nil {
		service.writeError(writer, invalidArgument("invalid poll request"))
		return
	}
	if protocolErr := validateProtocol(request.GetProtocolVersion()); protocolErr != nil {
		service.writeError(writer, *protocolErr)
		return
	}
	limit := int(request.GetLimit())
	if limit == 0 {
		limit = defaultPollLimit
	}
	recipientRole, roleOK := roleFromProto(request.GetRecipient())
	if !roleOK || limit > maximumPollLimit {
		service.writeError(writer, invalidArgument("invalid poll fields"))
		return
	}

	now := service.now().UTC()
	service.mu.Lock()
	defer service.mu.Unlock()
	service.pruneLocked(now)
	value, routeErr := service.routeLocked(routeID, authDigest, now, false)
	if routeErr != nil {
		service.writeError(writer, *routeErr)
		return
	}
	if recipient, ok := value.presences[recipientRole]; !ok || !recipient.expiresAt.After(now) {
		service.writeError(writer, serviceError{
			status:  http.StatusConflict,
			code:    connectivityv1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_CONFLICT,
			message: "recipient must publish current presence before polling",
		})
		return
	}
	queue := value.signals[recipientRole]
	if request.GetAfterSequence() > 0 {
		kept := queue[:0]
		for _, signal := range queue {
			if signal.sequence > request.GetAfterSequence() {
				kept = append(kept, signal)
			}
		}
		queue = kept
		value.signals[recipientRole] = queue
	}
	response := &connectivityv1.PollSignalsResponse{NextSequence: request.GetAfterSequence()}
	for _, signal := range queue {
		if signal.sequence <= request.GetAfterSequence() {
			continue
		}
		if len(response.Signals) == limit {
			response.HasMore = true
			break
		}
		response.Signals = append(response.Signals, signalToProto(signal))
		response.NextSequence = signal.sequence
	}
	service.writeProto(writer, http.StatusOK, response)
}

func (service *Service) routeLocked(
	routeID string,
	authDigest [sha256.Size]byte,
	now time.Time,
	create bool,
) (*route, *serviceError) {
	value := service.routes[routeID]
	if value == nil {
		if !create {
			result := serviceError{
				status:  http.StatusNotFound,
				code:    connectivityv1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_NOT_FOUND,
				message: "rendezvous route not found",
			}
			return nil, &result
		}
		if len(service.routes) >= service.maxRoutes {
			result := exhausted("rendezvous capacity is full")
			return nil, &result
		}
		value = &route{
			authDigest: authDigest, expiresAt: now.Add(service.routeLifetime),
			windowStart: now, presences: make(map[endpointRole]storedPresence),
			signals: make(map[endpointRole][]storedSignal),
		}
		service.routes[routeID] = value
	} else if subtle.ConstantTimeCompare(value.authDigest[:], authDigest[:]) != 1 {
		result := unauthenticated()
		return nil, &result
	}
	if !now.Before(value.windowStart.Add(time.Minute)) {
		value.windowStart = now
		value.requestCount = 0
	}
	if value.requestCount >= service.maxRequestsPerRoute {
		result := exhausted("rendezvous request rate exceeded")
		return nil, &result
	}
	value.requestCount++
	return value, nil
}

func (service *Service) pruneLocked(now time.Time) {
	for routeID, value := range service.routes {
		if !value.expiresAt.After(now) {
			delete(service.routes, routeID)
			continue
		}
		for role, presence := range value.presences {
			if !presence.expiresAt.After(now) {
				delete(value.presences, role)
			}
		}
		for role, queue := range value.signals {
			kept := queue[:0]
			for _, signal := range queue {
				if signal.expiresAt.After(now) {
					kept = append(kept, signal)
				}
			}
			if len(kept) == 0 {
				delete(value.signals, role)
			} else {
				value.signals[role] = kept
			}
		}
	}
}

func (service *Service) validPayload(payload []byte) bool {
	return len(payload) > 0 && len(payload) <= service.maxPayloadBytes
}

func (service *Service) writeProto(writer http.ResponseWriter, status int, message proto.Message) {
	contents, err := proto.Marshal(message)
	if err != nil {
		service.writeError(writer, serviceError{
			status:  http.StatusInternalServerError,
			code:    connectivityv1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_INTERNAL,
			message: "encode rendezvous response",
		})
		return
	}
	writer.Header().Set("Content-Type", ProtobufMediaType)
	writer.WriteHeader(status)
	_, _ = writer.Write(contents)
}

func (service *Service) writeError(writer http.ResponseWriter, value serviceError) {
	service.writeProto(writer, value.status, &connectivityv1.ErrorResponse{
		Code: value.code, Message: value.message,
	})
}

func parsePath(path string) (string, string, bool) {
	const prefix = "/v1/rendezvous/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func bearerToken(header string) ([]byte, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) || strings.Contains(header[len(prefix):], " ") {
		return nil, false
	}
	token, err := base64.RawURLEncoding.DecodeString(header[len(prefix):])
	return token, err == nil && len(token) == sha256.Size
}

func validOpaqueValue(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size &&
		base64.RawURLEncoding.EncodeToString(decoded) == value
}

func validateProtocol(version uint32) *serviceError {
	if version == ProtocolVersion {
		return nil
	}
	result := serviceError{
		status:  http.StatusUpgradeRequired,
		code:    connectivityv1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_UNSUPPORTED_VERSION,
		message: "unsupported rendezvous protocol version",
	}
	return &result
}

func roleFromProto(role connectivityv1.EndpointRole) (endpointRole, bool) {
	switch role {
	case connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR:
		return roleOrchestrator, true
	case connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER:
		return roleWorker, true
	default:
		return 0, false
	}
}

func roleToProto(role endpointRole) connectivityv1.EndpointRole {
	if role == roleOrchestrator {
		return connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR
	}
	if role == roleWorker {
		return connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER
	}
	return connectivityv1.EndpointRole_ENDPOINT_ROLE_UNSPECIFIED
}

func oppositeRole(role endpointRole) endpointRole {
	if role == roleOrchestrator {
		return roleWorker
	}
	if role == roleWorker {
		return roleOrchestrator
	}
	return 0
}

func presenceToProto(value storedPresence) *connectivityv1.Presence {
	return &connectivityv1.Presence{
		Role: roleToProto(value.role), Generation: value.generation,
		EncryptedPayload:  append([]byte(nil), value.payload...),
		ExpiresAtUnixNano: value.expiresAt.UnixNano(),
	}
}

func signalToProto(value storedSignal) *connectivityv1.Signal {
	return &connectivityv1.Signal{
		Sequence: value.sequence, Sender: roleToProto(value.sender),
		EncryptedPayload:  append([]byte(nil), value.payload...),
		CreatedAtUnixNano: value.createdAt.UnixNano(), ExpiresAtUnixNano: value.expiresAt.UnixNano(),
	}
}

func setResponseHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
}

func invalidArgument(message string) serviceError {
	return serviceError{
		status:  http.StatusBadRequest,
		code:    connectivityv1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_INVALID_ARGUMENT,
		message: message,
	}
}

func unauthenticated() serviceError {
	return serviceError{
		status:  http.StatusUnauthorized,
		code:    connectivityv1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_UNAUTHENTICATED,
		message: "invalid rendezvous credentials",
	}
}

func exhausted(message string) serviceError {
	return serviceError{
		status:  http.StatusTooManyRequests,
		code:    connectivityv1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_RESOURCE_EXHAUSTED,
		message: message,
	}
}

func minTime(first, second time.Time) time.Time {
	if first.Before(second) {
		return first
	}
	return second
}
