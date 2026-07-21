package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

func classLabel(class string) string {
	switch class {
	case "quota_exhausted":
		return "Quota exhausted"
	case "rate_limited":
		return "Rate limited"
	case "auth_failed":
		return "Auth failed"
	case "auth_invalid_key":
		return "Invalid API key"
	case "auth_expired":
		return "Auth expired"
	case "billing_required":
		return "Billing required"
	case "permission_denied":
		return "Permission denied"
	case "invalid_request":
		return "Invalid request"
	case "invalid_model":
		return "Invalid model"
	case "context_length":
		return "Context length exceeded"
	case "upstream_5xx":
		return "Upstream 5xx error"
	case "upstream_internal_error":
		return "Upstream internal error"
	case "upstream_not_implemented":
		return "Upstream not implemented"
	case "upstream_bad_gateway":
		return "Upstream bad gateway"
	case "upstream_unavailable":
		return "Upstream unavailable"
	case "upstream_timeout":
		return "Upstream timeout"
	case "upstream_connection_refused":
		return "Upstream connection refused"
	case "upstream_connection_reset":
		return "Upstream connection reset"
	case "upstream_dns_error":
		return "Upstream DNS error"
	case "upstream_network_unreachable":
		return "Upstream network unreachable"
	case "upstream_tls_error":
		return "Upstream TLS error"
	case "upstream_overloaded":
		return "Upstream overloaded"
	case "not_found":
		return "Not found"
	case "request_timeout":
		return "Request timeout"
	case "conflict":
		return "Conflict"
	case "request_too_large":
		return "Request too large"
	case "validation_error":
		return "Validation error"
	case "proxy_upstream_error":
		return "Proxy upstream error"
	case "proxy_connection_refused":
		return "Proxy connection refused"
	case "proxy_connection_reset":
		return "Proxy connection reset"
	case "proxy_timeout":
		return "Proxy upstream timeout"
	case "proxy_dns_error":
		return "Proxy DNS error"
	case "proxy_network_unreachable":
		return "Proxy network unreachable"
	case "proxy_tls_error":
		return "Proxy TLS error"
	case "proxy_connection_closed":
		return "Proxy connection closed"
	case "capture_parse_error":
		return "Capture parse error"
	case "db_write_error":
		return "DB write error"
	case "dropped_event":
		return "Dropped event"
	case "response_completed_without_usage":
		return "Response completed without usage data"
	case "stream_ended_without_completed":
		return "Stream ended without completion"
	case "response_error_event":
		return "Response error event"
	case "response_incomplete":
		return "Response incomplete"
	case "credential_unavailable":
		return "Credential unavailable"
	case "credential_disabled":
		return "Credential disabled"
	case "quota_low":
		return "Quota low"
	case "quota_refresh_failed":
		return "Quota refresh failed"
	case "quota_stale":
		return "Quota data stale"
	case "quota_unsupported":
		return "Quota unsupported"
	case "quota_unknown":
		return "Quota unknown"
	case "credential_error":
		return "Credential health error"
	case "credential_stale":
		return "Credential health stale"
	case "credential_quota_limited":
		return "Credential quota signal"
	case "credential_history_warning":
		return "Credential history warning"
	case "usage_conflict":
		return "Usage conflict"
	case "side_channel_duplicate":
		return "Duplicate side-channel usage"
	case "side_channel_expired":
		return "Expired side-channel usage"
	case "side_channel_invalid_payload":
		return "Invalid side-channel payload"
	case "side_channel_unmatched":
		return "Unmatched side-channel usage"
	default:
		return "Unclassified issue"
	}
}

func classSeverity(class string) string {
	switch class {
	case "auth_failed", "auth_invalid_key", "auth_expired", "billing_required", "permission_denied", "quota_exhausted",
		"proxy_upstream_error", "proxy_connection_refused", "proxy_connection_reset",
		"proxy_timeout", "proxy_dns_error", "proxy_network_unreachable", "proxy_tls_error", "db_write_error",
		"response_error_event", "credential_error", "credential_disabled", "usage_conflict":
		return "error"
	case "rate_limited", "upstream_5xx", "upstream_internal_error", "upstream_not_implemented", "upstream_bad_gateway",
		"upstream_unavailable", "upstream_timeout", "upstream_connection_refused", "upstream_connection_reset",
		"upstream_dns_error", "upstream_network_unreachable", "upstream_tls_error", "upstream_overloaded",
		"context_length", "invalid_request", "invalid_model", "not_found", "conflict", "validation_error",
		"request_timeout", "request_too_large", "capture_parse_error", "dropped_event",
		"response_completed_without_usage", "stream_ended_without_completed", "response_incomplete",
		"proxy_connection_closed", "credential_unavailable", "credential_stale", "quota_low", "quota_refresh_failed", "quota_stale", "quota_unsupported", "quota_unknown",
		"credential_quota_limited", "credential_history_warning", "side_channel_expired", "side_channel_invalid_payload", "side_channel_unmatched":
		return "warning"
	default:
		return "info"
	}
}

