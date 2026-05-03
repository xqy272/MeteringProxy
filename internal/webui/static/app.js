const apiBaseMeta = document.querySelector('meta[name="api-base"]');
const BASE = apiBaseMeta && apiBaseMeta.content ? apiBaseMeta.content : '/metering/api/';

let metadata = null;
let currentModels = [];
let autoRefreshTimer = null;
let isRefreshing = false;
let lastRefreshAt = 0;
let requestsExpanded = false;

const fallbackRanges = [
  { key: '24h', label: 'Last 24 Hours', bucket: '10m' },
  { key: 'today', label: 'Today', bucket: '10m' },
  { key: '7d', label: 'Last 7 Days', bucket: '1h' },
  { key: '30d', label: 'Last 30 Days', bucket: '1d' }
];

const $ = id => document.getElementById(id);

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
  return Number(n || 0).toLocaleString();
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

function formatRate(rate) {
  rate = Number(rate || 0);
  return (rate * 100).toFixed(1) + '%';
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
  return d.toLocaleString();
}

function formatShortTime(value) {
  if (!value) return '-';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

function shortHash(h) {
  if (!h) return '-';
  return String(h).slice(0, 12) + '...';
}

function getRange() {
  return $('range-select').value || '24h';
}

function bucketForRange(range) {
  const ranges = metadata && metadata.ranges ? metadata.ranges : fallbackRanges;
  const item = ranges.find(r => r.key === range);
  return item && item.bucket ? item.bucket : '10m';
}

function setStatus(kind, title, detail) {
  const pill = $('status-pill');
  pill.className = 'status-pill ' + (kind === 'error' ? 'err' : kind === 'partial' ? 'warn' : 'ok');
  pill.textContent = kind === 'error' ? 'Error' : kind === 'partial' ? 'Partial' : 'Live';
  $('status-title').textContent = title;
  $('status-detail').textContent = detail || '';
}

function setLastRefresh(date) {
  lastRefreshAt = date ? date.getTime() : 0;
  $('last-refresh').textContent = 'Last refresh: ' + (date ? date.toLocaleString() : 'never');
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

function applyMetadata() {
  const rangeSelect = $('range-select');
  const currentRange = rangeSelect.value;
  const ranges = metadata && metadata.ranges && metadata.ranges.length ? metadata.ranges : fallbackRanges;
  rangeSelect.innerHTML = ranges.map(r => `<option value="${esc(r.key)}">${esc(r.label)}</option>`).join('');
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
  endpointSelect.innerHTML = '<option value="">All endpoints</option>' + endpointOptions;
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
  select.innerHTML = '<option value="">All models</option>' + options;
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
  $('requests-sub').textContent = `${formatFull(failedRequests)} failed`;
  $('failure-rate').textContent = formatPercent(failedRequests, totalRequests);
  $('failure-sub').textContent = `${formatFull(failedRequests)} of ${formatFull(totalRequests)}`;
  $('total-tokens').textContent = formatNum(totalTokens);
  $('tokens-sub').textContent = `${formatNum(inputTokens)} in / ${formatNum(outputTokens)} out / ${formatNum(cachedTokens)} cached / ${formatNum(reasoningTokens)} reasoning`;
  $('total-cost').textContent = formatCost(data.total_cost);
  $('cost-sub').textContent = 'query-time pricing';
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
  $('latency-sub').textContent = `avg ${formatLat(data.avg_latency_ms)} / ttfb ${formatLat(data.p95_ttfb_ms)}`;
  $('capture-quality').textContent = captureTotal ? formatPercent(captureCaptured, captureTotal) : '-';
  $('capture-sub').textContent = `${formatFull(captureIssues)} capture issues`;

  $('request-health-summary').textContent = `${formatFull(sample)} sampled requests`;
  $('activity-success-rate').textContent = formatPercent(success, sample);
  $('activity-success-detail').textContent = `${formatFull(success)} success / ${formatFull(failed)} failed`;
  $('p95-ttfb').textContent = formatLat(data.p95_ttfb_ms);
  $('ttfb-detail').textContent = `avg ${formatLat(data.avg_ttfb_ms)}`;
  $('capture-issues').textContent = formatFull(captureIssues);
  $('capture-issues-detail').textContent = `${formatFull(captureFailed)} failed / ${formatFull(captureSkipped)} skipped`;

  if (Number(data.latest_error_status || 0) > 0) {
    $('latest-error-status').innerHTML = `<span class="badge err">${esc(data.latest_error_status)}</span>`;
    const model = data.latest_error_model ? `, ${data.latest_error_model}` : '';
    const detail = `${formatShortTime(data.latest_error_at)} ${data.latest_error_endpoint || ''}${model}`;
    $('latest-error-detail').textContent = detail.trim();
  } else {
    $('latest-error-status').innerHTML = '<span class="badge ok">none</span>';
    $('latest-error-detail').textContent = 'No request errors in this range';
  }
}

async function loadModels() {
  const data = await fetchJSON('models?range=' + encodeURIComponent(getRange()));
  currentModels = Array.isArray(data) ? data : [];
  applyModelFilterOptions();

  const tbody = $('models-table');
  if (!currentModels.length) {
    tbody.innerHTML = emptyRow(7, 'No model data', 'Captured requests with usage will appear here.');
    $('models-summary').textContent = '0 models';
    return;
  }

  const totalCost = currentModels.reduce((sum, r) => sum + (r.cost_known ? Number(r.cost || 0) : 0), 0);
  const totalTokens = currentModels.reduce((sum, r) => sum + Number(r.total_tokens || 0), 0);
  const unknownPricing = currentModels.filter(r => !r.cost_known).length;
  $('models-summary').textContent = `${currentModels.length} models, ${unknownPricing} without matched pricing`;

  tbody.innerHTML = currentModels.map(r => {
    const tokens = Number(r.total_tokens || 0);
    const requestCount = Number(r.request_count || 0);
    const avgTokens = requestCount ? Math.round(tokens / requestCount) : 0;
    const cost = Number(r.cost || 0);
    const costShare = r.cost_known && totalCost > 0 ? cost / totalCost * 100 : 0;
    const tokenShare = totalTokens > 0 ? tokens / totalTokens * 100 : 0;
    const share = r.cost_known ? costShare : tokenShare;
    const shareLabel = r.cost_known ? `${costShare.toFixed(1)}%` : `${tokenShare.toFixed(1)}% tokens`;
    const pricing = r.cost_known ? '<span class="badge ok">matched</span>' : '<span class="badge warn">unknown</span>';
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
  $('keys-summary').textContent = rows.length + ' keys';
  if (!rows.length) {
    tbody.innerHTML = emptyRow(5, 'No API key data', 'Requests with authorization headers will appear here.');
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
    tbody.innerHTML = emptyRow(11, 'No matching requests', 'Adjust the filters or choose a wider time range.');
    return;
  }
  tbody.innerHTML = rows.map(r => {
    const statusClass = r.status < 400 ? 'ok' : r.status < 500 ? 'warn' : 'err';
    const model = r.model_returned || r.model_requested || '-';
    const capture = captureBadge(r);
    const endpoint = r.stream ? `${esc(r.endpoint)} <span class="badge info">stream</span>` : esc(r.endpoint);
    const bytesTitle = `Request: ${formatBytes(r.request_bytes)} / Response: ${formatBytes(r.response_bytes)}`;
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
  if (outcome === 'captured') return '<span class="badge ok">captured</span>';
  if (outcome === 'failed') return `<span class="badge err" title="${esc(reason)}">failed</span>`;
  if (outcome === 'skipped') return `<span class="badge warn" title="${esc(reason)}">skipped</span>`;
  if (reason) return `<span class="badge neutral" title="${esc(reason)}">${esc(reason)}</span>`;
  return '<span class="badge neutral">unknown</span>';
}

async function loadTimeseries() {
  const range = getRange();
  const bucket = bucketForRange(range);
  const data = await fetchJSON('timeseries?range=' + encodeURIComponent(range) + '&bucket=' + encodeURIComponent(bucket));
  const rows = Array.isArray(data) ? data : [];
  renderTokensChart(rows, bucket);
  renderRequestsChart(rows, bucket);
}

function renderTokensChart(rows, bucket) {
  const chart = $('tokens-chart');
  if (!rows.length) {
    chart.innerHTML = '<div class="empty"><strong>No token data</strong>No captured usage in this range.</div>';
    $('tokens-chart-summary').textContent = '0 buckets';
    $('tokens-chart-left').textContent = '-';
    $('tokens-chart-right').textContent = '-';
    return;
  }

  const totals = rows.map(r => ({
    cached: Number(r.cached_tokens || 0),
    uncached: Math.max(0, Number(r.input_tokens || 0) - Number(r.cached_tokens || 0)),
    output: Number(r.output_tokens || 0),
    reasoning: Number(r.reasoning_tokens || 0),
    total: Number(r.total_tokens || 0)
  }));
  const maxStack = Math.max(...totals.map(r => r.cached + r.uncached + r.output + r.reasoning), 1);
  const totalTokens = totals.reduce((sum, r) => sum + r.total, 0);
  const peakTokens = Math.max(...totals.map(r => r.total), 0);

  chart.innerHTML = rows.map((r, i) => {
    const t = totals[i];
    const title = [
      formatShortTime(r.timestamp),
      `cached input: ${formatFull(t.cached)}`,
      `uncached input: ${formatFull(t.uncached)}`,
      `output: ${formatFull(t.output)}`,
      `reasoning: ${formatFull(t.reasoning)}`
    ].join(' | ');
    return `<div class="stack-wrap" title="${esc(title)}"><div class="stack-bar">
      ${segment('reasoning', t.reasoning, maxStack)}
      ${segment('output', t.output, maxStack)}
      ${segment('uncached', t.uncached, maxStack)}
      ${segment('cached', t.cached, maxStack)}
    </div></div>`;
  }).join('');

  $('tokens-chart-summary').textContent = `${rows.length} ${bucket} buckets, ${formatFull(totalTokens)} tokens`;
  $('tokens-chart-left').textContent = formatShortTime(rows[0].timestamp);
  $('tokens-chart-right').textContent = `Peak ${formatFull(peakTokens)} tokens`;
}

function segment(kind, value, maxStack) {
  value = Number(value || 0);
  if (value <= 0) return '';
  const height = Math.max(1, value / maxStack * 100);
  return `<span class="segment ${kind}" style="height:${height.toFixed(2)}%"></span>`;
}

function renderRequestsChart(rows, bucket) {
  const chart = $('requests-chart');
  if (!rows.length) {
    chart.innerHTML = '<div class="empty"><strong>No request data</strong>No requests in this range.</div>';
    $('requests-chart-summary').textContent = '0 buckets';
    $('requests-chart-left').textContent = '-';
    $('requests-chart-right').textContent = '-';
    return;
  }

  const width = 800;
  const height = 230;
  const padX = 24;
  const padY = 20;
  const maxCount = Math.max(...rows.map(r => Number(r.count || 0)), 1);
  const maxFailed = Math.max(...rows.map(r => Number(r.failed_count || 0)), 0);
  const xFor = i => rows.length === 1 ? width / 2 : padX + (width - padX * 2) * i / (rows.length - 1);
  const yFor = value => height - padY - (height - padY * 2) * Number(value || 0) / maxCount;
  const points = rows.map((r, i) => `${xFor(i).toFixed(1)},${yFor(r.count).toFixed(1)}`).join(' ');
  const failedPoints = rows.map((r, i) => `${xFor(i).toFixed(1)},${yFor(r.failed_count).toFixed(1)}`).join(' ');
  const area = `${padX},${height - padY} ${points} ${width - padX},${height - padY}`;
  const peakLatency = Math.max(...rows.map(r => Number(r.avg_latency_ms || 0)), 0);

  chart.innerHTML = `<svg viewBox="0 0 ${width} ${height}" role="img" aria-label="Request trend">
    <line class="axis-line" x1="${padX}" y1="${height - padY}" x2="${width - padX}" y2="${height - padY}"></line>
    <line class="axis-line" x1="${padX}" y1="${padY}" x2="${padX}" y2="${height - padY}"></line>
    <polygon class="request-area" points="${area}"></polygon>
    <polyline class="request-line" points="${points}"></polyline>
    ${maxFailed > 0 ? `<polyline class="failed-line" points="${failedPoints}"></polyline>` : ''}
    ${rows.map((r, i) => `<circle cx="${xFor(i).toFixed(1)}" cy="${yFor(r.count).toFixed(1)}" r="3" fill="currentColor"><title>${esc(`${formatShortTime(r.timestamp)}: ${formatFull(r.count)} requests, ${formatFull(r.failed_count)} failed, avg latency ${formatLat(r.avg_latency_ms)}`)}</title></circle>`).join('')}
  </svg>`;

  $('requests-chart-summary').textContent = `${rows.length} ${bucket} buckets, peak ${formatFull(maxCount)} requests`;
  $('requests-chart-left').textContent = formatShortTime(rows[0].timestamp);
  $('requests-chart-right').textContent = `Peak avg latency ${formatLat(peakLatency)}`;
}

async function loadHealth() {
  const data = await fetchJSON('health');
  $('queue-depth').textContent = formatFull(data.queue_depth);
  $('dropped-events').textContent = formatFull(data.dropped_events);
  $('parse-errors').textContent = formatFull(data.parse_errors);
  $('db-errors').textContent = formatFull(data.db_write_errors);

  const unhealthy = Number(data.dropped_events || 0) + Number(data.parse_errors || 0) + Number(data.db_write_errors || 0);
  if (data.capture_disabled || !data.metering_enabled) {
    $('health-summary').innerHTML = '<span class="badge warn">capture off</span>';
  } else if (unhealthy > 0 || Number(data.queue_depth || 0) > 0) {
    $('health-summary').innerHTML = '<span class="badge warn">attention</span>';
  } else {
    $('health-summary').innerHTML = '<span class="badge ok">healthy</span>';
  }
}

async function loadErrors() {
  const data = await fetchJSON('errors?range=' + encodeURIComponent(getRange()) + '&nonzero=true');
  const rows = Array.isArray(data.timeline) ? data.timeline : [];
  const source = data.source || 'unknown';
  const bucketCount = Number(data.bucket_count || rows.length || 0);
  const nonzeroBucketCount = Number(data.nonzero_bucket_count || rows.length || 0);
  $('errors-summary').textContent = `${source}, ${bucketCount} buckets, ${nonzeroBucketCount} non-zero`;

  if (!rows.length) {
    $('errors-table-wrap').classList.add('hidden');
    $('errors-state').classList.remove('hidden');
    $('errors-state').innerHTML = '<div class="state-box"><strong>No errors in this range</strong>All tracked error buckets are zero.</div>';
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
  btn.textContent = 'Loading';
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
      setStatus('live', 'Dashboard live', 'All panels refreshed successfully.');
    } else if (failures.length === tasks.length) {
      setStatus('error', 'Refresh failed', failures.map(f => `${f.name}: ${f.error.message}`).join(' | '));
      markFailedPanels(failures);
    } else {
      setStatus('partial', 'Partial refresh', failures.map(f => `${f.name}: ${f.error.message}`).join(' | '));
      markFailedPanels(failures);
    }

    setLastRefresh(new Date());
  } finally {
    btn.textContent = 'Refresh';
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
    setStatus('partial', 'Requests refresh failed', err.message);
  }
}

async function toggleRequests() {
  requestsExpanded = !requestsExpanded;
  $('request-details').classList.toggle('hidden', !requestsExpanded);
  $('toggle-requests').textContent = requestsExpanded ? 'Hide recent requests' : 'Show recent requests';
  if (requestsExpanded) {
    $('requests-table').innerHTML = emptyRow(11, 'Loading requests', 'Fetching the latest 100 matching rows.');
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

document.addEventListener('DOMContentLoaded', async () => {
  applyMetadata();
  $('refresh-btn').addEventListener('click', refresh);
  $('range-select').addEventListener('change', refresh);
  $('filter-status').addEventListener('change', reloadRequestsFromFilter);
  $('filter-model').addEventListener('change', reloadRequestsFromFilter);
  $('filter-endpoint').addEventListener('change', reloadRequestsFromFilter);
  $('toggle-requests').addEventListener('click', toggleRequests);
  $('auto-refresh').addEventListener('change', configureAutoRefresh);
  window.addEventListener('focus', debounce(refreshFromFocus, 2000));
  await refresh();
});

