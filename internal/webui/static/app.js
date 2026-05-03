const apiBaseMeta = document.querySelector('meta[name="api-base"]');
const BASE = apiBaseMeta && apiBaseMeta.content ? apiBaseMeta.content : '/metering/api/';
const LANG_KEY = 'metering-proxy-language';

const I18N = window.METERING_I18N || {};

let metadata = null;
let currentModels = [];
let autoRefreshTimer = null;
let isRefreshing = false;
let lastRefreshAt = 0;
let requestsExpanded = false;
let currentLang = detectLanguage();
let lastTimeseriesRows = [];
let lastTimeseriesBucket = '';

const fallbackRanges = [
  { key: '24h', label: 'Last 24 Hours', bucket: '10m' },
  { key: 'today', label: 'Today', bucket: '10m' },
  { key: '7d', label: 'Last 7 Days', bucket: '1h' },
  { key: '30d', label: 'Last 30 Days', bucket: '1d' }
];

const $ = id => document.getElementById(id);

function detectLanguage() {
  try {
    const saved = localStorage.getItem(LANG_KEY);
    if (saved === 'en' || saved === 'zh') return saved;
  } catch (_) {}
  return (navigator.language || '').toLowerCase().startsWith('zh') ? 'zh' : 'en';
}

function locale() {
  return currentLang === 'zh' ? 'zh-CN' : 'en-US';
}

function t(key, vars) {
  const fallback = I18N.en || {};
  const dict = I18N[currentLang] || fallback;
  let text = dict[key] || fallback[key] || key;
  if (!vars) return text;
  return text.replace(/\{([a-zA-Z0-9_]+)\}/g, (_, name) => vars[name] == null ? '' : String(vars[name]));
}

