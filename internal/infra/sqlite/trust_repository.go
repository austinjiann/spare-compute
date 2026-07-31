package sqlite

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/trust"
)

const trustColumns = `
	device_id,
	pair_id,
	public_key,
	name,
	role,
	state,
	platform,
	arch,
	logical_cpu_count,
	total_memory_bytes,
	tool_ids_json,
	hints_observed_at_ns,
	paired_at_ns,
	updated_at_ns,
	revoked_at_ns,
	connectivity_secret
`

// TrustRepository persists public-key pins and their revocation audit events.
type TrustRepository struct {
	database *sql.DB
}

var _ trust.Repository = (*TrustRepository)(nil)

// Activate records newly confirmed trust. Existing active trust must be revoked
// before a new pairing can replace it.
func (repository *TrustRepository) Activate(ctx context.Context, peer trust.Peer) error {
	if err := peer.Validate(); err != nil {
		return err
	}
	if peer.State != trust.StateActive {
		return trust.ErrInvalidPeer
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin trust activation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	existing, loadErr := queryTrustedPeer(ctx, transaction, peer.DeviceID)
	switch {
	case loadErr == nil && existing.State == trust.StateActive:
		if existing.PairID == peer.PairID && bytes.Equal(existing.PublicKey, peer.PublicKey) {
			return nil
		}
		return fmt.Errorf("%w: revoke %s before pairing it again", trust.ErrConflict, peer.DeviceID)
	case loadErr != nil && !errors.Is(loadErr, sql.ErrNoRows):
		return fmt.Errorf("load existing trust: %w", loadErr)
	}

	if peer.Role == device.RoleOrchestrator {
		var activeID string
		err := transaction.QueryRowContext(ctx, `
			SELECT device_id FROM trusted_devices
			WHERE role = 'orchestrator' AND state = 'active' AND device_id <> ?
			LIMIT 1
		`, peer.DeviceID).Scan(&activeID)
		if err == nil {
			return fmt.Errorf("%w: worker already trusts orchestrator %s", trust.ErrConflict, activeID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check existing orchestrator trust: %w", err)
		}
	}

	_, err = transaction.ExecContext(ctx, `
		INSERT INTO trusted_devices (
			device_id, pair_id, public_key, name, role, state,
			paired_at_ns, updated_at_ns, revoked_at_ns, connectivity_secret
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)
		ON CONFLICT(device_id) DO UPDATE SET
			pair_id = excluded.pair_id,
			public_key = excluded.public_key,
			name = excluded.name,
			role = excluded.role,
			state = excluded.state,
			paired_at_ns = excluded.paired_at_ns,
			updated_at_ns = excluded.updated_at_ns,
			revoked_at_ns = NULL,
			connectivity_secret = excluded.connectivity_secret
		WHERE trusted_devices.state = 'revoked'
	`, peer.DeviceID, peer.PairID, []byte(peer.PublicKey), peer.Name, peer.Role, peer.State,
		peer.PairedAt.UTC().UnixNano(), peer.UpdatedAt.UTC().UnixNano(), nullableSecret(peer.ConnectivitySecret))
	if err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("%w: activate trust for %s", trust.ErrConflict, peer.DeviceID)
		}
		return fmt.Errorf("activate trust for %s: %w", peer.DeviceID, err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO trust_events (device_id, pair_id, event, at_ns)
		VALUES (?, ?, 'paired', ?)
	`, peer.DeviceID, peer.PairID, peer.PairedAt.UTC().UnixNano()); err != nil {
		return fmt.Errorf("record paired trust event: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit trust activation: %w", err)
	}
	return nil
}

// Get loads a trusted peer by its durable device ID.
func (repository *TrustRepository) Get(ctx context.Context, id device.ID) (trust.Peer, error) {
	if !id.Valid() {
		return trust.Peer{}, device.ErrInvalidID
	}
	peer, err := queryTrustedPeer(ctx, repository.database, id)
	if errors.Is(err, sql.ErrNoRows) {
		return trust.Peer{}, fmt.Errorf("%w: %s", trust.ErrNotFound, id)
	}
	if err != nil {
		return trust.Peer{}, fmt.Errorf("get trusted peer %s: %w", id, err)
	}
	return peer, nil
}

// List returns active peers before revoked peers, then sorts by name and ID.
func (repository *TrustRepository) List(ctx context.Context) ([]trust.Peer, error) {
	rows, err := repository.database.QueryContext(ctx, `
		SELECT `+trustColumns+` FROM trusted_devices
		ORDER BY CASE state WHEN 'active' THEN 0 ELSE 1 END, name, device_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list trusted peers: %w", err)
	}
	defer rows.Close()
	peers := make([]trust.Peer, 0)
	for rows.Next() {
		peer, err := scanTrustedPeer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan trusted peer: %w", err)
		}
		peers = append(peers, peer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trusted peers: %w", err)
	}
	return peers, nil
}

// Revoke atomically disables a public-key pin and appends an audit event.
func (repository *TrustRepository) Revoke(ctx context.Context, id device.ID, at time.Time) (trust.Peer, error) {
	if !id.Valid() || at.IsZero() {
		return trust.Peer{}, trust.ErrInvalidPeer
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return trust.Peer{}, fmt.Errorf("begin trust revocation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	peer, err := queryTrustedPeer(ctx, transaction, id)
	if errors.Is(err, sql.ErrNoRows) {
		return trust.Peer{}, fmt.Errorf("%w: %s", trust.ErrNotFound, id)
	}
	if err != nil {
		return trust.Peer{}, fmt.Errorf("load trust for revocation: %w", err)
	}
	if peer.State == trust.StateRevoked {
		return peer, nil
	}
	at = at.UTC()
	if at.Before(peer.UpdatedAt) {
		// Revocation must remain available even if the wall clock moved backward.
		at = peer.UpdatedAt
	}
	result, err := transaction.ExecContext(ctx, `
		UPDATE trusted_devices
		SET state = 'revoked', updated_at_ns = ?, revoked_at_ns = ?, connectivity_secret = NULL
		WHERE device_id = ? AND state = 'active' AND updated_at_ns = ?
	`, at.UnixNano(), at.UnixNano(), id, peer.UpdatedAt.UTC().UnixNano())
	if err != nil {
		return trust.Peer{}, fmt.Errorf("revoke trusted peer: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return trust.Peer{}, fmt.Errorf("count revoked peers: %w", err)
	}
	if changed != 1 {
		return trust.Peer{}, fmt.Errorf("%w: trusted peer changed concurrently", trust.ErrConflict)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO trust_events (device_id, pair_id, event, at_ns)
		VALUES (?, ?, 'revoked', ?)
	`, id, peer.PairID, at.UnixNano()); err != nil {
		return trust.Peer{}, fmt.Errorf("record revoked trust event: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return trust.Peer{}, fmt.Errorf("commit trust revocation: %w", err)
	}
	peer.State = trust.StateRevoked
	peer.UpdatedAt = at
	peer.RevokedAt = &at
	peer.ConnectivitySecret = nil
	return peer, nil
}

// UpdateHints records the newest non-authoritative compatibility/resource
// hints observed for an active trusted peer. Older observations are ignored.
func (repository *TrustRepository) UpdateHints(
	ctx context.Context,
	id device.ID,
	hints trust.PeerHints,
) (trust.Peer, error) {
	if !id.Valid() {
		return trust.Peer{}, device.ErrInvalidID
	}
	if err := hints.Validate(); err != nil {
		return trust.Peer{}, err
	}
	hints.ObservedAt = hints.ObservedAt.UTC()
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return trust.Peer{}, fmt.Errorf("begin trust hint update: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	peer, err := queryTrustedPeer(ctx, transaction, id)
	if errors.Is(err, sql.ErrNoRows) {
		return trust.Peer{}, fmt.Errorf("%w: %s", trust.ErrNotFound, id)
	}
	if err != nil {
		return trust.Peer{}, fmt.Errorf("load trust for hint update: %w", err)
	}
	if peer.State != trust.StateActive {
		return peer, nil
	}
	if peer.HintsObservedAt != nil && hints.ObservedAt.Before(*peer.HintsObservedAt) {
		return peer, nil
	}
	result, err := transaction.ExecContext(ctx, `
		UPDATE trusted_devices
		SET platform = ?, arch = ?, logical_cpu_count = ?,
			total_memory_bytes = ?, tool_ids_json = ?, hints_observed_at_ns = ?
		WHERE device_id = ? AND state = 'active'
	`, hints.Platform, hints.Architecture, hints.LogicalCPUCount,
		int64(hints.TotalMemoryBytes), toolIDsJSON(hints.ToolIDs),
		hints.ObservedAt.UnixNano(), id)
	if err != nil {
		return trust.Peer{}, fmt.Errorf("update trusted peer hints: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return trust.Peer{}, fmt.Errorf("count trusted peer hint updates: %w", err)
	}
	if changed != 1 {
		return trust.Peer{}, fmt.Errorf("%w: trusted peer changed concurrently", trust.ErrConflict)
	}
	peer, err = queryTrustedPeer(ctx, transaction, id)
	if err != nil {
		return trust.Peer{}, fmt.Errorf("load updated trusted peer hints: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return trust.Peer{}, fmt.Errorf("commit trusted peer hint update: %w", err)
	}
	return peer, nil
}

type trustRowScanner interface {
	Scan(...any) error
}

type trustRowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func queryTrustedPeer(ctx context.Context, queryer trustRowQueryer, id device.ID) (trust.Peer, error) {
	return scanTrustedPeer(queryer.QueryRowContext(
		ctx, `SELECT `+trustColumns+` FROM trusted_devices WHERE device_id = ?`, id,
	))
}

func scanTrustedPeer(scanner trustRowScanner) (trust.Peer, error) {
	var (
		deviceID, pairID, name, role, state string
		platform, architecture              string
		publicKey, connectivitySecret       []byte
		toolIDsJSONValue                    string
		pairedAtNS, updatedAtNS             int64
		logicalCPUCount                     int64
		totalMemoryBytes                    int64
		revokedAtNS, hintsObservedAtNS      sql.NullInt64
	)
	if err := scanner.Scan(
		&deviceID, &pairID, &publicKey, &name, &role, &state,
		&platform, &architecture, &logicalCPUCount, &totalMemoryBytes, &toolIDsJSONValue, &hintsObservedAtNS,
		&pairedAtNS, &updatedAtNS, &revokedAtNS, &connectivitySecret,
	); err != nil {
		return trust.Peer{}, err
	}
	parsedDeviceID, err := device.ParseID(deviceID)
	if err != nil {
		return trust.Peer{}, err
	}
	parsedPairID, err := trust.ParsePairID(pairID)
	if err != nil {
		return trust.Peer{}, err
	}
	toolIDs, err := parseToolIDsJSON(toolIDsJSONValue)
	if err != nil {
		return trust.Peer{}, trust.ErrInvalidPeer
	}
	peer := trust.Peer{
		PairID: parsedPairID, DeviceID: parsedDeviceID,
		PublicKey:          ed25519.PublicKey(append([]byte(nil), publicKey...)),
		ConnectivitySecret: append(trust.ConnectivitySecret(nil), connectivitySecret...),
		Name:               name, Role: device.Role(role), State: trust.State(state),
		Platform: platform, Architecture: architecture,
		LogicalCPUCount: uint32(logicalCPUCount), TotalMemoryBytes: uint64(totalMemoryBytes),
		ToolIDs:  toolIDs,
		PairedAt: time.Unix(0, pairedAtNS).UTC(), UpdatedAt: time.Unix(0, updatedAtNS).UTC(),
	}
	if logicalCPUCount < 0 || totalMemoryBytes < 0 {
		return trust.Peer{}, trust.ErrInvalidPeer
	}
	if hintsObservedAtNS.Valid {
		observedAt := time.Unix(0, hintsObservedAtNS.Int64).UTC()
		peer.HintsObservedAt = &observedAt
	}
	if revokedAtNS.Valid {
		revokedAt := time.Unix(0, revokedAtNS.Int64).UTC()
		peer.RevokedAt = &revokedAt
	}
	if err := peer.Validate(); err != nil {
		return trust.Peer{}, err
	}
	return peer, nil
}

func nullableSecret(secret trust.ConnectivitySecret) any {
	if len(secret) == 0 {
		return nil
	}
	return []byte(secret)
}

func toolIDsJSON(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func parseToolIDsJSON(value string) ([]string, error) {
	if value == "" {
		value = "[]"
	}
	var decoded []string
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return nil, err
	}
	if decoded == nil {
		return []string{}, nil
	}
	return decoded, nil
}
