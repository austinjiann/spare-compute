package rendezvous

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	connectivityv1 "github.com/austinjiann/spare-compute/gen/go/computehop/connectivity/v1"
	"github.com/austinjiann/spare-compute/internal/connectivity"
	"google.golang.org/protobuf/proto"
)

const maximumClientResponseBytes = 1 << 20

var (
	ErrInvalidClientConfiguration = errors.New("invalid rendezvous client configuration")
	ErrInvalidClientRequest       = errors.New("invalid rendezvous client request")
	ErrExpiredAccess              = errors.New("rendezvous access expired")
	ErrInvalidServiceResponse     = errors.New("invalid rendezvous service response")
)

// ClientConfig controls the bounded HTTPS rendezvous client.
type ClientConfig struct {
	BaseURL           string
	HTTPClient        *http.Client
	Now               func() time.Time
	AllowLoopbackHTTP bool
}

// Client exchanges opaque pair-scoped payloads with a connectivity service.
type Client struct {
	baseURL    string
	httpClient *http.Client
	now        func() time.Time
}

// RemoteError is a typed failure returned by the connectivity service.
type RemoteError struct {
	StatusCode int
	Code       connectivityv1.RendezvousErrorCode
	Message    string
}

func (err *RemoteError) Error() string {
	return fmt.Sprintf("rendezvous service: %s", err.Message)
}

