package report

import "time"

// ModelsFilter is the typed read filter for the models report.
// Key filtering is intentionally out of scope for this vertical slice.
type ModelsFilter struct {
	Since time.Time
}

type SummaryFilter struct {
	Since time.Time
}

type TimeseriesFilter struct {
	Since     time.Time
	BucketMin int
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
