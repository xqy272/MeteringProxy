package report

import (
	"fmt"
	"time"
)

// ModelsFilter is the typed read filter for the models report.
// Key filtering is intentionally out of scope for this vertical slice.
type ModelsFilter struct {
	Since   time.Time
	KeyHash string
}

type SummaryFilter struct {
	Since time.Time
}

type TimeseriesFilter struct {
	Since     time.Time
	BucketMin int
	KeyHash   string
}

func ValidateKeyHashFilter(value string) error {
	if value == "" || value == "unknown" {
		return nil
	}
	if len(value) != 64 {
		return fmt.Errorf("key_hash must be a 64-character lowercase hex value or unknown")
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("key_hash must be a 64-character lowercase hex value or unknown")
		}
	}
	return nil
}

type ImagesFilter struct {
	Since time.Time
}

type OverviewFilter struct {
	Since time.Time
	Range string
}

type ModelAssetsFilter struct {
	Since time.Time
	Range string
}

type KeysFilter struct {
	Since time.Time
}

type ActivityFilter struct {
	Since   time.Time
	KeyHash string
}

type RequestFilter struct {
	Since      time.Time
	KeyHash    string
	Limit      int
	StatusMin  int
	StatusMax  int
	Model      string
	Endpoint   string
	ErrorClass string
}