function esc(s) {
  if (s == null) return '';
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function formatNum(n) {
  n = Number(n || 0);
  if (Math.abs(n) >= 1e9) return (n / 1e9).toFixed(1) + 'B';
  if (Math.abs(n) >= 1e6) return (n / 1e6).toFixed(1) + 'M';
  if (Math.abs(n) >= 1e3) return (n / 1e3).toFixed(1) + 'K';
  return String(n);
}

function formatFull(n) {
  return Number(n || 0).toLocaleString(locale());
}

function formatCost(c) {
  c = Number(c || 0);
  if (c === 0) return '$0.00';
  if (c > 0 && c < 0.01) return '<$0.01';
  return '$' + c.toFixed(2);
}

function formatPercent(value, total) {
  value = Number(value || 0);
  total = Number(total || 0);
  if (total <= 0) return '0.0%';
  return (value / total * 100).toFixed(1) + '%';
}

function formatLat(ms) {
  ms = Number(ms || 0);
  if (ms <= 0) return '-';
  if (ms < 1000) return ms + 'ms';
  return (ms / 1000).toFixed(1) + 's';
}

function formatBytes(bytes) {
  bytes = Number(bytes || 0);
  if (bytes >= 1024 * 1024) return (bytes / 1024 / 1024).toFixed(1) + ' MiB';
  if (bytes >= 1024) return (bytes / 1024).toFixed(1) + ' KiB';
  return bytes + ' B';
}

function formatTime(value) {
  if (!value) return '-';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleString(locale());
}

function formatShortTime(value) {
  if (!value) return '-';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleString(locale(), { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

function shortHash(h) {
  if (!h) return '-';
  return String(h).slice(0, 12) + '...';
}

function getRange() {
  return $('range-select').value || '24h';
}

function rangeLabel(r) {
  return t('range.' + r.key) || r.label || r.key;
}

function bucketForRange(range) {
  const ranges = metadata && metadata.ranges ? metadata.ranges : fallbackRanges;
  const item = ranges.find(r => r.key === range);
  return item && item.bucket ? item.bucket : '10m';
}

function setStatus(kind, title, detail) {
  const pill = $('status-pill');
  pill.className = 'status-pill ' + (kind === 'error' ? 'err' : kind === 'partial' ? 'warn' : 'ok');
  pill.textContent = kind === 'error' ? t('status.error') : kind === 'partial' ? t('status.partial') : t('status.live');
  $('status-title').textContent = title;
  $('status-detail').textContent = detail || '';
}

function setLastRefresh(date) {
  lastRefreshAt = date ? date.getTime() : 0;
  $('last-refresh').textContent = date ? t('status.last_refresh', { time: date.toLocaleString(locale()) }) : t('status.last_refresh_never');
}

function applyStaticI18N() {
  document.documentElement.lang = currentLang === 'zh' ? 'zh-CN' : 'en';
  document.querySelectorAll('[data-i18n]').forEach(el => {
    el.textContent = t(el.dataset.i18n);
  });
  const select = $('language-select');
  if (select) select.value = currentLang;
}

function setLanguage(lang) {
  if (lang !== 'en' && lang !== 'zh') return;
  currentLang = lang;
  try { localStorage.setItem(LANG_KEY, lang); } catch (_) {}
  applyStaticI18N();
  applyMetadata();
  setLastRefresh(lastRefreshAt ? new Date(lastRefreshAt) : null);
  refresh();
}

async function fetchJSON(path) {
  const url = BASE + path;
  const res = await fetch(url, {
    cache: 'no-store',
    credentials: 'same-origin',
    headers: { 'Accept': 'application/json' }
  });
  if (!res.ok) {
    let detail = '';
    try { detail = await res.text(); } catch (_) {}
    const err = new Error(url + ' returned HTTP ' + res.status);
    err.status = res.status;
    err.detail = detail.slice(0, 180);
    throw err;
  }
  return res.json();
}

function emptyRow(colspan, title, detail) {
  return `<tr><td colspan="${colspan}" class="empty"><strong>${esc(title)}</strong>${esc(detail || '')}</td></tr>`;
}

function errorRow(colspan, message) {
  return `<tr><td colspan="${colspan}" class="empty error-line">${esc(message)}</td></tr>`;
}

function emptyChart(title, detail) {
  return `<div class="empty"><strong>${esc(title)}</strong>${esc(detail || '')}</div>`;
}

function applyMetadata() {
  const rangeSelect = $('range-select');
  const currentRange = rangeSelect.value;
  const ranges = metadata && metadata.ranges && metadata.ranges.length ? metadata.ranges : fallbackRanges;
  rangeSelect.innerHTML = ranges.map(r => `<option value="${esc(r.key)}">${esc(rangeLabel(r))}</option>`).join('');
  if ([...rangeSelect.options].some(o => o.value === currentRange)) {
    rangeSelect.value = currentRange;
  } else {
    rangeSelect.value = ranges[0].key;
  }

  const endpointSelect = $('filter-endpoint');
  const currentEndpoint = endpointSelect.value;
  const endpointOptions = ((metadata && metadata.endpoints) || [])
    .filter(e => e.capture_mode !== 'passthrough')
    .map(e => `<option value="${esc(e.path)}">${esc(e.display_name || e.path)}</option>`)
    .join('');
  endpointSelect.innerHTML = `<option value="">${esc(t('filter.all_endpoints'))}</option>` + endpointOptions;
  if ([...endpointSelect.options].some(o => o.value === currentEndpoint)) {
    endpointSelect.value = currentEndpoint;
  }
}

function applyModelFilterOptions() {
  const select = $('filter-model');
  const current = select.value;
  const options = currentModels
    .map(r => r.model || 'unknown')
    .filter((value, index, arr) => arr.indexOf(value) === index)
    .map(model => `<option value="${esc(model)}">${esc(model)}</option>`)
    .join('');
  select.innerHTML = `<option value="">${esc(t('filter.all_models'))}</option>` + options;
  if ([...select.options].some(o => o.value === current)) {
    select.value = current;
  }
}

async function loadMetadata() {
  metadata = await fetchJSON('metadata');
  applyMetadata();
}

async function loadSummary() {
  const data = await fetchJSON('summary?range=' + encodeURIComponent(getRange()));
  const totalRequests = Number(data.total_requests || 0);
  const failedRequests = Number(data.failed_requests || 0);
  const totalTokens = Number(data.total_tokens || 0);
  const inputTokens = Number(data.total_input_tokens || 0);
  const cachedTokens = Number(data.total_cached_tokens || 0);
  const outputTokens = Number(data.total_output_tokens || 0);
  const reasoningTokens = Number(data.total_reasoning_tokens || 0);

  $('total-requests').textContent = formatNum(totalRequests);
  $('requests-sub').textContent = t('metric.failed_count', { count: formatFull(failedRequests) });
  $('failure-rate').textContent = formatPercent(failedRequests, totalRequests);
  $('failure-sub').textContent = t('metric.failed_of_total', { failed: formatFull(failedRequests), total: formatFull(totalRequests) });
  $('total-tokens').textContent = formatNum(totalTokens);
  $('tokens-sub').textContent = t('metric.token_mix', {
    input: formatNum(inputTokens),
    output: formatNum(outputTokens),
    cached: formatNum(cachedTokens),
    reasoning: formatNum(reasoningTokens)
  });
  $('total-cost').textContent = formatCost(data.total_cost);
  $('cost-sub').textContent = t('metric.query_time_pricing');
}

async function loadActivity() {
  const data = await fetchJSON('activity?range=' + encodeURIComponent(getRange()));
  const sample = Number(data.sample_size || 0);
  const success = Number(data.success_count || 0);
  const failed = Number(data.failed_count || 0);
  const captureCaptured = Number(data.capture_captured || 0);
  const captureFailed = Number(data.capture_failed || 0);
  const captureSkipped = Number(data.capture_skipped || 0);
  const captureTotal = captureCaptured + captureFailed + captureSkipped;
  const captureIssues = captureFailed + captureSkipped;

  $('p95-latency').textContent = formatLat(data.p95_latency_ms);
  $('latency-sub').textContent = t('metric.avg_latency_ttfb', { avg: formatLat(data.avg_latency_ms), ttfb: formatLat(data.p95_ttfb_ms) });
  $('capture-quality').textContent = captureTotal ? formatPercent(captureCaptured, captureTotal) : '-';
  $('capture-sub').textContent = t('metric.capture_issue_count', { count: formatFull(captureIssues) });

  $('request-health-summary').textContent = t('metric.sampled_requests', { count: formatFull(sample) });
  $('activity-success-rate').textContent = formatPercent(success, sample);
  $('activity-success-detail').textContent = t('metric.success_failed', { success: formatFull(success), failed: formatFull(failed) });
  $('p95-ttfb').textContent = formatLat(data.p95_ttfb_ms);
  $('ttfb-detail').textContent = t('metric.avg_latency_ttfb', { avg: formatLat(data.avg_ttfb_ms), ttfb: formatLat(data.p95_ttfb_ms) });
  $('capture-issues').textContent = formatFull(captureIssues);
  $('capture-issues-detail').textContent = t('metric.capture_failed_skipped', { failed: formatFull(captureFailed), skipped: formatFull(captureSkipped) });

  if (Number(data.latest_error_status || 0) > 0) {
    $('latest-error-status').innerHTML = `<span class="badge err">${esc(data.latest_error_status)}</span>`;
    const model = data.latest_error_model ? `, ${data.latest_error_model}` : '';
    const detail = `${formatShortTime(data.latest_error_at)} ${data.latest_error_endpoint || ''}${model}`;
    $('latest-error-detail').textContent = detail.trim();
  } else {
    $('latest-error-status').innerHTML = `<span class="badge ok">${esc(t('badge.none'))}</span>`;
    $('latest-error-detail').textContent = t('metric.no_request_errors');
  }
}

async function loadModels() {
  const data = await fetchJSON('models?range=' + encodeURIComponent(getRange()));
  currentModels = Array.isArray(data) ? data : [];
  applyModelFilterOptions();

  const tbody = $('models-table');
  if (!currentModels.length) {
    tbody.innerHTML = emptyRow(7, t('state.no_model_data'), t('state.model_hint'));
    $('models-summary').textContent = t('summary.zero_models');
    return;
  }

  const totalCost = currentModels.reduce((sum, r) => sum + (r.cost_known ? Number(r.cost || 0) : 0), 0);
  const totalTokens = currentModels.reduce((sum, r) => sum + Number(r.total_tokens || 0), 0);
  const unknownPricing = currentModels.filter(r => !r.cost_known).length;
  $('models-summary').textContent = t('summary.models', { count: currentModels.length, unknown: unknownPricing });

  tbody.innerHTML = currentModels.map(r => {
    const tokens = Number(r.total_tokens || 0);
    const requestCount = Number(r.request_count || 0);
    const avgTokens = requestCount ? Math.round(tokens / requestCount) : 0;
    const cost = Number(r.cost || 0);
    const costShare = r.cost_known && totalCost > 0 ? cost / totalCost * 100 : 0;
    const tokenShare = totalTokens > 0 ? tokens / totalTokens * 100 : 0;
    const share = r.cost_known ? costShare : tokenShare;
    const shareLabel = r.cost_known ? `${costShare.toFixed(1)}%` : `${tokenShare.toFixed(1)}% ${t('table.tokens').toLowerCase()}`;
    const pricing = r.cost_known ? `<span class="badge ok">${esc(t('badge.matched'))}</span>` : `<span class="badge warn">${esc(t('badge.pricing_unknown'))}</span>`;
    const costStr = r.cost_known ? formatCost(cost) : '-';
    return `<tr>
      <td><div class="model-name" title="${esc(r.model || 'unknown')}">${esc(r.model || 'unknown')}</div></td>
      <td class="numeric mono">${formatFull(requestCount)}</td>
      <td class="numeric mono">${formatNum(tokens)}</td>
      <td class="numeric mono">${formatFull(avgTokens)}</td>
      <td class="numeric"><span class="rank-bar"><span style="width:${Math.max(2, Math.min(100, share)).toFixed(1)}%"></span></span>${shareLabel}</td>
      <td class="numeric mono">${costStr}</td>
      <td>${pricing}</td>
    </tr>`;
  }).join('');
}

async function loadKeys() {
  const data = await fetchJSON('keys?range=' + encodeURIComponent(getRange()));
  const rows = Array.isArray(data) ? data : [];
  const tbody = $('keys-table');
  $('keys-summary').textContent = t('summary.keys', { count: rows.length });
  if (!rows.length) {
    tbody.innerHTML = emptyRow(5, t('state.no_key_data'), t('state.key_hint'));
    return;
  }
  tbody.innerHTML = rows.map(r => {
    const count = Number(r.request_count || 0);
    const failed = Number(r.failed_count || 0);
    return `<tr>
      <td><code>${esc(shortHash(r.key_hash))}</code></td>
      <td class="numeric mono">${formatFull(count)}</td>
      <td class="numeric mono">${formatFull(failed)}</td>
      <td class="numeric mono">${formatPercent(failed, count)}</td>
      <td class="numeric mono">${formatNum(r.total_tokens)}</td>
    </tr>`;
  }).join('');
}

async function loadRequests() {
  const params = new URLSearchParams({ limit: '100', range: getRange() });
  const status = $('filter-status').value;
  const model = $('filter-model').value;
  const endpoint = $('filter-endpoint').value;
  if (status) params.set('status', status);
  if (model) params.set('model', model);
  if (endpoint) params.set('endpoint', endpoint);

  const data = await fetchJSON('requests?' + params.toString());
  const rows = Array.isArray(data) ? data : [];
  const tbody = $('requests-table');
  if (!rows.length) {
    tbody.innerHTML = emptyRow(11, t('state.no_matching_requests'), t('state.adjust_filters'));
    return;
  }
  tbody.innerHTML = rows.map(r => {
    const statusClass = r.status < 400 ? 'ok' : r.status < 500 ? 'warn' : 'err';
    const model = r.model_returned || r.model_requested || '-';
    const capture = captureBadge(r);
    const endpoint = r.stream ? `${esc(r.endpoint)} <span class="badge info">stream</span>` : esc(r.endpoint);
    const bytesTitle = t('bytes.request_response', { request: formatBytes(r.request_bytes), response: formatBytes(r.response_bytes) });
    return `<tr title="${esc(bytesTitle)}">
      <td class="mono">${esc(formatTime(r.created_at))}</td>
      <td><span class="badge ${statusClass}">${esc(r.status)}</span></td>
      <td>${endpoint}</td>
      <td><div class="model-name" title="${esc(model)}">${esc(model)}</div></td>
      <td class="numeric mono">${formatNum(r.input_tokens)}</td>
      <td class="numeric mono">${formatNum(r.output_tokens)}</td>
      <td class="numeric mono">${formatNum(r.total_tokens)}</td>
      <td class="numeric mono">${formatLat(r.ttfb_ms)}</td>
      <td class="numeric mono">${formatLat(r.latency_ms)}</td>
      <td>${capture}</td>
      <td><code>${esc(shortHash(r.api_key_hash))}</code></td>
    </tr>`;
  }).join('');
}

function captureBadge(r) {
  const outcome = r.capture_outcome || '';
  const reason = r.capture_reason || '';
  if (outcome === 'captured') return `<span class="badge ok">${esc(t('badge.captured'))}</span>`;
  if (outcome === 'failed') return `<span class="badge err" title="${esc(reason)}">${esc(t('badge.failed'))}</span>`;
  if (outcome === 'skipped') return `<span class="badge warn" title="${esc(reason)}">${esc(t('badge.skipped'))}</span>`;
  if (reason) return `<span class="badge neutral" title="${esc(reason)}">${esc(reason)}</span>`;
  return `<span class="badge neutral">${esc(t('badge.unknown'))}</span>`;
}

async function loadTimeseries() {
  const range = getRange();
  const bucket = bucketForRange(range);
  const data = await fetchJSON('timeseries?range=' + encodeURIComponent(range) + '&bucket=' + encodeURIComponent(bucket));
  const rows = Array.isArray(data) ? data : [];
  lastTimeseriesRows = rows;
  lastTimeseriesBucket = bucket;
  renderTokensChart(rows, bucket);
  renderRequestsChart(rows, bucket);
}

function chartDimensions(container) {
  const rect = container && container.getBoundingClientRect ? container.getBoundingClientRect() : null;
  const width = Math.max(360, Math.round((rect && rect.width) || (container && container.clientWidth) || 820));
  const height = Math.max(190, Math.round((rect && rect.height) || (container && container.clientHeight) || 250));
  return { width, height, left: 54, right: 18, top: 18, bottom: 30 };
}

function chartGrid(maxValue, dims) {
  const plotH = dims.height - dims.top - dims.bottom;
  return [0, 0.5, 1].map(f => {
    const value = maxValue * f;
    const y = dims.height - dims.bottom - plotH * f;
    return `<line class="chart-grid-line" x1="${dims.left}" y1="${y.toFixed(1)}" x2="${dims.width - dims.right}" y2="${y.toFixed(1)}"></line>
      <text class="chart-axis-label" x="${dims.left - 8}" y="${(y + 4).toFixed(1)}">${esc(formatNum(value))}</text>`;
  }).join('');
}

function categoryBucket(index, count, dims) {
  const plotW = dims.width - dims.left - dims.right;
  const slot = plotW / Math.max(count, 1);
  return {
    x: dims.left + slot * index,
    width: slot,
    center: dims.left + slot * (index + 0.5)
  };
}

function coordinateBucket(index, count, dims, xFor) {
  const center = xFor(index);
  const start = index === 0 ? dims.left : (xFor(index - 1) + center) / 2;
  const end = index === count - 1 ? dims.width - dims.right : (center + xFor(index + 1)) / 2;
  return {
    x: start,
    width: Math.max(4, end - start),
    center
  };
}

function renderTokensChart(rows, bucket) {
  const chart = $('tokens-chart');
  if (!rows.length) {
    chart.innerHTML = emptyChart(t('state.no_token_data'), t('state.no_captured_usage'));
    $('tokens-chart-summary').textContent = t('summary.chart_buckets', { count: 0, bucket });
    $('tokens-chart-left').textContent = '-';
    $('tokens-chart-right').textContent = '-';
    return;
  }

  const totals = rows.map(r => {
    const reasoning = Number(r.reasoning_tokens || 0);
    const rawOutput = Number(r.output_tokens || 0);
    return {
      cached: Number(r.cached_tokens || 0),
      uncached: Math.max(0, Number(r.input_tokens || 0) - Number(r.cached_tokens || 0)),
      output: Math.max(0, rawOutput - reasoning),
      reasoning,
      total: Number(r.total_tokens || 0)
    };
  });
  const stackTotals = totals.map(r => r.cached + r.uncached + r.output + r.reasoning);
  const maxStack = Math.max(...stackTotals, 1);
  const totalTokens = totals.reduce((sum, r) => sum + r.total, 0);
  const peakTokens = Math.max(...totals.map(r => r.total), 0);
  const dims = chartDimensions(chart);
  const plotW = dims.width - dims.left - dims.right;
  const plotH = dims.height - dims.top - dims.bottom;
  const slot = plotW / rows.length;
  const barW = Math.max(2, Math.min(16, slot * 0.58));
  const yFor = value => dims.height - dims.bottom - plotH * Number(value || 0) / maxStack;

  const bars = rows.map((r, i) => {
    const bucketRect = categoryBucket(i, rows.length, dims);
    const x = bucketRect.center - barW / 2;
    let yCursor = dims.height - dims.bottom;
    const parts = [
      ['cached', totals[i].cached],
      ['uncached', totals[i].uncached],
      ['output', totals[i].output],
      ['reasoning', totals[i].reasoning]
    ];
    const rects = parts.map(([kind, value]) => {
      if (value <= 0) return '';
      const y = yFor(value + (dims.height - dims.bottom - yCursor) / plotH * maxStack);
      let h = yCursor - y;
      if (h > 0 && h < 1) h = 1;
      yCursor -= h;
      return `<rect class="token-segment ${kind}" x="${x.toFixed(1)}" y="${yCursor.toFixed(1)}" width="${barW.toFixed(1)}" height="${h.toFixed(1)}" rx="2"></rect>`;
    }).join('');
    const stackTop = yCursor;
    const stackH = Math.max(0, dims.height - dims.bottom - stackTop);
    const outline = stackH > 0 ? `<rect class="token-outline" x="${(x - 1.5).toFixed(1)}" y="${(stackTop - 1.5).toFixed(1)}" width="${(barW + 3).toFixed(1)}" height="${(stackH + 3).toFixed(1)}" rx="3"></rect>` : '';
    return `<g class="chart-bucket" tabindex="0" data-tooltip="${esc(tokenTooltip(r, totals[i]))}">
      <rect class="bucket-band" x="${bucketRect.x.toFixed(1)}" y="${dims.top}" width="${bucketRect.width.toFixed(1)}" height="${plotH}"></rect>
      <line class="bucket-ruler" x1="${bucketRect.center.toFixed(1)}" y1="${dims.top}" x2="${bucketRect.center.toFixed(1)}" y2="${dims.height - dims.bottom}"></line>
      ${outline}
      ${rects}
      <rect class="chart-hit" x="${bucketRect.x.toFixed(1)}" y="${dims.top}" width="${bucketRect.width.toFixed(1)}" height="${plotH}"></rect>
    </g>`;
  }).join('');

  chart.innerHTML = `<svg class="svg-chart" viewBox="0 0 ${dims.width} ${dims.height}" role="img" aria-label="${esc(t('panel.tokens'))}">
    ${chartGrid(maxStack, dims)}
    <line class="axis-line" x1="${dims.left}" y1="${dims.height - dims.bottom}" x2="${dims.width - dims.right}" y2="${dims.height - dims.bottom}"></line>
    ${bars}
  </svg>`;
  attachTooltips(chart);

  $('tokens-chart-summary').textContent = t('summary.tokens_chart', { count: rows.length, bucket, tokens: formatFull(totalTokens) });
  $('tokens-chart-left').textContent = formatShortTime(rows[0].timestamp);
  $('tokens-chart-right').textContent = t('summary.peak_tokens', { tokens: formatFull(peakTokens) });
}

function tokenTooltip(row, values) {
  return tooltipHtml(
    formatShortTime(row.timestamp),
    t('tooltip.total_tokens', { value: formatFull(row.total_tokens) }),
    [
      ['cached', t('tooltip.cached_input'), formatFull(values.cached)],
      ['uncached', t('tooltip.uncached_input'), formatFull(values.uncached)],
      ['output', t('tooltip.output'), formatFull(values.output)],
      ['reasoning', t('tooltip.reasoning'), formatFull(values.reasoning)]
    ]
  );
}

function renderRequestsChart(rows, bucket) {
  const chart = $('requests-chart');
  if (!rows.length) {
    chart.innerHTML = emptyChart(t('state.no_request_data'), t('state.no_requests_range'));
    $('requests-chart-summary').textContent = t('summary.chart_buckets', { count: 0, bucket });
    $('requests-chart-left').textContent = '-';
    $('requests-chart-right').textContent = '-';
    return;
  }

  const dims = chartDimensions(chart);
  const plotW = dims.width - dims.left - dims.right;
  const plotH = dims.height - dims.top - dims.bottom;
  const maxCount = Math.max(...rows.map(r => Number(r.count || 0)), 1);
  const maxFailed = Math.max(...rows.map(r => Number(r.failed_count || 0)), 0);
  const xFor = i => rows.length === 1 ? dims.left + plotW / 2 : dims.left + plotW * i / (rows.length - 1);
  const yFor = value => dims.height - dims.bottom - plotH * Number(value || 0) / maxCount;
  const linePath = field => rows.map((r, i) => `${i === 0 ? 'M' : 'L'}${xFor(i).toFixed(1)} ${yFor(r[field]).toFixed(1)}`).join(' ');
  const requestPath = linePath('count');
  const failedPath = linePath('failed_count');
  const areaPath = `${requestPath} L${xFor(rows.length - 1).toFixed(1)} ${dims.height - dims.bottom} L${xFor(0).toFixed(1)} ${dims.height - dims.bottom} Z`;
  const peakLatency = Math.max(...rows.map(r => Number(r.avg_latency_ms || 0)), 0);

  const buckets = rows.map((r, i) => {
    const bucketRect = coordinateBucket(i, rows.length, dims, xFor);
    const x = xFor(i);
    const y = yFor(r.count);
    const failed = Number(r.failed_count || 0);
    const failedY = yFor(failed);
    return `<g class="chart-bucket" tabindex="0" data-tooltip="${esc(requestTooltip(r))}">
      <rect class="bucket-band" x="${bucketRect.x.toFixed(1)}" y="${dims.top}" width="${bucketRect.width.toFixed(1)}" height="${plotH}"></rect>
      <line class="bucket-ruler" x1="${x.toFixed(1)}" y1="${dims.top}" x2="${x.toFixed(1)}" y2="${dims.height - dims.bottom}"></line>
      <circle class="request-point-ring" cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" r="7"></circle>
      <circle class="request-point" cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" r="3.4"></circle>
      ${failed > 0 ? `<circle class="failed-point" cx="${x.toFixed(1)}" cy="${failedY.toFixed(1)}" r="3"></circle>` : ''}
      <rect class="chart-hit" x="${bucketRect.x.toFixed(1)}" y="${dims.top}" width="${bucketRect.width.toFixed(1)}" height="${plotH}"></rect>
    </g>`;
  }).join('');

  chart.innerHTML = `<svg class="svg-chart" viewBox="0 0 ${dims.width} ${dims.height}" role="img" aria-label="${esc(t('panel.requests'))}">
    ${chartGrid(maxCount, dims)}
    <line class="axis-line" x1="${dims.left}" y1="${dims.height - dims.bottom}" x2="${dims.width - dims.right}" y2="${dims.height - dims.bottom}"></line>
    <path class="request-area" d="${areaPath}"></path>
    <path class="request-line" d="${requestPath}"></path>
    ${maxFailed > 0 ? `<path class="failed-line" d="${failedPath}"></path>` : ''}
    ${buckets}
  </svg>`;
  attachTooltips(chart);

  $('requests-chart-summary').textContent = t('summary.requests_chart', { count: rows.length, bucket, peak: formatFull(maxCount) });
  $('requests-chart-left').textContent = formatShortTime(rows[0].timestamp);
  $('requests-chart-right').textContent = t('summary.peak_latency', { latency: formatLat(peakLatency) });
}

function requestTooltip(row) {
  const count = Number(row.count || 0);
  const failed = Number(row.failed_count || 0);
  return tooltipHtml(
    formatShortTime(row.timestamp),
    '',
    [
      ['requests', t('tooltip.requests'), formatFull(count)],
      ['failed', t('tooltip.failed'), formatFull(failed)],
      ['failed', t('tooltip.failure_rate'), formatPercent(failed, count)],
      ['requests', t('tooltip.avg_ttfb'), formatLat(row.avg_ttfb_ms)],
      ['requests', t('tooltip.avg_latency'), formatLat(row.avg_latency_ms)],
      ['tokens', t('tooltip.tokens'), formatFull(row.total_tokens)]
    ]
  );
}

function tooltipHtml(title, meta, rows) {
  return `<div class="tooltip-title"><span>${esc(title)}</span>${meta ? `<strong>${esc(meta)}</strong>` : ''}</div>` +
    rows.map(([kind, label, value]) => `<div class="tooltip-row"><span><i class="tooltip-swatch ${kind}"></i>${esc(label)}</span><strong>${esc(value)}</strong></div>`).join('');
}

function attachTooltips(container) {
  const svg = container.querySelector('.svg-chart');
  container.querySelectorAll('.chart-bucket').forEach(el => {
    el.addEventListener('mouseenter', event => activateBucket(container, svg, el, event));
    el.addEventListener('mousemove', event => moveTooltip(event, el));
    el.addEventListener('mouseleave', () => deactivateBucket(container, svg, el));
    el.addEventListener('focus', event => activateBucket(container, svg, el, event));
    el.addEventListener('blur', () => deactivateBucket(container, svg, el));
  });
}

function activateBucket(container, svg, el, event) {
  container.querySelectorAll('.chart-bucket.is-active').forEach(active => {
    if (active !== el) active.classList.remove('is-active');
  });
  if (svg) svg.classList.add('chart-active');
  el.classList.add('is-active');
  showTooltip(event, el.dataset.tooltip, el);
}

function deactivateBucket(container, svg, el) {
  el.classList.remove('is-active');
  if (svg && !container.querySelector('.chart-bucket.is-active')) svg.classList.remove('chart-active');
  hideTooltip();
}

function showTooltip(event, html, anchor) {
  const tooltip = $('chart-tooltip');
  tooltip.innerHTML = html || '';
  tooltip.classList.remove('hidden');
  moveTooltip(event, anchor);
}

function moveTooltip(event, anchor) {
  const tooltip = $('chart-tooltip');
  if (!tooltip || tooltip.classList.contains('hidden')) return;
  const pad = 12;
  const rect = tooltip.getBoundingClientRect();
  const target = anchor || event.currentTarget;
  const targetRect = target && target.getBoundingClientRect ? target.getBoundingClientRect() : null;
  const clientX = targetRect ? targetRect.left + targetRect.width / 2 : Number.isFinite(event.clientX) ? event.clientX : window.innerWidth / 2;
  const clientY = targetRect ? targetRect.top + 8 : Number.isFinite(event.clientY) ? event.clientY : window.innerHeight / 2;
  let x = clientX - rect.width / 2;
  let y = clientY - rect.height - 10;
  if (y < pad && targetRect) y = targetRect.bottom + 10;
  if (x + rect.width + pad > window.innerWidth) x = window.innerWidth - rect.width - pad;
  if (y + rect.height + pad > window.innerHeight) y = window.innerHeight - rect.height - pad;
  tooltip.style.left = Math.max(pad, x) + 'px';
  tooltip.style.top = Math.max(pad, y) + 'px';
}

function hideTooltip() {
  const tooltip = $('chart-tooltip');
  if (tooltip) tooltip.classList.add('hidden');
}

async function loadHealth() {
  const data = await fetchJSON('health');
  $('queue-depth').textContent = formatFull(data.queue_depth);
  $('dropped-events').textContent = formatFull(data.dropped_events);
  $('parse-errors').textContent = formatFull(data.parse_errors);
  $('db-errors').textContent = formatFull(data.db_write_errors);

  const unhealthy = Number(data.dropped_events || 0) + Number(data.parse_errors || 0) + Number(data.db_write_errors || 0);
  if (data.capture_disabled || !data.metering_enabled) {
    $('health-summary').innerHTML = `<span class="badge warn">${esc(t('badge.capture_off'))}</span>`;
  } else if (unhealthy > 0 || Number(data.queue_depth || 0) > 0) {
    $('health-summary').innerHTML = `<span class="badge warn">${esc(t('badge.attention'))}</span>`;
  } else {
    $('health-summary').innerHTML = `<span class="badge ok">${esc(t('badge.healthy'))}</span>`;
  }
}

async function loadErrors() {
  const data = await fetchJSON('errors?range=' + encodeURIComponent(getRange()) + '&nonzero=true');
  const rows = Array.isArray(data.timeline) ? data.timeline : [];
  const source = data.source || 'unknown';
  const bucketCount = Number(data.bucket_count || rows.length || 0);
  const nonzeroBucketCount = Number(data.nonzero_bucket_count || rows.length || 0);
  $('errors-summary').textContent = t('summary.error_buckets', { source, buckets: bucketCount, nonzero: nonzeroBucketCount });

  if (!rows.length) {
    $('errors-table-wrap').classList.add('hidden');
    $('errors-state').classList.remove('hidden');
    $('errors-state').innerHTML = `<div class="state-box"><strong>${esc(t('state.no_errors'))}</strong>${esc(t('state.all_error_buckets_zero'))}</div>`;
    return;
  }

  $('errors-state').classList.add('hidden');
  $('errors-table-wrap').classList.remove('hidden');
  $('errors-table').innerHTML = rows.map(r => `<tr>
    <td class="mono">${esc(formatTime(r.timestamp))}</td>
    <td class="numeric mono">${formatFull(r.count)}</td>
    <td class="numeric mono">${formatFull(r.parse_errors)}</td>
    <td class="numeric mono">${formatFull(r.db_errors)}</td>
    <td class="numeric mono">${formatFull(r.dropped_events)}</td>
  </tr>`).join('');
}

async function refresh() {
  if (isRefreshing) return;
  isRefreshing = true;
  const btn = $('refresh-btn');
  btn.textContent = t('action.loading');
  btn.disabled = true;

  try {
    const tasks = [];
    if (!metadata) {
      tasks.push(['metadata', loadMetadata]);
    }
    tasks.push(
      ['summary', loadSummary],
      ['activity', loadActivity],
      ['models', loadModels],
      ['keys', loadKeys],
      ['timeseries', loadTimeseries],
      ['health', loadHealth],
      ['errors', loadErrors]
    );
    if (requestsExpanded) {
      tasks.push(['requests', loadRequests]);
    }

    const settled = await Promise.allSettled(tasks.map(([name, fn]) => fn().then(() => name)));
    const failures = settled
      .map((result, index) => ({ result, name: tasks[index][0] }))
      .filter(item => item.result.status === 'rejected')
      .map(item => ({ name: item.name, error: item.result.reason }));

    if (failures.length === 0) {
      setStatus('live', t('status.dashboard_live'), t('status.all_panels'));
    } else if (failures.length === tasks.length) {
      setStatus('error', t('status.refresh_failed'), failures.map(f => `${f.name}: ${f.error.message}`).join(' | '));
      markFailedPanels(failures);
    } else {
      setStatus('partial', t('status.partial_refresh'), failures.map(f => `${f.name}: ${f.error.message}`).join(' | '));
      markFailedPanels(failures);
    }

    setLastRefresh(new Date());
  } finally {
    btn.textContent = t('action.refresh');
    btn.disabled = false;
    isRefreshing = false;
  }
}

function markFailedPanels(failures) {
  const failed = new Map(failures.map(f => [f.name, f.error]));
  if (failed.has('models')) $('models-table').innerHTML = errorRow(7, failed.get('models').message);
  if (failed.has('keys')) $('keys-table').innerHTML = errorRow(5, failed.get('keys').message);
  if (failed.has('requests')) $('requests-table').innerHTML = errorRow(11, failed.get('requests').message);
  if (failed.has('errors')) {
    $('errors-table-wrap').classList.add('hidden');
    $('errors-state').classList.remove('hidden');
    $('errors-state').innerHTML = `<div class="state-box error-line">${esc(failed.get('errors').message)}</div>`;
  }
  if (failed.has('timeseries')) {
    $('tokens-chart').innerHTML = `<div class="empty error-line">${esc(failed.get('timeseries').message)}</div>`;
    $('requests-chart').innerHTML = `<div class="empty error-line">${esc(failed.get('timeseries').message)}</div>`;
  }
}

function configureAutoRefresh() {
  const enabled = $('auto-refresh').checked;
  if (autoRefreshTimer) {
    clearInterval(autoRefreshTimer);
    autoRefreshTimer = null;
  }
  if (enabled) {
    autoRefreshTimer = setInterval(refresh, 30000);
  }
}

async function reloadRequestsFromFilter() {
  if (!requestsExpanded) return;
  try {
    await loadRequests();
  } catch (err) {
    $('requests-table').innerHTML = errorRow(11, err.message);
    setStatus('partial', t('status.requests_failed'), err.message);
  }
}

async function toggleRequests() {
  requestsExpanded = !requestsExpanded;
  $('request-details').classList.toggle('hidden', !requestsExpanded);
  $('toggle-requests').textContent = requestsExpanded ? t('action.hide_requests') : t('action.show_requests');
  if (requestsExpanded) {
    $('requests-table').innerHTML = emptyRow(11, t('state.loading_requests'), t('state.fetching_requests'));
    await reloadRequestsFromFilter();
  }
}

function debounce(fn, wait) {
  let timer;
  return function debounced() {
    clearTimeout(timer);
    timer = setTimeout(fn, wait);
  };
}

function refreshFromFocus() {
  if (Date.now() - lastRefreshAt < 10000) return;
  refresh();
}

function rerenderTimeseriesCharts() {
  if (!lastTimeseriesRows.length) return;
  renderTokensChart(lastTimeseriesRows, lastTimeseriesBucket);
  renderRequestsChart(lastTimeseriesRows, lastTimeseriesBucket);
}

document.addEventListener('DOMContentLoaded', async () => {
  applyStaticI18N();
  applyMetadata();
  setStatus('live', t('status.ready'), t('status.waiting'));
  setLastRefresh(null);
  $('language-select').addEventListener('change', event => setLanguage(event.target.value));
  $('refresh-btn').addEventListener('click', refresh);
  $('range-select').addEventListener('change', refresh);
  $('filter-status').addEventListener('change', reloadRequestsFromFilter);
  $('filter-model').addEventListener('change', reloadRequestsFromFilter);
  $('filter-endpoint').addEventListener('change', reloadRequestsFromFilter);
  $('toggle-requests').addEventListener('click', toggleRequests);
  $('auto-refresh').addEventListener('change', configureAutoRefresh);
  window.addEventListener('focus', debounce(refreshFromFocus, 2000));
  window.addEventListener('resize', debounce(rerenderTimeseriesCharts, 160));
  await refresh();
});

