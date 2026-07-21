package db

import (
	"time"
)

// ReportScope is the common exact scope for usage reports. KeyHash is either
// empty (all keys), "unknown", or a validated full lowercase hash.
type ReportScope struct {
	Since   time.Time
	KeyHash string
}

func reportScopeWhere(scope ReportScope) (string, []any) {
	where := "created_at_unix >= ?"
	args := []any{scope.Since.Unix()}
	switch {
	case scope.KeyHash == "":
	case scope.KeyHash == "unknown":
		where += " AND NULLIF(TRIM(api_key_hash), '') IS NULL"
	default:
		where += " AND api_key_hash = ?"
		args = append(args, scope.KeyHash)
	}
	return where, args
}
