package profile

// EndpointMeta is the stable metadata API description of a known endpoint profile.
// It lives in profile so metadata assembly does not depend on read-side report packages.
type EndpointMeta struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	FilterValue  string `json:"filter_value"`
	Method       string `json:"method"`
	DisplayName  string `json:"display_name"`
	MeteringKind string `json:"metering_kind"`
	CaptureMode  string `json:"capture_mode"`
}

// GatewayProfileInfo is the static capability-matrix entry used by gateway reports.
type GatewayProfileInfo struct {
	Name             string
	DisplayName      string
	CaptureMode      string
	MeteringKind     string
	KnownLimitations []string
}
