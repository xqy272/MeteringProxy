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
		"availability==='empty'",
		"availability==='unavailable'",
		"rerenderKeysForLocale",
		"resetKeyDetailCaches",
		"lastKeyModelsRows",
		"lastKeyIssuesData",
		"lastKeyRequestsRows",
		"lastKeyActivity",
	} {
		requireContains(t, app, want, "app.js")
	}

	// Locale switch must re-render Key views from cache before the async refresh.
	if !strings.Contains(app, "rerenderKeysForLocale();") {
		t.Fatal("setLang must call rerenderKeysForLocale for immediate Key re-render")
	}
	setLangIdx := strings.Index(app, "function setLang(lang)")
	refreshIdx := strings.Index(app[setLangIdx:], "refresh();")
	rerenderIdx := strings.Index(app[setLangIdx:], "rerenderKeysForLocale();")
	if setLangIdx < 0 || refreshIdx < 0 || rerenderIdx < 0 || rerenderIdx > refreshIdx {
		t.Fatal("setLang must call rerenderKeysForLocale before refresh")
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


func TestKeyDetailLocaleSwitchImmediateRerender(t *testing.T) {
	app := readStatic(t, "app.js")
	i18n := readStatic(t, "i18n.js")

	// Dynamic Key list/detail helpers must go through t(), not hard-coded Chinese literals.
	// Spot-check the primary Key render path symbols and their translation keys.
	for _, want := range []string{
		"function renderKeysTable()",
		"function renderKeyDetailHeader(",
		"function renderKeySummary(",
		"function renderKeyModels(",
		"function renderKeyIssues(",
		"function renderKeyRequests(",
		"function renderKeyTrend(",
		"function rerenderKeysForLocale()",
		"function tp(",
		"tp('summary.keys'",
		"tp('key_detail.model_count'",
		"tp('key_detail.issues_summary'",
		"tp('key_detail.requests_summary'",
		"tp('key_detail.trend_summary'",
		"tp('summary.keys'",
		"t('key_detail.selected')",
		"t('key_detail.open_aria'",
		"t('cost.state.'",
		"t('cost.reason.'",
		"t('metric.token_mix'",
		"t('metric.unpriced_models'",
		"tp('key_detail.model_count'",
		"tp('key_detail.issues_summary'",
		"tp('key_detail.requests_summary'",
		"tp('key_detail.trend_summary'",
		"t('usage.mode.cost')",
		"t('usage.mode.tokens')",
		"t('usage.mode.requests')",
		"function tp(",
		"locale()",
		"fmtTime(",
		"fmtShort(",
	} {
		requireContains(t, app, want, "app.js key locale path")
	}

	// Known Chinese UI literals that previously stuck after language switch must not
	// appear as hard-coded strings in app.js Key render helpers.
	chineseLiterals := []string{
		"个 Key",
		"详情已打开",
		"打开 ",
		"的 Key 详情",
		"部分",
		"完整",
		"未定价模型",
		"缺少用量",
		"个模型",
		"花费",
		"输入/",
		"输出/",
		"缓存/",
		"推理",
	}
	// Restrict the scan to Key-related function bodies for a tighter signal.
	start := strings.Index(app, "function renderKeysTable()")
	end := strings.Index(app, "async function loadImages()")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("could not isolate Key render region")
	}
	region := app[start:end]
	for _, lit := range chineseLiterals {
		if strings.Contains(region, lit) {
			t.Fatalf("Key render region hard-codes Chinese UI literal %q; must use t()", lit)
		}
	}

	// English dictionary must contain the dynamic Key strings that smoke saw stuck in Chinese.
	enStart := strings.Index(i18n, "en: {")
	zhStart := strings.Index(i18n, "zh: {")
	if enStart < 0 || zhStart < 0 || zhStart <= enStart {
		t.Fatal("could not locate en/zh dictionaries")
	}
	enBody := i18n[enStart:zhStart]
	zhBody := i18n[zhStart:]
	required := map[string]string{
		"summary.keys":               "keys",
		"summary.keys.one":           "key",
		"key_detail.selected":        "detail open",
		"key_detail.open_aria":       "Open",
		"cost.state.complete":        "Complete",
		"cost.state.partial":         "Partial",
		"cost.reason.unpriced_model": "Unpriced",
		"cost.reason.missing_usage":  "Missing usage",
		"metric.token_mix":           "in",
		"key_detail.model_count":     "models",
		"key_detail.model_count.one": "model",
		"usage.mode.cost":            "Cost",
	}
	for key, enSnippet := range required {
		needle := "'" + key + "':"
		if !strings.Contains(enBody, needle) {
			t.Fatalf("i18n en missing %s", key)
		}
		// Chinese reuses the base form for count=1; singular .one keys are English-only.
		if !strings.HasSuffix(key, ".one") && !strings.Contains(zhBody, needle) {
			t.Fatalf("i18n zh missing %s", key)
		}
		// Ensure English value is present and not still Chinese for sampled keys.
		lineIdx := strings.Index(enBody, needle)
		lineEnd := strings.Index(enBody[lineIdx:], "\n")
		if lineEnd < 0 {
			t.Fatalf("could not read en line for %s", key)
		}
		line := enBody[lineIdx : lineIdx+lineEnd]
		if !strings.Contains(line, enSnippet) {
			t.Fatalf("i18n en %s should contain %q, got %s", key, enSnippet, line)
		}
		if strings.Contains(line, "个") || strings.Contains(line, "详情") || strings.Contains(line, "部分") {
			t.Fatalf("i18n en %s still looks Chinese: %s", key, line)
		}
	}
}