// NewClient validates the service origin and disables redirects so bearer
// credentials can never be forwarded to another endpoint.
func NewClient(config ClientConfig) (*Client, error) {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed == nil {
		return nil, ErrInvalidClientConfiguration
	}
	hostIP := net.ParseIP(parsed.Hostname())
	loopbackHTTP := parsed.Scheme == "http" && config.AllowLoopbackHTTP &&
		(parsed.Hostname() == "localhost" || (hostIP != nil && hostIP.IsLoopback()))
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") ||
		(parsed.Scheme != "https" && !loopbackHTTP) {
		return nil, ErrInvalidClientConfiguration
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	if config.HTTPClient != nil {
		clone := *config.HTTPClient
		httpClient = &clone
	}
	if httpClient.Timeout <= 0 {
		httpClient.Timeout = 10 * time.Second
	}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{
		baseURL: strings.TrimSuffix(parsed.String(), "/"), httpClient: httpClient, now: config.Now,
	}, nil
}

// PublishPresence refreshes one encrypted endpoint presence document.
func (client *Client) PublishPresence(
	ctx context.Context,
	access connectivity.Access,
	role connectivityv1.EndpointRole,
	generation uint64,
	encryptedPayload []byte,
) (*connectivityv1.PublishPresenceResponse, error) {
	if !validEndpointRole(role) || generation == 0 ||
		len(encryptedPayload) == 0 || len(encryptedPayload) > connectivity.MaximumCiphertextBytes {
		return nil, ErrInvalidClientRequest
	}
	request := &connectivityv1.PublishPresenceRequest{
		ProtocolVersion: ProtocolVersion, Role: role, Generation: generation,
		EncryptedPayload: append([]byte(nil), encryptedPayload...),
	}
	response := &connectivityv1.PublishPresenceResponse{}
	if err := client.post(ctx, access, "presence", request, response); err != nil {
		return nil, err
	}
	if response.GetExpiresAtUnixNano() <= client.now().UTC().UnixNano() {
		return nil, ErrInvalidServiceResponse
	}
	if peer := response.GetPeer(); peer != nil {
		if !validEndpointRole(peer.GetRole()) || peer.GetRole() == role || peer.GetGeneration() == 0 ||
			len(peer.GetEncryptedPayload()) == 0 ||
			len(peer.GetEncryptedPayload()) > connectivity.MaximumCiphertextBytes ||
			peer.GetExpiresAtUnixNano() <= client.now().UTC().UnixNano() {
			return nil, ErrInvalidServiceResponse
		}
	}
	return response, nil
}

// SendSignal queues one encrypted signaling payload for the opposite endpoint.
func (client *Client) SendSignal(
	ctx context.Context,
	access connectivity.Access,
	sender connectivityv1.EndpointRole,
	recipient connectivityv1.EndpointRole,
	encryptedPayload []byte,
) (*connectivityv1.SendSignalResponse, error) {
	if !validEndpointRole(sender) || !validEndpointRole(recipient) || sender == recipient ||
		len(encryptedPayload) == 0 || len(encryptedPayload) > connectivity.MaximumCiphertextBytes {
		return nil, ErrInvalidClientRequest
	}
	request := &connectivityv1.SendSignalRequest{
		ProtocolVersion: ProtocolVersion, Sender: sender, Recipient: recipient,
		EncryptedPayload: append([]byte(nil), encryptedPayload...),
	}
	response := &connectivityv1.SendSignalResponse{}
	if err := client.post(ctx, access, "signals", request, response); err != nil {
		return nil, err
	}
	if response.GetSequence() == 0 || response.GetExpiresAtUnixNano() <= client.now().UTC().UnixNano() {
		return nil, ErrInvalidServiceResponse
	}
	return response, nil
}

// PollSignals reads bounded signaling records after a durable caller cursor.
func (client *Client) PollSignals(
	ctx context.Context,
	access connectivity.Access,
	recipient connectivityv1.EndpointRole,
	afterSequence uint64,
	limit uint32,
) (*connectivityv1.PollSignalsResponse, error) {
	if !validEndpointRole(recipient) || limit > maximumPollLimit {
		return nil, ErrInvalidClientRequest
	}
	request := &connectivityv1.PollSignalsRequest{
		ProtocolVersion: ProtocolVersion, Recipient: recipient,
		AfterSequence: afterSequence, Limit: limit,
	}
	response := &connectivityv1.PollSignalsResponse{}
	if err := client.post(ctx, access, "poll", request, response); err != nil {
		return nil, err
	}
	previous := afterSequence
	for _, signal := range response.GetSignals() {
		if signal.GetSequence() <= previous || !validEndpointRole(signal.GetSender()) ||
			signal.GetSender() == recipient || len(signal.GetEncryptedPayload()) == 0 ||
			len(signal.GetEncryptedPayload()) > connectivity.MaximumCiphertextBytes ||
			signal.GetCreatedAtUnixNano() <= 0 || signal.GetExpiresAtUnixNano() <= signal.GetCreatedAtUnixNano() {
			return nil, ErrInvalidServiceResponse
		}
		previous = signal.GetSequence()
	}
	if response.GetNextSequence() < previous {
		return nil, ErrInvalidServiceResponse
	}
	return response, nil
}

func (client *Client) post(
	ctx context.Context,
	access connectivity.Access,
	operation string,
	requestMessage proto.Message,
	responseMessage proto.Message,
) error {
	if err := validateAccess(access, client.now().UTC()); err != nil {
		return err
	}
	contents, err := proto.Marshal(requestMessage)
	if err != nil {
		return fmt.Errorf("%w: encode request: %v", ErrInvalidClientRequest, err)
	}
	endpoint := client.baseURL + "/v1/rendezvous/" + url.PathEscape(access.RouteID) + "/" + operation
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(contents))
	if err != nil {
		return fmt.Errorf("%w: create HTTP request: %v", ErrInvalidClientRequest, err)
	}
	request.Header.Set("Authorization", "Bearer "+access.Token)
	request.Header.Set("Content-Type", ProtobufMediaType)
	request.Header.Set("Accept", ProtobufMediaType)
	request.Header.Set("User-Agent", "ComputeHop/1")

	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("rendezvous request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumClientResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%w: read response: %v", ErrInvalidServiceResponse, err)
	}
	if len(body) > maximumClientResponseBytes {
		return fmt.Errorf("%w: response is too large", ErrInvalidServiceResponse)
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK {
		remote := &connectivityv1.ErrorResponse{}
		if mediaErr == nil && mediaType == ProtobufMediaType && proto.Unmarshal(body, remote) == nil &&
			remote.GetCode() != connectivityv1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_UNSPECIFIED &&
			remote.GetMessage() != "" {
			return &RemoteError{StatusCode: response.StatusCode, Code: remote.GetCode(), Message: remote.GetMessage()}
		}
		return fmt.Errorf("%w: HTTP status %d", ErrInvalidServiceResponse, response.StatusCode)
	}
	if mediaErr != nil || mediaType != ProtobufMediaType || len(body) == 0 || proto.Unmarshal(body, responseMessage) != nil {
		return ErrInvalidServiceResponse
	}
	return nil
}

func validateAccess(access connectivity.Access, now time.Time) error {
	if !validEncodedCredential(access.RouteID) || !validEncodedCredential(access.Token) || access.ExpiresAt.IsZero() {
		return ErrInvalidClientRequest
	}
	if !now.Before(access.ExpiresAt) {
		return ErrExpiredAccess
	}
	return nil
}

func validEncodedCredential(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == value
}

func validEndpointRole(role connectivityv1.EndpointRole) bool {
	return role == connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR ||
		role == connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER
}
