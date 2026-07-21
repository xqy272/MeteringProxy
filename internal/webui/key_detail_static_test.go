package webui

import (
	"strings"
	"testing"
)

func readStatic(t *testing.T, name string) string {
	t.Helper()
	b, err := staticFiles.ReadFile("static/" + name)
	if err != nil {
		t.Fatalf("read static/%s: %v", name, err)
	}
	// Reject UTF-8 BOM so browser-facing assets stay clean.
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		t.Fatalf("static/%s has UTF-8 BOM", name)
	}
	return string(b)
}

func requireContains(t *testing.T, body, want, label string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Fatalf("%s: missing %q", label, want)
	}
}

func TestKeyDetailStaticContracts(t *testing.T) {
	index := readStatic(t, "index.html")
	app := readStatic(t, "app.js")
	i18n := readStatic(t, "i18n.js")
	css := readStatic(t, "styles.css")

	// Region and hooks in markup.
	for _, want := range []string{
		`id="key-detail"`,
		`id="key-detail-label"`,
		`id="key-detail-full-hash"`,
		`id="key-detail-copy"`,
		`id="key-detail-close"`,
		`id="key-detail-empty"`,
		`id="key-detail-body"`,
		`id="kd-trend-chart"`,
		`id="kd-models-table"`,
		`id="kd-issues-list"`,
		`id="kd-requests-table"`,
		`data-i18n="table.label_key"`,
		`data-i18n="table.cost_state"`,
		`data-i18n="table.latest_seen"`,
		`data-i18n-aria="panel.api_keys"`,
		`data-i18n-aria="key_detail.trend_mode"`,
		`data-key-usage-mode="cost"`,
		`data-key-usage-mode="tokens"`,
		`data-key-usage-mode="requests"`,
	} {
		requireContains(t, index, want, "index.html")
	}

	// Critical JS workflow contracts: selection, scoped filters, copy, staleness.
	for _, want := range []string{
		"selectedKeyHash",
		"loadKeyDetail",
		"bindKeyDetailControls",
		"copySelectedKeyHash",
		"keyDetailGeneration",
		"AbortController",
		"key_hash=",
		"encodeURIComponent(hash)",
		"models?range=",
		"timeseries?range=",
		"activity?range=",
		"issues?range=",
		"requests?range=",
		"fmtCostDisplay",
		"costStateOf",
		"partial_unknown",
		"unavailable_value",
		"clearKeySelection",
		"data-key-hash",
		"is-selected",
		"aria-pressed",
		"markKeyDetailUnavailable",
		"fmtKeyTrendTotal",
		"issueSourceStatusLabel",
		"focusKeyRow",
	} {
		requireContains(t, app, want, "app.js")
	}

	// Styling hooks for selection and inline detail.
	for _, want := range []string{
		".key-detail",
		"tr.key-row.is-selected",
		".key-chip",
		".key-detail-chart",
	} {
		requireContains(t, css, want, "styles.css")
	}

	// Every newly introduced i18n key used by markup/JS must exist in en and zh.
	requiredKeys := []string{
		"table.label_key",
		"table.failure_rate_short",
		"table.cost_state",
		"table.latest_seen",
		"table.ttfb",
		"table.request_id",
		"metric.latency_ttfb",
		"metric.latest_seen",
		"metric.cache_creation",
		"action.copy_full_hash",
		"action.close_key_detail",
		"key_detail.selected",
		"key_detail.issue_source_status.not_applicable",
		"key_detail.issue_source_status.unavailable",
		"key_detail.issue_source_status.complete",
		"key_detail.stale_title",
		"key_detail.unknown_key",
		"key_detail.open_aria",
		"key_detail.empty_title",
		"key_detail.empty_detail",
		"key_detail.usage_confidence",
		"key_detail.partial_reasons",
		"key_detail.trend",
		"key_detail.trend_mode",
		"key_detail.models",
		"key_detail.issues",
		"key_detail.requests",
		"key_detail.no_range_data",
		"key_detail.section_unavailable",
		"key_detail.activity_unavailable",
		"key_detail.latency_detail",
		"key_detail.model_count",
		"key_detail.confidence_empty",
		"key_detail.issue_source",
		"key_detail.issues_summary",
		"key_detail.issues_empty_detail",
		"key_detail.requests_summary",
		"key_detail.requests_empty_detail",
		"key_detail.trend_summary",
		"key_detail.copy_ok",
		"key_detail.copy_failed",
		"key_detail.copy_unavailable",
		"cost.state.complete",
		"cost.state.partial",
		"cost.state.unavailable",
		"cost.unavailable_value",
		"cost.partial_unknown",
		"cost.no_partial_reasons",
		"cost.reason.unpriced_model",
		"cost.reason.missing_usage",
		"cost.reason.request_only",
		"cost.reason.unsupported",
		"cost.reason.usage_conflict",
		"cost.reason.image_count_missing",
		"cost.reason.image_size_defaulted",
		"cost.reason.cost_query_failed",
		"confidence.observed",
		"confidence.side_channel",
		"confidence.request_only",
		"confidence.missing_usage",
		"confidence.unsupported",
		"confidence.conflict",
	}

	// Extract dictionary bodies roughly by language block order.
	enStart := strings.Index(i18n, "en: {")
	zhStart := strings.Index(i18n, "zh: {")
	if enStart < 0 || zhStart < 0 || zhStart <= enStart {
		t.Fatalf("could not locate en/zh dictionaries")
	}
	enBody := i18n[enStart:zhStart]
	zhBody := i18n[zhStart:]
	for _, key := range requiredKeys {
		needle := "'" + key + "':"
		if !strings.Contains(enBody, needle) {
			t.Fatalf("i18n en missing %s", key)
		}
		if !strings.Contains(zhBody, needle) {
			t.Fatalf("i18n zh missing %s", key)
		}
	}
}