func TestKeyDetailCountSummaryPluralization(t *testing.T) {
	app := readStatic(t, "app.js")
	i18n := readStatic(t, "i18n.js")

	requireContains(t, app, "function tp(", "app.js plural helper")
	// Key list/detail count summaries must choose singular via tp(..., count, ...).
	for _, want := range []string{
		"tp('summary.keys'",
		"tp('summary.models'",
		"tp('key_detail.model_count'",
		"tp('key_detail.issues_summary'",
		"tp('key_detail.requests_summary'",
		"tp('key_detail.trend_summary'",
		"baseKey + '.one'",
		"n === 1",
	} {
		requireContains(t, app, want, "app.js plural key path")
	}

	enStart := strings.Index(i18n, "en: {")
	zhStart := strings.Index(i18n, "zh: {")
	if enStart < 0 || zhStart < 0 || zhStart <= enStart {
		t.Fatal("could not locate en/zh dictionaries")
	}
	enBody := i18n[enStart:zhStart]
	zhBody := i18n[zhStart:]

	// English must provide both plural (base) and singular (.one) forms.
	forms := map[string][2]string{
		"summary.keys": {
			"'{count} keys'",
			"'{count} key'",
		},
		"summary.models": {
			"'{count} models · {unknown} unpriced'",
			"'{count} model · {unknown} unpriced'",
		},
		"key_detail.model_count": {
			"'{count} models'",
			"'{count} model'",
		},
		"key_detail.issues_summary": {
			"'{count} request issues'",
			"'{count} request issue'",
		},
		"key_detail.requests_summary": {
			"'{count} recent requests'",
			"'{count} recent request'",
		},
		"key_detail.trend_summary": {
			"'{count} buckets · {bucket} · total {value} · partial {partial}'",
			"'{count} bucket · {bucket} · total {value} · partial {partial}'",
		},
	}
	for key, pair := range forms {
		pluralKey := "'" + key + "':"
		oneKey := "'" + key + ".one':"
		if !strings.Contains(enBody, pluralKey) {
			t.Fatalf("en missing plural key %s", key)
		}
		if !strings.Contains(enBody, oneKey) {
			t.Fatalf("en missing singular key %s.one", key)
		}
		if !strings.Contains(enBody, pair[0]) {
			t.Fatalf("en plural value missing for %s: want %s", key, pair[0])
		}
		if !strings.Contains(enBody, pair[1]) {
			t.Fatalf("en singular value missing for %s.one: want %s", key, pair[1])
		}
		// Chinese keeps a single form that works for count=1; .one is optional.
		if !strings.Contains(zhBody, pluralKey) {
			t.Fatalf("zh missing base key %s", key)
		}
	}

	// Guard against the reported broken English steady-state strings.
	for _, bad := range []string{
		"1 models",
		"1 buckets",
		"1 recent requests",
		"1 request issues",
		"1 keys",
	} {
		if strings.Contains(enBody, bad) {
			t.Fatalf("en dictionary contains broken singular form %q", bad)
		}
	}
}