func (db *DB) CaptureOutcomeCounts(since time.Time) (captured, skipped, failed int64, err error) {
	if err := db.read.QueryRow(`
		SELECT COUNT(*) FROM request_usage
		WHERE created_at_unix >= ? AND capture_outcome = 'captured'
	`, since.Unix()).Scan(&captured); err != nil {
		return 0, 0, 0, fmt.Errorf("capture outcome count captured: %w", err)
	}
	if err := db.read.QueryRow(`
		SELECT COUNT(*) FROM request_usage
		WHERE created_at_unix >= ? AND capture_outcome = 'skipped'
	`, since.Unix()).Scan(&skipped); err != nil {
		return 0, 0, 0, fmt.Errorf("capture outcome count skipped: %w", err)
	}
	if err := db.read.QueryRow(`
		SELECT COUNT(*) FROM request_usage
		WHERE created_at_unix >= ? AND capture_outcome = 'failed'
	`, since.Unix()).Scan(&failed); err != nil {
		return 0, 0, 0, fmt.Errorf("capture outcome count failed: %w", err)
	}
	return captured, skipped, failed, nil
}

// GatewayCapabilityRow is one per-endpoint_profile aggregate for the gateway
// capability view.
type GatewayCapabilityRow struct {
	EndpointProfile   string `json:"endpoint_profile"`
	RequestCount      int64  `json:"request_count"`
	StreamCount       int64  `json:"stream_count"`
	MissingUsageCount int64  `json:"missing_usage_count"`
	UsageMeteredCount int64  `json:"usage_metered_count"`
	RequestOnlyCount  int64  `json:"request_only_count"`
	PassthroughCount  int64  `json:"passthrough_count"`
}

// VerifySaltFingerprint enforces the salt-consistency invariant (CLAUDE.md #7).
// On a fresh DB (no request_usage data, no stored fingerprint) it binds the
// current fingerprint. On a legacy DB (has data, no fingerprint) it performs a
// one-time legacy bind. If a fingerprint is already stored and does not match,
// it returns an error telling the operator how to recover. The salt itself is
// never stored; only the non-reversible fingerprint is persisted.
func (db *DB) VerifySaltFingerprint(fingerprint, dbPath, saltPath string) error {
	var hasData bool
	if err := db.read.QueryRow(`SELECT EXISTS(SELECT 1 FROM request_usage LIMIT 1)`).Scan(&hasData); err != nil {
		return fmt.Errorf("check request_usage data: %w", err)
	}

	var stored string
	err := db.read.QueryRow(`SELECT value FROM db_metadata WHERE key = 'salt_fingerprint'`).Scan(&stored)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read salt fingerprint: %w", err)
	}
	hasFingerprint := err == nil

	if !hasFingerprint {
		// Fresh DB or legacy bind: write the current fingerprint.
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := db.sql.Exec(
			`INSERT INTO db_metadata (key, value, updated_at) VALUES ('salt_fingerprint', ?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			fingerprint, now,
		); err != nil {
			return fmt.Errorf("write salt fingerprint: %w", err)
		}
		if hasData {
			log.Printf("salt fingerprint: legacy DB bound to current salt file (one-time migration)")
		}
		return nil
	}

	if stored != fingerprint {
		return fmt.Errorf(
			"salt file has changed but the database already has historical data; "+
				"changing the salt breaks all historical api_key_hash and client_ip_hash groupings.\n"+
				"  database: %s\n"+
				"  salt file: %s\n"+
				"  stored fingerprint: %s\n"+
				"  current fingerprint: %s\n"+
				"recovery: restore the original salt file from backup (it must be backed up alongside the SQLite DB), "+
				"or start with a fresh database if you do not need historical grouping",
			dbPath, saltPath, stored, fingerprint,
		)
	}
	return nil
}

// GatewayCapabilities aggregates request_usage by endpoint_profile for the
// gateway capability view. Rows cover only profiles that had traffic in the
// range; the webui layer merges in zero-traffic profiles from the registry.
func (db *DB) GatewayCapabilities(since time.Time) ([]GatewayCapabilityRow, error) {
	rows, err := db.read.Query(`
		SELECT
			COALESCE(NULLIF(endpoint_profile, ''), 'unknown') AS endpoint_profile,
			COUNT(*) AS request_count,
			SUM(CASE WHEN stream = 1 THEN 1 ELSE 0 END) AS stream_count,
			SUM(CASE WHEN capture_mode = 'usage_metered' AND capture_outcome != 'captured' THEN 1 ELSE 0 END) AS missing_usage_count,
			SUM(CASE WHEN capture_mode = 'usage_metered' THEN 1 ELSE 0 END) AS usage_metered_count,
			SUM(CASE WHEN capture_mode = 'request_only' THEN 1 ELSE 0 END) AS request_only_count,
			SUM(CASE WHEN capture_mode = 'passthrough' THEN 1 ELSE 0 END) AS passthrough_count
		FROM request_usage
		WHERE created_at_unix >= ?
		GROUP BY COALESCE(NULLIF(endpoint_profile, ''), 'unknown')
	`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("gateway capabilities query: %w", err)
	}
	defer rows.Close()

	var result []GatewayCapabilityRow
	for rows.Next() {
		var r GatewayCapabilityRow
		if err := rows.Scan(
			&r.EndpointProfile,
			&r.RequestCount,
			&r.StreamCount,
			&r.MissingUsageCount,
			&r.UsageMeteredCount,
			&r.RequestOnlyCount,
			&r.PassthroughCount,
		); err != nil {
			return nil, fmt.Errorf("gateway capabilities scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
