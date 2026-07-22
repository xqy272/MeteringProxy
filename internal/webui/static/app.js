/* ============================================================
   MeteringProxy - Dashboard Application
   ============================================================ */
const apiBaseMeta = document.querySelector('meta[name="api-base"]');
const BASE = apiBaseMeta && apiBaseMeta.content ? apiBaseMeta.content : '/metering/api/';
const LANG_KEY = 'mp-lang';
const THEME_KEY = 'mp-theme';
const USAGE_MODE_KEY = 'mp-usage-mode';
const LAYOUT_TAB_KEY = 'mp-layout-tab';
const I18N = window.METERING_I18N || {};
const QUOTA_PAGE_SIZE = 8;
const layoutTabs = [
  { key: 'overview', label: 'layout.tab.overview' },
  { key: 'credentials', label: 'layout.tab.credentials' },
  { key: 'requests', label: 'layout.tab.requests' },
  { key: 'images', label: 'layout.tab.images' },
  { key: 'keys', label: 'layout.tab.keys' },
  { key: 'diagnostics', label: 'layout.tab.diagnostics' }
];

let metadata = null;
let currentModels = [];
let autoRefreshTimer = null;
let isRefreshing = false;
let lastRefreshAt = 0;
let requestsExpanded = false;
let currentLang = detectLang();
let currentTheme = detectTheme();
let currentUsageMode = detectUsageMode();
let currentLayoutTab = detectLayoutTab();
let lastTSRows = [];
let lastTSBucket = '';
let latestOverview = null;
let latestIssueItems = [];
let latestHealth = null;
let latestActivity = null;
let latestQuota = null;
let latestObservability = null;
let latestQuotaDiagnostics = null;
let selectedKeyHash = null;
let latestKeysRows = [];
let selectedKeySnapshot = null;
let keyDetailGeneration = 0;
let keyDetailAbort = null;
let keyDetailUsageMode = 'cost';
let lastKeyTSRows = [];
let lastKeyTSBucket = '';
let keyDetailBound = false;
let selectedIssueSeverity = '';
let currentIssueClassFilter = '';
let statusHideTimer = null;
let latestQuotaGroups = [];
let latestCredentialRows = [];
let quotaPage = 1;

const fallbackRanges = [
  { key: '24h', label: 'Last 24 Hours', bucket: '1h' },
  { key: 'today', label: 'Today', bucket: '1h' },
  { key: '7d', label: 'Last 7 Days', bucket: '1h' },
  { key: '30d', label: 'Last 30 Days', bucket: '1d' }
];

const $ = id => document.getElementById(id);

/* --- Language / Theme -------------------------------------- */
function detectLang() {
  try { const s = localStorage.getItem(LANG_KEY); if (s === 'en' || s === 'zh') return s; } catch (_) {}
  return (navigator.language || '').startsWith('zh') ? 'zh' : 'en';
}
function detectTheme() {
  try { const s = localStorage.getItem(THEME_KEY); if (s === 'light' || s === 'dark') return s; } catch (_) {}
  return 'light';
}
function detectUsageMode() {
  try { const s = localStorage.getItem(USAGE_MODE_KEY); if (s === 'cost' || s === 'tokens' || s === 'requests') return s; } catch (_) {}
  return 'cost';
}
function detectLayoutTab() {
  try {
    const qp = new URLSearchParams(window.location.search).get('tab');
    if (qp === 'quota-diagnostics') return 'diagnostics';
    if (layoutTabs.some(tab => tab.key === qp)) return qp;
    const s = localStorage.getItem(LAYOUT_TAB_KEY);
    if (s === 'quota-diagnostics') return 'diagnostics';
    if (layoutTabs.some(tab => tab.key === s)) return s;
  } catch (_) {}
  return 'overview';
}
function applyTheme(theme) {
  currentTheme = theme;
  document.documentElement.setAttribute('data-theme', theme);
  try { localStorage.setItem(THEME_KEY, theme); } catch (_) {}
  const sun = $('theme-icon-sun'), moon = $('theme-icon-moon');
  if (sun) sun.classList.toggle('hidden', theme === 'light');
  if (moon) moon.classList.toggle('hidden', theme === 'dark');
}
function toggleTheme() {
  applyTheme(currentTheme === 'light' ? 'dark' : 'light');
  rerenderCharts();
}
function locale() { return currentLang === 'zh' ? 'zh-CN' : 'en-US'; }
function t(key, vars) {
  const fb = I18N.en || {}, dict = I18N[currentLang] || fb;
  let s = dict[key] || fb[key] || key;
  if (!vars) return s;
  return s.replace(/\{([a-zA-Z0-9_]+)\}/g, (_, n) => vars[n] == null ? '' : String(vars[n]));
}
function esc(s) {
  if (s == null) return '';
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}
function setLang(lang) {
  if (lang !== 'en' && lang !== 'zh') return;
  currentLang = lang;
  try { localStorage.setItem(LANG_KEY, lang); } catch (_) {}
  applyI18N(); applyMeta();
  setLastRefresh(lastRefreshAt ? new Date(lastRefreshAt) : null);
  refresh();
}

function resetIssueSelection() {
  selectedIssueSeverity='';
  currentIssueClassFilter='';
  closeRequestDetails();
  const list=$('issues-list');
  if(list) list.innerHTML=`<div class="issue-class-placeholder">${esc(t('issues.select_severity_hint'))}</div>`;
}
function applyI18N() {
  document.documentElement.lang = currentLang === 'zh' ? 'zh-CN' : 'en';
  document.querySelectorAll('[data-i18n]').forEach(el => { el.textContent = t(el.dataset.i18n); });
  document.querySelectorAll('[data-i18n-aria]').forEach(el => { el.setAttribute('aria-label', t(el.dataset.i18nAria)); });
  document.querySelectorAll('[data-i18n-title]').forEach(el => { el.setAttribute('title', t(el.dataset.i18nTitle)); });
  const s = $('language-select'); if (s) s.value = currentLang;
  const zh = document.querySelector('#language-select option[value="zh"]'); if (zh) zh.textContent = '中文';
  renderLayoutTabs();
  updateToggleLabels();
}

function renderLayoutTabs() {
  const nav = $('layout-tabs');
  if(!nav) return;
  nav.innerHTML = layoutTabs.map(tab => {
    const active=tab.key===currentLayoutTab;
    return `<button class="layout-tab ${active?'active':''}" type="button" role="tab" aria-selected="${active?'true':'false'}" tabindex="${active?'0':'-1'}" data-layout-tab="${esc(tab.key)}">${esc(t(tab.label))}</button>`;
  }).join('');
  nav.querySelectorAll('[data-layout-tab]').forEach(btn => btn.addEventListener('click', () => setLayoutTab(btn.dataset.layoutTab)));
}
function setLayoutTab(tab) {
  if(tab === 'quota-diagnostics') tab = 'diagnostics';
  if(!layoutTabs.some(item => item.key === tab)) tab = 'overview';
  currentLayoutTab = tab;
  try { localStorage.setItem(LAYOUT_TAB_KEY, currentLayoutTab); } catch (_) {}
  renderLayoutTabs();
  updateLayoutPanels();
  requestAnimationFrame(rerenderCharts);
}
function updateLayoutPanels() {
  document.querySelectorAll('[data-layout-panel]').forEach(panel => {
    const hidden=panel.dataset.layoutPanel !== currentLayoutTab;
    panel.classList.toggle('layout-panel-hidden', hidden);
    panel.setAttribute('aria-hidden', hidden ? 'true' : 'false');
  });
}

/* --- Formatters -------------------------------------------- */
function fmtNum(n) { n = Number(n||0); if (Math.abs(n)>=1e9) return (n/1e9).toFixed(1)+'B'; if (Math.abs(n)>=1e6) return (n/1e6).toFixed(1)+'M'; if (Math.abs(n)>=1e3) return (n/1e3).toFixed(1)+'K'; return String(n); }
function fmtFull(n) { return Number(n||0).toLocaleString(locale()); }
function fmtCost(c) { c=Number(c||0); if(c===0)return'$0.00'; if(c>0&&c<0.01)return'<$0.01'; return'$'+c.toFixed(2); }
function fmtPct(v,total) { v=Number(v||0);total=Number(total||0); return total<=0?'0%':(v/total*100).toFixed(1)+'%'; }
function fmtLat(ms) { ms=Number(ms||0); if(ms<=0)return'-'; if(ms<1000)return ms+'ms'; return(ms/1000).toFixed(1)+'s'; }
function fmtTime(v) { if(!v)return'-'; const d=new Date(v); return isNaN(d)?v:d.toLocaleString(locale()); }
function fmtShort(v) { if(!v)return'-'; const d=new Date(v); return isNaN(d)?v:d.toLocaleString(locale(),{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'}); }
function fmtMDHM(v) {
  if(!v)return'';
  const d=new Date(v);
  if(isNaN(d))return String(v);
  const p=n=>String(n).padStart(2,'0');
  return `${p(d.getMonth()+1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}
function shortHash(h) {
  if(!h) return '-';
  const s=String(h);
  if(s === 'unknown') return 'unknown';
  if(s.length <= 12) return s;
  return s.slice(0,10)+'...';
}
function keyLabelText(row) {
  if(!row) return t('key_detail.unknown_key');
  const label=String(row.label||'').trim();
  if(label) return label;
  if(String(row.key_hash||'') === 'unknown') return t('key_detail.unknown_key');
  return shortHash(row.key_hash);
}
function costStateOf(row) {
  if(!row) return 'unavailable';
  const state=String(row.cost_state||'').toLowerCase();
  if(state === 'complete' || state === 'partial' || state === 'unavailable') return state;
  if(row.cost_known === true) return 'complete';
  if(row.cost_known === false) return 'partial';
  return 'unavailable';
}
function costAmountOf(row) {
  if(!row) return 0;
  if(row.estimated_cost != null) return Number(row.estimated_cost||0);
  return Number(row.cost||0);
}
function fmtCostDisplay(row) {
  const state=costStateOf(row);
  if(state === 'unavailable') return t('cost.unavailable_value');
  const amount=costAmountOf(row);
  if(state === 'partial') {
    if(!(Number(amount) > 0)) return t('cost.partial_unknown');
    return fmtCost(amount);
  }
  if(row && row.cost_known === false && !(Number(amount) > 0)) return t('cost.partial_unknown');
  return fmtCost(amount);
}
function costStateBadgeHTML(row) {
  const state=costStateOf(row);
  const cls=state==='complete'?'ok':(state==='partial'?'warn':'err');
  return `<span class="badge ${cls}">${esc(t('cost.state.'+state))}</span>`;
}
function partialReasonsList(row) {
  const reasons=Array.isArray(row && row.partial_reasons) ? row.partial_reasons : [];
  return reasons.map(r=>String(r||'').trim()).filter(Boolean);
}
function confidenceCounts(row) {
  const c=(row && row.usage_confidence_counts) || {};
  return {
    observed:Number(c.observed||0),
    side_channel:Number(c.side_channel||0),
    request_only:Number(c.request_only||0),
    missing_usage:Number(c.missing_usage||0),
    unsupported:Number(c.unsupported||0),
    conflict:Number(c.conflict||0)
  };
}
function isAbortError(err) {
  return !!(err && (err.name === 'AbortError' || /abort/i.test(String(err.message||''))));
}
async function fetchJSONSignal(path, signal) {
  return fetchJSON(path, signal ? { signal } : undefined);
}
function issueSourceStatusLabel(source) {
  const raw=String(source||'').trim().toLowerCase();
  if(!raw) return '';
  const key='key_detail.issue_source_status.'+raw;
  const label=t(key);
  return label===key ? raw : label;
}
function focusKeyRow(hash) {
  if(!hash) return;
  const tbody=$('keys-table');
  if(!tbody) return;
  const row=[...tbody.querySelectorAll('tr[data-key-hash]')].find(el=>el.dataset.keyHash===String(hash));
  if(row && typeof row.focus==='function'){
    try { row.focus({ preventScroll:true }); } catch(_) { row.focus(); }
  }
}
function setKeyDetailEmptyState(mode, message) {
  const emptyEl=$('key-detail-empty');
  const bodyEl=$('key-detail-body');
  if(!emptyEl) return;
  if(mode==='hide'){
    emptyEl.classList.add('hidden');
    emptyEl.hidden=true;
    return;
  }
  emptyEl.classList.remove('hidden');
  emptyEl.hidden=false;
  if(bodyEl){
    bodyEl.classList.add('hidden');
    bodyEl.hidden=true;
  }
  if(mode==='unavailable'){
    emptyEl.innerHTML=`<div class="empty-state error-text"><strong>${esc(t('key_detail.stale_title'))}</strong><span>${esc(message||t('key_detail.section_unavailable'))}</span></div>`;
  } else {
    emptyEl.innerHTML=`<div class="empty-state"><strong>${esc(t('key_detail.empty_title'))}</strong><span>${esc(t('key_detail.empty_detail'))}</span></div>`;
  }
}
function markKeyDetailUnavailable(message) {
  if(!selectedKeyHash) return;
  if(keyDetailAbort){
    try { keyDetailAbort.abort(); } catch(_) {}
    keyDetailAbort=null;
  }
  keyDetailGeneration += 1;
  lastKeyTSRows=[];
  lastKeyTSBucket='';
  showKeyDetailShell();
  renderKeyDetailHeader(selectedKeySnapshot || { key_hash:selectedKeyHash, label:'' }, 'unavailable');
  setKeyDetailEmptyState('unavailable', message);
}
function fmtKeyTrendTotal(list, meta, total) {
  if(keyDetailUsageMode!=='cost') return meta.fmtFull(total);
  const states=(list||[]).map(costStateOf);
  const anyUnavailable=states.some(s=>s==='unavailable');
  const anyIncomplete=states.some(s=>s!=='complete');
  if(anyUnavailable && states.every(s=>s==='unavailable')) return t('cost.unavailable_value');
  if(anyIncomplete && !(Number(total)>0)) {
    return anyUnavailable ? t('cost.unavailable_value') : t('cost.partial_unknown');
  }
  return meta.fmtFull(total);
}
function fmtKeyTrendPoint(value, state) {
  if(keyDetailUsageMode!=='cost') return null;
  if(state==='unavailable') return t('cost.unavailable_value');
  if(state==='partial' && !(Number(value)>0)) return t('cost.partial_unknown');
  return null;
}
function quotaCredentialLabel(value) {
  const s=String(value||'');
  if(!s) return '-';
  return s.length>42?s.slice(0,38)+'...':s;
}
function modelName(m) { return !m||m==='unknown'||m==='unidentified' ? t('model.unidentified') : m; }
function statusBadgeClass(status) {
  status=String(status||'').toLowerCase();
  if(['ok','ready','available','connected','healthy','success'].includes(status)) return 'ok';
  if(['warning','low','stale','unknown','unsupported','partial'].includes(status)) return 'warn';
  if(['error','exhausted','unavailable','disabled','disconnected'].includes(status)) return 'err';
  return 'neutral';
}
function moduleStatusLabel(status) {
  status=String(status||'disabled').toLowerCase();
  if(status==='available') return t('obs.available');
  if(status==='unavailable') return t('obs.unavailable');
  if(status==='unsupported') return t('obs.unsupported');
  if(status==='disabled') return t('obs.disabled');
  return t('obs.partial');
}
function errorDiagnosticLabel(row) {
  return row && (row.error_code || row.latest_error_code || row.error_class || row.latest_error_class || row.error_type || row.latest_error || row.error || '');
}
function selectOptionValue(id, value) {
  const el=$(id);
  if(!el) return '';
  const v=String(value||'');
  return [...el.options].some(opt=>opt.value===v) ? v : '';
}
function singleSelectOptionValue(id, values) {
  const unique=[...new Set([...(values||[])].map(v=>String(v||'')).filter(Boolean))];
  return unique.length===1 ? selectOptionValue(id, unique[0]) : '';
}
function statusFilterFromStatuses(statuses) {
  const families=new Set();
  [...(statuses||[])].forEach(status=>{
    const n=Number(status||0);
    if(n>=200&&n<400) families.add('success');
    else if(n>=400&&n<500) families.add('4xx');
    else if(n>=500) families.add('5xx');
  });
  return families.size===1 ? [...families][0] : '';
}
function syncRequestFiltersForIssue(group) {
  const status=statusFilterFromStatuses(group&&group.statuses);
  const model=singleSelectOptionValue('filter-model', group&&group.rawModels);
  const endpoint=singleSelectOptionValue('filter-endpoint', group&&group.endpoints);
  const fs=$('filter-status'), fm=$('filter-model'), fe=$('filter-endpoint');
  if(fs) fs.value=status;
  if(fm) fm.value=model;
  if(fe) fe.value=endpoint;
}
function issueClassLabel(cls, fb) {
  const key='issues.class.'+(cls||'unknown'), fallback=I18N.en||{}, dict=I18N[currentLang]||fallback;
  return dict[key]||fallback[key]||fb||cls||'';
}
function diagnosticGuide(cls, code) {
  const fb=I18N.en||{}, dict=I18N[currentLang]||fb;
  const candidates=[
    `issues.guide.${cls||'unknown'}.${code||''}`,
    `issues.guide.${cls||'unknown'}`,
    `issues.guide.${code||''}`
  ];
  for(const key of candidates){
    if(dict[key]||fb[key]) return dict[key]||fb[key];
  }
  return '';
}
function quotaWindowRank(key) {
  key=String(key||'').toLowerCase();
  if(key.includes('5h')||key.includes('5_hour')||key.includes('five')) return 0;
  if(key.includes('week')) return 1;
  if(key.includes('day')) return 2;
  if(key.includes('month')) return 3;
  return 9;
}
function quotaWindowLabel(key) {
  const raw=String(key||'').trim();
  const k=raw.toLowerCase();
  if(k.includes('5h')||k.includes('5_hour')||k.includes('five')) return t('quota.window_5h');
  if(k.includes('week')) return t('quota.window_weekly');
  if(k.includes('day')) return t('quota.window_daily');
  if(k.includes('month')) return t('quota.window_monthly');
  return raw || '-';
}
function providerKey(provider) {
  return String(provider||'').trim().toLowerCase().replace(/[^a-z0-9]+/g,'_') || 'default';
}
function providerInitial(provider) {
  const raw=String(provider||'?').trim();
  if(!raw) return '?';
  const words=raw.split(/[^a-zA-Z0-9]+/).filter(Boolean);
  if(words.length>1) return words.slice(0,2).map(w=>w[0]).join('').toUpperCase();
  return raw.slice(0,2).toUpperCase();
}
function credentialProviderClass(provider) {
  const key=providerKey(provider);
  if(['codex','antigravity','claude','kimi'].includes(key)) return key;
  return 'default';
}
function credentialProviderName(provider) {
  const key='credential.provider.'+providerKey(provider);
  const label=t(key);
  return label===key ? (provider||'-') : label;
}
function quotaWindowLabelForProvider(provider, key) {
  const raw=String(key||'').trim();
  const k=raw.toLowerCase();
  if(providerKey(provider)==='antigravity'){
    if(k.includes('project')) return t('quota.window_project');
    if(k.includes('model')) return t('quota.window_model');
    if(k.includes('minute')||k==='rpm'||k==='tpm') return t('quota.window_rate');
  }
  return quotaWindowLabel(raw);
}
function quotaResetText(reset) {
  return reset ? t('quota.resets_at',{time:fmtShort(reset)}) : t('quota.no_reset');
}
function quotaStatusClass(status) {
  status=String(status||'').toLowerCase();
  if(status==='exhausted') return 'exhausted';
  if(status==='low'||status==='warning') return 'low';
  if(status==='unsupported'||status==='unknown'||status==='stale') return 'warn';
  if(status==='error'||status==='unavailable'||status==='disabled') return 'err';
  return 'ok';
}
function quotaStatusRank(row) {
  const status=String((row&&row.status)||(row&&row.adapter_status)||'').toLowerCase();
  if(status==='exhausted') return 0;
  if(status==='error'||status==='unavailable'||status==='disabled') return 1;
  if(status==='low'||status==='warning') return 2;
  if(status==='unsupported'||status==='unknown'||status==='stale') return 3;
  return 4;
}
function quotaRowCompare(a,b) {
  const wr=quotaWindowRank(a.window_key)-quotaWindowRank(b.window_key);
  if(wr) return wr;
  const sr=quotaStatusRank(a)-quotaStatusRank(b);
  if(sr) return sr;
  return String(a.window_key||'').localeCompare(String(b.window_key||''));
}
function quotaMetric(row) {
  const limit=Number(row&&row.limit_amount||0);
  const remaining=Number(row&&row.remaining_amount||0);
  const hasQuota=limit>0;
  const pct=hasQuota?Math.max(0,Math.min(100,Math.round(remaining/limit*100))):0;
  return { limit, remaining, hasQuota, pct };
}
function quotaAmountLabel(row) {
  const m=quotaMetric(row);
  if(!m.hasQuota) return String((row&&row.status)||'').toLowerCase()==='unsupported'?t('quota.unsupported'):'-';
  return `${fmtNum(m.remaining)} / ${fmtNum(m.limit)}`;
}
function quotaDiagnosticText(data) {
  const rows=Array.isArray(data&&data.diagnostics)?data.diagnostics:[];
  if(!rows.length) return '';
  const row=rows.find(r=>String(r.status||'').toLowerCase()==='error')||rows[0];
  const status=[row.provider,row.phase,row.status].filter(Boolean).join('/');
  const detail=row.error_class||row.adapter_status||row.error_message||'';
  return [status,detail].filter(Boolean).join(': ');
}
function quotaWindowReset(row) {
  return (row&&row.reset_at)||(row&&row.expires_at)||'';
}
function quotaGroups(items) {
  const map=new Map();
  items.forEach(row=>{
    const credential=row.credential_hash||row.auth_index_hash||row.label_hash||'';
    const key=[row.provider||'',credential,row.plan||''].join('\u0001');
    if(!map.has(key)) map.set(key,{ provider:row.provider||'-', credential, plan:row.plan||'-', rows:[] });
    map.get(key).rows.push(row);
  });
  return Array.from(map.values()).map(group=>{
    group.rows=group.rows.slice().sort(quotaRowCompare);
    group.primary=group.rows.slice().sort((a,b)=>quotaStatusRank(a)-quotaStatusRank(b)||quotaRowCompare(a,b))[0]||{};
    return group;
  }).sort((a,b)=>{
    const pc=String(a.provider||'').localeCompare(String(b.provider||''));
    if(pc) return pc;
    const sr=quotaStatusRank(a.primary)-quotaStatusRank(b.primary);
    if(sr) return sr;
    return String(a.credential||'').localeCompare(String(b.credential||''));
  });
}
function isCredentialHealthRow(row) {
  if(!row) return false;
  return !row.window_key && ('success_count' in row || 'failed_count' in row || 'auth_index_hash' in row || 'label_hash' in row);
}
function credentialIdentity(row) {
  return (row&&row.credential_hash)||(row&&row.auth_index_hash)||(row&&row.label_hash)||'';
}
function credentialDisplayName(row) {
  const identity=String(row&&row.identity_hint||'').trim();
  if(identity.includes('@')) return identity;
  return (row&&row.display_label)||(row&&row.identity_hint)||(row&&row.provider)||'-';
}
function credentialStatusLabel(status) {
  const raw=String(status||'unknown').toLowerCase();
  const key='credential.status.'+raw;
  const label=t(key);
  return label===key ? (status||'-') : label;
}
function credentialHealthCompare(a,b) {
  const sr=quotaStatusRank(a)-quotaStatusRank(b);
  if(sr) return sr;
  const pc=String(a.provider||'').localeCompare(String(b.provider||''));
  if(pc) return pc;
  return credentialDisplayName(a).localeCompare(credentialDisplayName(b));
}
function parseCredentialDiagnosticText(value) {
  const raw=String(value||'').trim();
  if(!raw) return {};
  if(!(raw.startsWith('{')&&raw.endsWith('}'))) return { message: raw };
  try {
    const outer=JSON.parse(raw);
    const err=outer&&typeof outer.error==='object'&&outer.error?outer.error:outer;
    const resetRaw=err.resets_at||err.reset_at||err.next_recover_at||err.nextRetryAfter||outer.resets_at||outer.reset_at||outer.next_recover_at;
    let resetAt='';
    if(resetRaw){
      const n=Number(resetRaw);
      if(Number.isFinite(n)&&n>0) resetAt=new Date((n>100000000000?n:n*1000)).toISOString();
      else resetAt=String(resetRaw);
    }
    const resetIn=Number(err.resets_in_seconds||err.reset_in_seconds||err.retry_after_seconds||outer.resets_in_seconds||0);
    if(!resetAt && Number.isFinite(resetIn) && resetIn>0) resetAt=new Date(Date.now()+resetIn*1000).toISOString();
    return {
      type: err.type||err.error_type||outer.type||outer.error_type||'',
      code: err.code||err.error_code||outer.code||outer.error_code||'',
      message: err.message||err.error_message||err.detail||outer.message||outer.error_message||'',
      plan: err.plan_type||err.planType||err.plan||outer.plan_type||outer.planType||outer.plan||'',
      resetAt
    };
  } catch (_) {
    return { message: raw };
  }
}
function credentialDiagnostic(row) {
  const parsed=parseCredentialDiagnosticText((row&&row.status_message)||(row&&row.error_message)||'');
  const message=parsed.message||(row&&row.error_message)||(row&&row.status_message)||'';
  const resetAt=(row&&row.next_retry_after)||parsed.resetAt||'';
  const label=row&&row.error_class?issueClassLabel(row.error_class,row.error_class):(parsed.type||parsed.code||t('credential.message'));
  const plan=(row&&row.plan)||parsed.plan||'';
  const rawTitle=[row&&row.error_class,row&&row.error_type,row&&row.error_code,message,resetAt&&quotaResetText(resetAt)].filter(Boolean).join(' / ');
  return { label, message, resetAt, plan, title: rawTitle };
}
function titleCaseValue(value) {
  const raw=String(value||'').replace(/[_-]+/g,' ').trim();
  if(!raw) return '';
  return raw.replace(/\b([a-z])([a-z0-9]*)/gi,(_,a,b)=>a.toUpperCase()+b.toLowerCase());
}
function credentialPlanFromQuotaRows(rows) {
  const row=(rows||[]).find(r=>r&&r.plan&&r.plan!=='-');
  return row ? row.plan : '';
}
function credentialPlanBadgeHTML(plan, provider) {
  const raw=String(plan||'').trim();
  if(!raw) return '';
  const cls=raw.toLowerCase().replace(/[^a-z0-9]+/g,'-') || 'unknown';
  return `<span class="credential-plan-badge plan-${esc(cls)} provider-${esc(credentialProviderClass(provider))}" title="${esc(t('credential.plan'))}: ${esc(raw)}">
    <span>${esc(t('credential.plan'))}</span><strong>${esc(titleCaseValue(raw))}</strong>
  </span>`;
}
function credentialQuotaTitle(provider) {
  const key='credential.quota_title.'+providerKey(provider);
  const label=t(key);
  return label===key ? t('credential.quota_title.default') : label;
}
function credentialQuotaEmptyText(row, diag) {
  const provider=providerKey(row&&row.provider);
  const status=String(row&&row.status||'').toLowerCase();
  const err=String(row&&row.error_class||'').toLowerCase();
  if((diag&&diag.resetAt)||err.includes('quota')) return t('credential.quota_limited_hint');
  if(status==='disabled') return t('credential.quota_disabled_hint');
  if(status==='unavailable'||status==='error') return t('credential.quota_unavailable_hint');
  const key='credential.quota_empty.'+provider;
  const label=t(key);
  return label===key ? t('credential.no_quota_snapshot') : label;
}
function credentialQuotaRowsFor(row, quotaRows) {
  const provider=String(row&&row.provider||'');
  const credential=credentialIdentity(row);
  const alt=new Set([credential,row&&row.auth_index_hash,row&&row.label_hash].filter(Boolean));
  return (quotaRows||[]).filter(q=>{
    if(provider && q.provider && String(q.provider)!==provider) return false;
    return alt.has(q.credential_hash||'') || alt.has(q.auth_index_hash||'') || alt.has(q.label_hash||'');
  }).slice().sort(quotaRowCompare);
}
function credentialQuotaSectionHTML(row, quotaRows, diag) {
  const provider=row&&row.provider||'';
  const rows=credentialQuotaRowsFor(row, quotaRows);
  const title=credentialQuotaTitle(provider);
  if(rows.length){
    return `<div class="credential-quota-section provider-${esc(credentialProviderClass(provider))}">
      <div class="credential-quota-head"><span>${esc(title)}</span></div>
      <div class="credential-quota-windows">${rows.map(q=>quotaWindowItemHTML(q,true,provider)).join('')}</div>
    </div>`;
  }
  const qClass=quotaStatusClass((row&&row.status)||'unknown');
  const retry=(diag&&diag.resetAt)?`<div class="credential-quota-retry">${esc(t('credential.next_retry'))}: <strong>${esc(fmtShort(diag.resetAt))}</strong></div>`:'';
  return `<div class="credential-quota-section provider-${esc(credentialProviderClass(provider))}">
    <div class="credential-quota-head"><span>${esc(title)}</span></div>
    <div class="credential-quota-empty ${qClass}">
      <span>${esc(credentialQuotaEmptyText(row,diag))}</span>
      ${retry}
    </div>
  </div>`;
}
function credentialHealthStats(row) {
  const success=Number(row&&row.success_count||0);
  const failed=Number(row&&row.failed_count||0);
  const total=success+failed;
  const recentSuccess=Number(row&&row.recent_success_count||0);
  const recentFailed=Number(row&&row.recent_failed_count||0);
  const recentTotal=recentSuccess+recentFailed;
  const pct=total>0?Math.max(0,Math.min(100,success/total*100)):0;
  const recentPct=recentTotal>0?Math.max(0,Math.min(100,recentSuccess/recentTotal*100)):pct;
  return { success, failed, total, pct, recentSuccess, recentFailed, recentTotal, recentPct };
}
function credentialHealthBucketClass(bucket) {
  const success=Number(bucket&&bucket.success||0);
  const failed=Number(bucket&&bucket.failed||0);
  const total=success+failed;
  if(total<=0) return 'empty';
  const pct=success/total*100;
  if(pct>=95) return 'ok';
  if(pct>=80) return 'warn';
  return 'err';
}
function credentialHealthBucketTitle(bucket) {
  const success=Number(bucket&&bucket.success||0);
  const failed=Number(bucket&&bucket.failed||0);
  const total=success+failed;
  const pct=total>0?(success/total*100).toFixed(1)+'%':'-';
  if(total<=0) return `${bucket&&bucket.time?bucket.time:'-'}\n${t('credential.no_requests')}`;
  return `${bucket&&bucket.time?bucket.time:'-'}\n${t('credential.success')} ${fmtFull(success)} / ${t('credential.failed')} ${fmtFull(failed)} (${pct})`;
}
function credentialEmptyHealthBuckets(row) {
  const count=20;
  const checked=Date.parse(row&&row.checked_at||'');
  if(!checked) return Array.from({length:count},()=>({time:'',success:0,failed:0}));
  const span=10*60*1000;
  return Array.from({length:count},(_,i)=>{
    const start=new Date(checked-(count-i)*span);
    const end=new Date(start.getTime()+span);
    const hm=d=>`${String(d.getHours()).padStart(2,'0')}:${String(d.getMinutes()).padStart(2,'0')}`;
    return { time:`${hm(start)} - ${hm(end)}`, success:0, failed:0 };
  });
}
function credentialHealthTimelineHTML(row, stats) {
  const realBuckets=Array.isArray(row&&row.recent_requests)?row.recent_requests:[];
  const buckets=realBuckets.length?realBuckets:credentialEmptyHealthBuckets(row);
  const hasRecent=realBuckets.some(bucket=>Number(bucket&&bucket.success||0)+Number(bucket&&bucket.failed||0)>0);
  const healthPct=hasRecent&&stats.recentTotal>0?stats.recentPct.toFixed(1)+'%':'--';
  return `<div class="credential-health-row timeline">
    <div>
      <span>${esc(t('credential.health'))}</span>
      <div class="credential-health-segments" aria-label="${esc(t('credential.health'))}">
        ${buckets.map(bucket=>`<i class="${esc(credentialHealthBucketClass(bucket))}" title="${esc(credentialHealthBucketTitle(bucket))}"></i>`).join('')}
      </div>
    </div>
    <strong class="mono">${esc(healthPct)}</strong>
  </div>`;
}
function credentialHealthCardHTML(row, quotaRows) {
  const status=(row&&row.status)||'-';
  const qClass=quotaStatusClass(status);
  const credential=credentialIdentity(row);
  const displayName=credentialDisplayName(row);
  const identityHint=row&&row.identity_hint&&row.identity_hint!==displayName?row.identity_hint:'';
  const diag=credentialDiagnostic(row);
  const matchingQuotaRows=credentialQuotaRowsFor(row, quotaRows);
  const plan=diag.plan||credentialPlanFromQuotaRows(matchingQuotaRows);
  const title=[diag.title,row&&row.checked_at&&fmtTime(row.checked_at)].filter(Boolean).join(' / ');
  const planBadge=credentialPlanBadgeHTML(plan,row&&row.provider);
  const stats=credentialHealthStats(row);
  const retry=diag.resetAt?`<div class="credential-reset-chip">${esc(t('credential.next_retry'))}: <strong>${esc(fmtShort(diag.resetAt))}</strong></div>`:'';
  const errLevel=qClass==='err'?'err':(qClass==='low'||qClass==='warn'?'warn':'neutral');
  const err=row&&(row.error_class||diag.message)?`<div class="credential-alert ${errLevel}" title="${esc(title)}">
      <div><strong>${esc(diag.label)}</strong>${diag.message?`<p>${esc(diag.message)}</p>`:''}</div>
      ${retry}
    </div>`:'';
  const providerClass=credentialProviderClass(row&&row.provider);
  const providerName=credentialProviderName(row&&row.provider);
  const quotaSection=credentialQuotaSectionHTML(row, quotaRows, {...diag, plan});
  const healthTimeline=credentialHealthTimelineHTML(row,stats);
  return `<div class="quota-account credential-card provider-${esc(providerClass)} ${qClass}">
    <div class="credential-card-top">
      <div class="credential-provider-wrap">
        <span class="credential-provider-chip">${esc(providerName)}</span>
        ${planBadge}
      </div>
      <span class="badge ${statusBadgeClass(status)}" title="${esc(title)}">${esc(credentialStatusLabel(status))}</span>
    </div>
    <div class="credential-title" title="${esc(displayName)}">${esc(displayName)}</div>
    <div class="credential-meta">${identityHint?`<span title="${esc(identityHint)}">${esc(identityHint)}</span>`:''}<span>${esc(t('credential.checked_at',{time:fmtShort(row&&row.checked_at)}))}</span></div>
    <div class="credential-stat-pills">
      <span class="credential-stat ok">${esc(t('credential.success'))} <strong class="mono">${esc(fmtFull(stats.success))}</strong></span>
      <span class="credential-stat err">${esc(t('credential.failed'))} <strong class="mono">${esc(fmtFull(stats.failed))}</strong></span>
    </div>
    ${healthTimeline}
    ${quotaSection}
    ${err}
  </div>`;
}
function renderCredentialHealthSummary(rows, emptyText, quotaRows) {
  const el=$('quota-window-summary');
  if(!el) return;
  latestCredentialRows=Array.isArray(rows)?rows:[];
  latestQuotaGroups=[];
  const allQuotaRows=Array.isArray(quotaRows)?quotaRows:[];
  if(!latestCredentialRows.length){
    el.innerHTML=`<div class="quota-empty-card">${esc(emptyText||t('state.no_quota_data'))}</div>`;
    return;
  }
  const totalPages=Math.max(1,Math.ceil(latestCredentialRows.length/QUOTA_PAGE_SIZE));
  quotaPage=Math.min(Math.max(1,quotaPage),totalPages);
  const start=(quotaPage-1)*QUOTA_PAGE_SIZE;
  const visibleRows=latestCredentialRows.slice(start,start+QUOTA_PAGE_SIZE);
  const pager=latestCredentialRows.length>QUOTA_PAGE_SIZE?`<div class="quota-pager">
    <button class="ctrl-btn" type="button" data-credential-page="prev" ${quotaPage<=1?'disabled':''}>${esc(t('action.prev_page'))}</button>
    <span>${esc(t('quota.page_status',{page:quotaPage,total:totalPages,count:latestCredentialRows.length}))}</span>
    <button class="ctrl-btn" type="button" data-credential-page="next" ${quotaPage>=totalPages?'disabled':''}>${esc(t('action.next_page'))}</button>
  </div>`:'';
  el.innerHTML=visibleRows.map(row=>credentialHealthCardHTML(row,allQuotaRows)).join('')+pager;
  el.querySelectorAll('[data-credential-page]').forEach(btn=>btn.addEventListener('click',()=>{
    quotaPage+=btn.dataset.credentialPage==='next'?1:-1;
    renderCredentialHealthSummary(latestCredentialRows,undefined,allQuotaRows);
  }));
}
function quotaPercentText(row) {
  const m=quotaMetric(row);
  return m.hasQuota?`${m.pct}%`:'--';
}
function quotaMeterHTML(row, compact) {
  const m=quotaMetric(row);
  const qClass=quotaStatusClass((row&&row.status)||(row&&row.adapter_status));
  const unit=row&&row.unit?`<div class="quota-unit">${esc(row.unit)}</div>`:'';
  if(!m.hasQuota) return `<div class="quota-empty mono">${esc(quotaAmountLabel(row))}</div>${unit}`;
  const label=quotaAmountLabel(row);
  return `<div class="quota-meter ${compact?'compact':''} ${qClass}" style="--pct:${m.pct}%"><span></span>${compact?'':`<strong class="mono">${esc(label)}</strong>`}</div>${unit}`;
}
function quotaWindowItemHTML(row, compact, providerOverride) {
  const status=(row&&row.status)||(row&&row.adapter_status)||'-';
  const qClass=quotaStatusClass(status);
  const window=row&&row.window_key?row.window_key:'-';
  const provider=providerOverride||(row&&row.provider)||'';
  const windowLabel=quotaWindowLabelForProvider(provider,window);
  const reset=quotaWindowReset(row);
  const title=[row&&row.adapter_status,row&&row.error_class,windowLabel,quotaResetText(reset)].filter(Boolean).join(' / ');
  if(compact){
    return `<div class="quota-window-item compact ${qClass}">
      <div class="quota-window-line">
        <div class="quota-window ${qClass}">${esc(windowLabel)}</div>
        <div class="quota-window-meta"><strong class="mono">${esc(quotaPercentText(row))}</strong>${reset?`<span>${esc(fmtMDHM(reset))}</span>`:''}</div>
      </div>
      ${quotaMeterHTML(row,true)}
    </div>`;
  }
  return `<div class="quota-window-item ${compact?'compact':''} ${qClass}">
    <div class="quota-window-line">
      <div>
        <div class="quota-window ${qClass}">${esc(windowLabel)}</div>
        <div class="quota-window-sub mono">${esc(window)}</div>
      </div>
      <span class="badge ${statusBadgeClass(status)}" title="${esc(title)}">${esc(status)}</span>
    </div>
    ${quotaMeterHTML(row,compact)}
    <div class="quota-window-foot">${esc(quotaResetText(reset))}</div>
  </div>`;
}
function renderQuotaSummary(groups, emptyText) {
  const el=$('quota-window-summary');
  if(!el) return;
  latestQuotaGroups=Array.isArray(groups)?groups:[];
  latestCredentialRows=[];
  if(!groups.length){
    el.innerHTML=`<div class="quota-empty-card">${esc(emptyText||t('state.no_quota_data'))}</div>`;
    return;
  }
  const totalPages=Math.max(1,Math.ceil(groups.length/QUOTA_PAGE_SIZE));
  quotaPage=Math.min(Math.max(1,quotaPage),totalPages);
  const start=(quotaPage-1)*QUOTA_PAGE_SIZE;
  const visibleGroups=groups.slice(start,start+QUOTA_PAGE_SIZE);
  const pager=groups.length>QUOTA_PAGE_SIZE?`<div class="quota-pager">
    <button class="ctrl-btn" type="button" data-quota-page="prev" ${quotaPage<=1?'disabled':''}>${esc(t('action.prev_page'))}</button>
    <span>${esc(t('quota.page_status',{page:quotaPage,total:totalPages,count:groups.length}))}</span>
    <button class="ctrl-btn" type="button" data-quota-page="next" ${quotaPage>=totalPages?'disabled':''}>${esc(t('action.next_page'))}</button>
  </div>`:'';
  el.innerHTML=visibleGroups.map(group=>{
    const primary=group.primary||{};
    const status=primary.status||primary.adapter_status||'-';
    const title=[primary.adapter_status,primary.error_class].filter(Boolean).join(' / ');
    return `<div class="quota-account ${quotaStatusClass(status)}">
      <div class="quota-account-head">
        <div>
          <div class="quota-account-title">${esc(group.provider)}</div>
          <div class="quota-account-sub"><code class="quota-credential" title="${esc(group.credential)}">${esc(quotaCredentialLabel(group.credential))}</code>${group.plan&&group.plan!=='-'?`<span>${esc(group.plan)}</span>`:''}</div>
        </div>
        <span class="badge ${statusBadgeClass(status)}" title="${esc(title)}">${esc(status)}</span>
      </div>
      <div class="quota-account-windows">${group.rows.map(row=>quotaWindowItemHTML(row,false)).join('')}</div>
    </div>`;
  }).join('')+pager;
  el.querySelectorAll('[data-quota-page]').forEach(btn=>btn.addEventListener('click',()=>{
    const dir=btn.dataset.quotaPage;
    quotaPage+=dir==='next'?1:-1;
    renderQuotaSummary(latestQuotaGroups);
  }));
}

/* --- Status / refresh -------------------------------------- */
function setStatus(kind, title, detail) {
  if(statusHideTimer){clearTimeout(statusHideTimer);statusHideTimer=null;}
  const strip=$('status-strip');
  if(strip) strip.classList.remove('is-hidden');
  const dot = $('status-dot');
  if (dot) { dot.className = 'status-dot' + (kind==='error'?' err':kind==='partial'?' warn':''); }
  const tt = $('status-title'); if(tt) tt.textContent = title;
  const dd = $('status-detail'); if(dd) dd.textContent = detail||'';
  // legacy compat
  const pill = $('status-pill');
  if (pill) { pill.className = 'status-pill hidden '+(kind==='error'?'err':kind==='partial'?'warn':'ok'); pill.textContent = title; }
  const ttl=kind==='live'?4200:kind==='partial'?9000:kind==='error'?12000:0;
  if(ttl>0) statusHideTimer=setTimeout(()=>{const el=$('status-strip'); if(el) el.classList.add('is-hidden');}, ttl);
}
function setLastRefresh(date) {
  lastRefreshAt = date ? date.getTime() : 0;
  const el = $('last-refresh');
  if (el) el.textContent = date ? t('status.last_refresh', { time: date.toLocaleString(locale()) }) : '';
}
function setText(id, v) { const el=$(id); if(el)el.textContent=v==null||v===''?'-':String(v); }
function setNodeState() {} // proxy path removed - no-op
function setRefreshing(active) {
  document.documentElement.classList.toggle('is-refreshing', active);
  const strip=$('status-strip');
  if(strip) {
    strip.setAttribute('aria-busy', active ? 'true' : 'false');
    if(active) {
      if(statusHideTimer){clearTimeout(statusHideTimer);statusHideTimer=null;}
      strip.classList.remove('is-hidden');
      statusHideTimer=setTimeout(()=>{const el=$('status-strip'); if(el) el.classList.add('is-hidden');}, 18000);
    }
  }
  const btn=$('refresh-btn');
  if(btn) {
    btn.disabled=active;
    btn.classList.toggle('is-loading', active);
    btn.setAttribute('aria-busy', active ? 'true' : 'false');
    btn.textContent=active ? t('action.loading') : t('action.refresh');
  }
}

/* --- API --------------------------------------------------- */
async function fetchJSON(path, opts) {
  const res = await fetch(BASE+path, Object.assign({ cache: 'no-store', credentials:'same-origin', headers:{'Accept':'application/json'} }, opts||{}));
  if (!res.ok) {
    let detail='';
    try { detail=(await res.text()).trim(); } catch (_) {}
    const err = new Error(BASE+path+' HTTP '+res.status+(detail?': '+detail.slice(0,180):''));
    err.status=res.status;
    throw err;
  }
  return res.json();
}
function getRange() { return $('range-select').value || '24h'; }
function bucketFor(range) {
  const ranges = metadata && metadata.ranges ? metadata.ranges : fallbackRanges;
  const item = ranges.find(r => r.key === range);
  return item && item.bucket ? item.bucket : '1h';
}
function emptyRow(cols, title, detail) { return `<tr><td colspan="${cols}" class="empty-state"><strong>${esc(title)}</strong>${esc(detail||'')}</td></tr>`; }
function errorRow(cols, msg) { return `<tr><td colspan="${cols}" class="empty-state error-text">${esc(msg)}</td></tr>`; }

/* --- Metadata ---------------------------------------------- */
function applyMeta() {
  const sel = $('range-select'), cur = sel.value;
  const ranges = metadata && metadata.ranges && metadata.ranges.length ? metadata.ranges : fallbackRanges;
  sel.innerHTML = ranges.map(r => `<option value="${esc(r.key)}">${esc(t('range.'+r.key)||r.label||r.key)}</option>`).join('');
  if ([...sel.options].some(o=>o.value===cur)) sel.value=cur; else sel.value=ranges[0].key;
  const ep = $('filter-endpoint'), curEp = ep.value;
  const epOpts = ((metadata&&metadata.endpoints)||[]).filter(e=>e.capture_mode!=='passthrough')
    .map(e=>`<option value="${esc(e.filter_value||e.path)}">${esc(e.display_name||e.path)}</option>`).join('');
  ep.innerHTML = `<option value="">${esc(t('filter.all_endpoints'))}</option>`+epOpts;
  if([...ep.options].some(o=>o.value===curEp)) ep.value=curEp;
}
function applyModelFilter() {
  const sel=$('filter-model'), cur=sel.value;
  const opts = currentModels.map(r=>r.model||'unidentified').filter((v,i,a)=>a.indexOf(v)===i)
    .map(m=>`<option value="${esc(m)}">${esc(modelName(m))}</option>`).join('');
  sel.innerHTML = `<option value="">${esc(t('filter.all_models'))}</option>`+opts;
  if([...sel.options].some(o=>o.value===cur)) sel.value=cur;
}
async function loadMetadata() { metadata = await fetchJSON('metadata'); applyMeta(); }

/* --- Overview ---------------------------------------------- */
async function loadOverview() {
  const data = await fetchJSON('overview?range='+encodeURIComponent(getRange()));
  if(!data) return; latestOverview = data;
  if(data.selected && data.selected.data) {
    const s=data.selected.data, recent=data.recent_1h&&data.recent_1h.data?data.recent_1h.data:{};
    const toks=Number(s.total_tokens||0), inp=Number(s.total_input_tokens||0), out=Number(s.total_output_tokens||0);
    const cached=Number(s.total_cached_tokens||0), reas=Number(s.total_reasoning_tokens||0);
    setText('total-requests', fmtNum(s.total_requests||0));
    setText('requests-sub', t('metric.recent_failed',{count:fmtFull(recent.failed_requests||0)}));
    setText('total-tokens', fmtNum(toks));
    setText('tokens-sub', inp||out?t('metric.token_mix',{input:fmtNum(inp),output:fmtNum(out),cached:fmtNum(cached),reasoning:fmtNum(reas)}):t('metric.total_token_count',{count:fmtFull(toks)}));
    setText('total-cost', fmtCost(s.total_cost||0));
    const partial = data.cost&&data.cost.data&&data.cost.data.partial;
    const unpriced = data.cost&&data.cost.data?Number(data.cost.data.unpriced_models||0):0;
    setText('cost-sub', partial?t('metric.partial_estimate')+(unpriced?' / '+t('metric.unpriced_models',{count:fmtFull(unpriced)}):''):t('metric.full_estimate'));
    setText('p95-latency', fmtLat(s.p95_latency_ms||0));
    setText('latency-sub', 'TTFB '+fmtLat(s.p95_ttfb_ms||0));
    renderUsagePanel();
  }
}

/* --- Activity ---------------------------------------------- */
async function loadActivity() {
  const data = await fetchJSON('activity?range='+encodeURIComponent(getRange()));
  latestActivity = data;
  const sample=Number(data.sample_size||0), success=Number(data.success_count||0), failed=Number(data.failed_count||0);
  const capF=Number(data.capture_failed||0), capS=Number(data.capture_skipped||0);
  setText('request-health-summary', t('metric.sampled_requests',{count:fmtFull(sample)}));
  setText('activity-success-rate', fmtPct(success,sample));
  setText('activity-success-detail', t('metric.success_failed',{success:fmtFull(success),failed:fmtFull(failed)}));
  setText('p95-ttfb', fmtLat(data.p95_ttfb_ms));
  setText('ttfb-detail', t('metric.avg_latency_ttfb',{avg:fmtLat(data.avg_ttfb_ms),ttfb:fmtLat(data.p95_ttfb_ms)}));
  setText('capture-issues', fmtFull(capF+capS));
  setText('capture-issues-detail', t('metric.capture_failed_skipped',{failed:fmtFull(capF),skipped:fmtFull(capS)}));
  if(Number(data.latest_error_status||0)>0) {
    const diag=errorDiagnosticLabel(data)||String(data.latest_error_status);
    $('latest-error-status').innerHTML=`<span class="badge err" title="HTTP ${esc(data.latest_error_status)}">${esc(diag)}</span>`;
    $('latest-error-detail').textContent=[fmtShort(data.latest_error_at),'HTTP '+data.latest_error_status,data.latest_error_endpoint||'',data.latest_error_model||''].filter(Boolean).join(' / ');
  } else {
    $('latest-error-status').innerHTML=`<span class="badge ok">${esc(t('badge.none'))}</span>`;
    $('latest-error-detail').textContent=t('metric.no_request_errors');
  }
}

/* --- Models ------------------------------------------------ */
async function loadModels() {
  const data = await fetchJSON('models?range='+encodeURIComponent(getRange()));
  currentModels = Array.isArray(data)?data:[];
  applyModelFilter();
  const tbody=$('models-table');
  renderUsagePanel();
  if(!currentModels.length) {
    if(tbody) tbody.innerHTML=emptyRow(7,t('state.no_model_data'),t('state.model_hint'));
    setText('models-summary',t('summary.zero_models')); return;
  }
  const unkn=currentModels.filter(r=>!r.cost_known).length;
  setText('models-summary',t('summary.models',{count:currentModels.length,unknown:unkn}));
  if(!tbody) return;
  const maxReq=Math.max(...currentModels.map(r=>Number(r.request_count||0)),1);
  tbody.innerHTML=currentModels.map(r=>{
    const tok=Number(r.total_tokens||0), inp=Number(r.input_tokens||0), cached=Number(r.cached_tokens||0);
    const req=Number(r.request_count||0), fail=Number(r.failed_count||0), cost=Number(r.cost||0);
    const cache=inp>0?(cached/inp*100).toFixed(1)+'%':'0%';
    const pct=Math.max(3,Math.round(req/maxReq*100));
    const pricing=r.cost_known?`<span class="badge ok">${esc(t('badge.matched'))}</span>`:`<span class="badge warn">${esc(t('model.unpriced'))}</span>`;
    const src=r.model_source==='returned'?t('model.source_returned'):r.model_source==='requested'?t('model.source_requested'):'';
    return `<tr>
      <td><div class="model-name" title="${esc(modelName(r.model))}">${esc(modelName(r.model))}</div>${src?`<div class="model-source">${esc(src)}</div>`:''}</td>
      <td class="numeric"><span class="usage-bar" style="--pct:${pct}%"><span></span></span><span class="mono">${fmtFull(req)}</span></td>
      <td class="numeric mono">${fmtFull(fail)}</td>
      <td class="numeric mono">${fmtNum(tok)}</td>
      <td class="numeric mono">${cache}</td>
      <td class="numeric mono">${r.cost_known?fmtCost(cost):'-'}</td>
      <td>${pricing}</td>
    </tr>`;
  }).join('');
}

/* --- Keys -------------------------------------------------- */
function bindKeyDetailControls() {
  if(keyDetailBound) return;
  keyDetailBound = true;
  const tbody=$('keys-table');
  if(tbody){
    tbody.addEventListener('click', e=>{
      const row=e.target.closest('tr[data-key-hash]');
      if(!row) return;
      selectKey(row.dataset.keyHash);
    });
    tbody.addEventListener('keydown', e=>{
      if(e.key!=='Enter' && e.key!==' ') return;
      const row=e.target.closest('tr[data-key-hash]');
      if(!row) return;
      e.preventDefault();
      selectKey(row.dataset.keyHash);
    });
  }
  const closeBtn=$('key-detail-close');
  if(closeBtn) closeBtn.addEventListener('click', clearKeySelection);
  const copyBtn=$('key-detail-copy');
  if(copyBtn) copyBtn.addEventListener('click', copySelectedKeyHash);
  document.querySelectorAll('[data-key-usage-mode]').forEach(btn=>{
    btn.addEventListener('click', ()=>setKeyDetailUsageMode(btn.dataset.keyUsageMode));
  });
}

function clearKeySelection() {
  const prevHash = selectedKeyHash;
  selectedKeyHash = null;
  selectedKeySnapshot = null;
  lastKeyTSRows = [];
  lastKeyTSBucket = '';
  if(keyDetailAbort){
    try { keyDetailAbort.abort(); } catch (_) {}
    keyDetailAbort = null;
  }
  keyDetailGeneration += 1;
  renderKeysTable();
  hideKeyDetail();
  focusKeyRow(prevHash);
}

function selectKey(keyHash) {
  const hash=String(keyHash||'');
  if(!hash) return;
  if(selectedKeyHash === hash){
    // re-open/refresh same selection
    loadKeyDetail();
    return;
  }
  selectedKeyHash = hash;
  const found=(latestKeysRows||[]).find(r=>String(r.key_hash||'')===hash);
  if(found) selectedKeySnapshot = found;
  renderKeysTable();
  loadKeyDetail();
  const detail=$('key-detail');
  if(detail) detail.scrollIntoView({block:'nearest', behavior:'smooth'});
}

function hideKeyDetail() {
  const detail=$('key-detail');
  if(!detail) return;
  detail.classList.add('hidden');
  detail.hidden = true;
  const empty=$('key-detail-empty');
  const body=$('key-detail-body');
  if(empty){ empty.classList.add('hidden'); empty.hidden=true; }
  if(body){ body.classList.add('hidden'); body.hidden=true; }
}

function showKeyDetailShell() {
  const detail=$('key-detail');
  if(!detail) return;
  detail.classList.remove('hidden');
  detail.hidden = false;
}

function setKeyDetailUsageMode(mode) {
  if(mode!=='cost' && mode!=='tokens' && mode!=='requests') return;
  keyDetailUsageMode = mode;
  document.querySelectorAll('[data-key-usage-mode]').forEach(btn=>{
    const active=btn.dataset.keyUsageMode===keyDetailUsageMode;
    btn.classList.toggle('active', active);
    btn.setAttribute('aria-selected', active?'true':'false');
  });
  renderKeyTrend(lastKeyTSRows, lastKeyTSBucket);
}

function renderKeysTable() {
  const tbody=$('keys-table');
  if(!tbody) return;
  const rows=Array.isArray(latestKeysRows)?latestKeysRows:[];
  setText('keys-summary', t('summary.keys',{count:rows.length})+(selectedKeyHash?` \u00b7 ${t('key_detail.selected')}`:''));
  if(!rows.length){
    tbody.innerHTML=emptyRow(7,t('state.no_key_data'),t('state.key_hint'));
    return;
  }
  tbody.innerHTML=rows.map(r=>{
    const hash=String(r.key_hash||'');
    const selected=selectedKeyHash!=null && selectedKeyHash===hash;
    const req=Number(r.request_count||0);
    const fail=Number(r.failed_count||0);
    const failPct=req>0 ? ((fail/req)*100).toFixed(1)+'%' : '0%';
    const label=keyLabelText(r);
    const short=shortHash(hash);
    return `<tr class="key-row${selected?' is-selected':''}" data-key-hash="${esc(hash)}" tabindex="0" role="button" aria-pressed="${selected?'true':'false'}" aria-label="${esc(t('key_detail.open_aria',{label}))}">
      <td>
        <div class="key-label-cell">
          <div class="key-label-primary" title="${esc(label)}">${esc(label)}</div>
          <div class="key-label-secondary mono"><code>${esc(short)}</code></div>
        </div>
      </td>
      <td class="numeric mono">${fmtFull(req)}</td>
      <td class="numeric mono">${esc(failPct)}</td>
      <td class="numeric mono">${fmtNum(r.total_tokens)}</td>
      <td class="numeric mono">${esc(fmtCostDisplay(r))}</td>
      <td>${costStateBadgeHTML(r)}</td>
      <td class="mono">${esc(fmtShort(r.latest_seen_at))}</td>
    </tr>`;
  }).join('');
}

async function loadKeys() {
  const data = await fetchJSON('keys?range='+encodeURIComponent(getRange()));
  latestKeysRows = Array.isArray(data)?data:[];
  if(selectedKeyHash){
    const found=latestKeysRows.find(r=>String(r.key_hash||'')===selectedKeyHash);
    if(found) selectedKeySnapshot = found;
  }
  renderKeysTable();
  if(selectedKeyHash) await loadKeyDetail();
  else hideKeyDetail();
}

async function copySelectedKeyHash() {
  const status=$('key-detail-copy-status');
  const hash=String(selectedKeyHash||'');
  if(!hash || hash==='unknown'){
    if(status) status.textContent = t('key_detail.copy_unavailable');
    return;
  }
  try {
    if(navigator.clipboard && navigator.clipboard.writeText){
      await navigator.clipboard.writeText(hash);
    } else {
      const ta=document.createElement('textarea');
      ta.value=hash;
      ta.setAttribute('readonly','');
      ta.style.position='fixed';
      ta.style.left='-9999px';
      document.body.appendChild(ta);
      try {
        ta.select();
        if(!document.execCommand('copy')) throw new Error('copy failed');
      } finally {
        if(ta.parentNode) ta.parentNode.removeChild(ta);
      }
    }
    if(status) status.textContent = t('key_detail.copy_ok');
  } catch (_) {
    if(status) status.textContent = t('key_detail.copy_failed');
  }
}

function renderKeyDetailHeader(row, availability='current') {
  const hash=String((row && row.key_hash) || selectedKeyHash || '');
  const label=keyLabelText(row || {key_hash:hash, label:''});
  setText('key-detail-label', label);
  const badge=$('key-detail-cost-badge');
  if(badge){
    if(availability==='empty'){
      badge.className = 'badge neutral';
      badge.textContent = t('key_detail.no_range_data');
    } else if(availability==='unavailable'){
      badge.className = 'badge err';
      badge.textContent = t('cost.state.unavailable');
    } else if(row){
      badge.className = 'badge ' + (costStateOf(row)==='complete'?'ok':(costStateOf(row)==='partial'?'warn':'err'));
      badge.textContent = t('cost.state.'+costStateOf(row));
    } else {
      badge.className = 'badge neutral';
      badge.textContent = t('key_detail.no_range_data');
    }
  }
  setText('key-detail-short-hash', shortHash(hash));
  const full=$('key-detail-full-hash');
  if(full){
    full.textContent = hash || '-';
    full.title = hash || '';
  }
  const copyBtn=$('key-detail-copy');
  if(copyBtn){
    const disabled = !hash || hash==='unknown';
    copyBtn.disabled = disabled;
    copyBtn.setAttribute('aria-disabled', disabled?'true':'false');
  }
  const status=$('key-detail-copy-status');
  if(status) status.textContent = '';
}

function renderKeySummary(row, activity) {
  const req=Number(row.request_count||0);
  const fail=Number(row.failed_count||0);
  const rate = row.failure_rate!=null ? Number(row.failure_rate) : (req>0?fail/req:0);
  setText('kd-requests', fmtFull(req));
  setText('kd-requests-detail', t('metric.failed_count',{count:fmtFull(fail)}));
  setText('kd-failure', (rate*100).toFixed(1)+'%');
  setText('kd-failure-detail', t('metric.failed_of_total',{failed:fmtFull(fail),total:fmtFull(req)}));
  setText('kd-tokens', fmtNum(row.total_tokens||0));
  setText('kd-tokens-detail', t('metric.token_mix',{
    input:fmtNum(row.input_tokens||0),
    output:fmtNum(row.output_tokens||0),
    cached:fmtNum(row.cached_tokens||0),
    reasoning:fmtNum(row.reasoning_tokens||0)
  }) + (Number(row.cache_creation_tokens||0)?` / ${t('metric.cache_creation',{count:fmtNum(row.cache_creation_tokens||0)})}`:''));
  setText('kd-cost', fmtCostDisplay(row));
  const reasons=partialReasonsList(row);
  const costDetailParts=[t('cost.state.'+costStateOf(row))];
  if(Number(row.unpriced_models||0)>0) costDetailParts.push(t('metric.unpriced_models',{count:fmtFull(row.unpriced_models||0)}));
  if(reasons.length) costDetailParts.push(reasons.map(r=>t('cost.reason.'+r)!==('cost.reason.'+r)?t('cost.reason.'+r):r).join(', '));
  setText('kd-cost-detail', costDetailParts.join(' · '));
  const avgLat=fmtLat(row.avg_latency_ms||0);
  const avgTtfb=fmtLat(row.avg_ttfb_ms||0);
  const p95Lat=activity ? fmtLat(activity.p95_latency_ms||0) : '-';
  const p95Ttfb=activity ? fmtLat(activity.p95_ttfb_ms||0) : '-';
  setText('kd-latency', `${avgLat} / ${avgTtfb}`);
  setText('kd-latency-detail', t('key_detail.latency_detail',{avg_lat:avgLat,avg_ttfb:avgTtfb,p95_lat:p95Lat,p95_ttfb:p95Ttfb}));
  setText('kd-latest', fmtTime(row.latest_seen_at));
  setText('kd-models-detail', t('key_detail.model_count',{count:fmtFull(row.model_count||0)}));

  const conf=confidenceCounts(row);
  const confEl=$('kd-confidence');
  if(confEl){
    const parts=[
      ['observed',conf.observed],
      ['side_channel',conf.side_channel],
      ['request_only',conf.request_only],
      ['missing_usage',conf.missing_usage],
      ['unsupported',conf.unsupported],
      ['conflict',conf.conflict]
    ].filter(([,n])=>n>0);
    if(!parts.length){
      confEl.innerHTML=`<span class="key-chip">${esc(t('key_detail.confidence_empty'))}</span>`;
    } else {
      confEl.innerHTML=parts.map(([k,n])=>`<span class="key-chip"><span class="key-chip-label">${esc(t('confidence.'+k))}</span><span class="mono">${esc(fmtFull(n))}</span></span>`).join('');
    }
  }
  const reasonsEl=$('kd-partial-reasons');
  if(reasonsEl){
    if(costStateOf(row)==='complete' && !reasons.length){
      reasonsEl.innerHTML=`<span class="key-chip ok">${esc(t('cost.no_partial_reasons'))}</span>`;
    } else if(!reasons.length && costStateOf(row)==='unavailable'){
      reasonsEl.innerHTML=`<span class="key-chip err">${esc(t('cost.state.unavailable'))}</span>`;
    } else if(!reasons.length){
      reasonsEl.innerHTML=`<span class="key-chip">${esc(t('cost.no_partial_reasons'))}</span>`;
    } else {
      reasonsEl.innerHTML=reasons.map(r=>{
        const label=t('cost.reason.'+r);
        return `<span class="key-chip warn">${esc(label===('cost.reason.'+r)?r:label)}</span>`;
      }).join('');
    }
  }
}

function renderKeyModels(rows, err) {
  const tbody=$('kd-models-table');
  if(!tbody) return;
  if(err){
    tbody.innerHTML=errorRow(6, err.message||String(err));
    setText('kd-models-summary', t('key_detail.section_unavailable'));
    return;
  }
  const list=Array.isArray(rows)?rows:[];
  if(!list.length){
    tbody.innerHTML=emptyRow(6,t('state.no_model_data'),t('state.model_hint'));
    setText('kd-models-summary', t('summary.zero_models'));
    return;
  }
  const unpriced=list.filter(r=>costStateOf(r)!=='complete').length;
  setText('kd-models-summary', t('summary.models',{count:list.length,unknown:unpriced}));
  tbody.innerHTML=list.map(r=>{
    return `<tr>
      <td><div class="model-name" title="${esc(modelName(r.model))}">${esc(modelName(r.model))}</div></td>
      <td class="numeric mono">${fmtFull(r.request_count||0)}</td>
      <td class="numeric mono">${fmtFull(r.failed_count||0)}</td>
      <td class="numeric mono">${fmtNum(r.total_tokens||0)}</td>
      <td class="numeric mono">${esc(fmtCostDisplay(r))}</td>
      <td>${costStateBadgeHTML(r)}</td>
    </tr>`;
  }).join('');
}

function renderKeyIssues(data, err) {
  const list=$('kd-issues-list');
  if(!list) return;
  if(err){
    list.innerHTML=`<div class="empty-state error-text">${esc(err.message||String(err))}</div>`;
    setText('kd-issues-summary', t('key_detail.section_unavailable'));
    return;
  }
  const items=Array.isArray(data && data.items)?data.items:[];
  const partial=!!(data && data.partial);
  const source=data && data.sources ? data.sources.request_usage : '';
  const sourceLabel=issueSourceStatusLabel(source);
  const sourceNote=sourceLabel?t('key_detail.issue_source',{source:sourceLabel}):'';
  setText('kd-issues-summary', items.length
    ? t('key_detail.issues_summary',{count:fmtFull(items.length)})+(partial?` \u00b7 ${t('status.partial')}`:'')+(sourceNote?` \u00b7 ${sourceNote}`:'')
    : t('issues.empty'));
  if(!items.length){
    list.innerHTML=`<div class="empty-state"><strong>${esc(t('issues.empty'))}</strong><span>${esc(t('key_detail.issues_empty_detail'))}</span></div>`;
    return;
  }
  list.innerHTML=items.map(item=>{
    const sev=item.severity||'info';
    const label=issueClassLabel(item.class, item.label||item.class);
    const bits=[item.error_code||item.error_type, item.model?modelName(item.model):'', item.endpoint||'', item.request_id||''].filter(Boolean);
    return `<div class="key-issue-item">
      <span class="issue-sev ${esc(sev)}"></span>
      <div class="key-issue-body">
        <div class="key-issue-top">
          <span class="issue-label">${esc(label)}</span>
          <span class="issue-count mono">${esc(fmtFull(item.count||0))}</span>
        </div>
        <div class="key-issue-meta">${esc([fmtShort(item.latest_at), bits.join(' / ')].filter(Boolean).join(' \u00b7 ')||'-')}</div>
      </div>
    </div>`;
  }).join('');
}

function renderKeyRequests(rows, err) {
  const tbody=$('kd-requests-table');
  if(!tbody) return;
  if(err){
    tbody.innerHTML=errorRow(8, err.message||String(err));
    setText('kd-requests-summary', t('key_detail.section_unavailable'));
    return;
  }
  const list=Array.isArray(rows)?rows:[];
  setText('kd-requests-summary', t('key_detail.requests_summary',{count:fmtFull(list.length)}));
  if(!list.length){
    tbody.innerHTML=emptyRow(8,t('state.no_matching_requests'),t('key_detail.requests_empty_detail'));
    return;
  }
  tbody.innerHTML=list.map(r=>{
    const sc=r.status<400?'ok':r.status<500?'warn':'err';
    const md=modelName(r.model_returned||r.model_requested||'unidentified');
    const rid=r.request_id?String(r.request_id):'-';
    const ridShort=rid.length>18?rid.slice(0,12)+'...':rid;
    return `<tr>
      <td class="mono">${esc(fmtTime(r.created_at))}</td>
      <td><span class="badge ${sc}">${esc(r.status)}</span></td>
      <td><div class="model-name" title="${esc(md)}">${esc(md)}</div></td>
      <td>${esc(r.endpoint||'-')}</td>
      <td class="numeric mono">${fmtNum(r.total_tokens||0)}</td>
      <td class="numeric mono">${fmtLat(r.latency_ms)}</td>
      <td class="numeric mono">${fmtLat(r.ttfb_ms)}</td>
      <td><code title="${esc(rid)}">${esc(ridShort)}</code></td>
    </tr>`;
  }).join('');
}

function keyUsageMeta(mode=keyDetailUsageMode) {
  if(mode==='tokens') return {
    mode, label:t('usage.mode.tokens'),
    value:r=>Number(r.total_tokens||0),
    fmt:fmtNum, fmtFull:fmtFull, empty:t('state.no_token_data')
  };
  if(mode==='requests') return {
    mode, label:t('usage.mode.requests'),
    value:r=>Number(r.count||0),
    fmt:fmtNum, fmtFull:fmtFull, empty:t('state.no_request_data')
  };
  return {
    mode:'cost', label:t('usage.mode.cost'),
    value:r=>{
      const state=costStateOf(r);
      if(state==='unavailable') return 0;
      return Number(r.cost||0);
    },
    fmt:fmtCost, fmtFull:fmtCost, empty:t('state.no_cost_data')
  };
}

function renderKeyTrend(rows, bucket, err) {
  const el=$('kd-trend-chart');
  if(!el) return;
  document.querySelectorAll('[data-key-usage-mode]').forEach(btn=>{
    const active=btn.dataset.keyUsageMode===keyDetailUsageMode;
    btn.classList.toggle('active', active);
    btn.setAttribute('aria-selected', active?'true':'false');
  });
  if(err){
    el.innerHTML=`<div class="empty-state error-text">${esc(err.message||String(err))}</div>`;
    setText('kd-trend-summary', t('key_detail.section_unavailable'));
    setText('kd-trend-left','-');
    setText('kd-trend-right','-');
    return;
  }
  const list=Array.isArray(rows)?rows:[];
  const meta=keyUsageMeta();
  if(!list.length){
    el.innerHTML=emptyChart(meta.empty);
    setText('kd-trend-summary','');
    setText('kd-trend-left','-');
    setText('kd-trend-right','-');
    return;
  }
  const dims=chartDims(el);
  const pW=dims.w-dims.l-dims.r, pH=dims.h-dims.t-dims.b;
  const values=list.map(meta.value);
  const maxV=Math.max(...values,1);
  const total=values.reduce((s,v)=>s+v,0);
  const totalLabel=fmtKeyTrendTotal(list, meta, total);
  const slot=pW/list.length;
  const barW=Math.max(2,Math.min(28,slot*.6));
  const yFor=v=>dims.h-dims.b-pH*Number(v||0)/maxV;
  const bars=list.map((r,i)=>{
    const cx=dims.l+slot*(i+.5), x=cx-barW/2, v=values[i];
    const y=yFor(v), h=Math.max(0,dims.h-dims.b-y);
    const state=costStateOf(r);
    const partialMark = keyDetailUsageMode==='cost' && state!=='complete';
    const cls = partialMark ? 'chart-bar chart-bar-rect is-partial' : 'chart-bar chart-bar-rect';
    const pointLabel=fmtKeyTrendPoint(v, state);
    const tipRows=[
      [keyDetailUsageMode, meta.label, pointLabel!=null?pointLabel:meta.fmtFull(v)],
      ['tokens', t('tooltip.tokens'), fmtFull(r.total_tokens||0)]
    ];
    if(keyDetailUsageMode==='cost' && state!=='complete'){
      tipRows.push(['state', t('table.cost_state'), t('cost.state.'+state)]);
    }
    const tt=ttHtml(fmtShort(r.timestamp),'',tipRows);
    return `<g class="chart-hover-group" tabindex="0" data-tooltip="${esc(tt)}">
      <rect class="chart-hover-band" x="${(dims.l+slot*i).toFixed(1)}" y="${dims.t}" width="${slot.toFixed(1)}" height="${pH}"/>
      <line class="chart-hover-ruler" x1="${cx.toFixed(1)}" y1="${dims.t}" x2="${cx.toFixed(1)}" y2="${dims.h-dims.b}"/>
      ${h>0?`<rect class="${cls}" x="${x.toFixed(1)}" y="${y.toFixed(1)}" width="${barW.toFixed(1)}" height="${h.toFixed(1)}" ${partialMark?'opacity="0.55" stroke-dasharray="3 2"':''}/>`:''}
    </g>`;
  }).join('');
  el.innerHTML=`<svg viewBox="0 0 ${dims.w} ${dims.h}" role="img" aria-label="${esc(meta.label)}">
    ${gridLines(maxV,dims,meta.fmt)}
    <line stroke="var(--chart-grid)" stroke-width="1" x1="${dims.l}" y1="${dims.h-dims.b}" x2="${dims.w-dims.r}" y2="${dims.h-dims.b}"/>
    ${bars}
  </svg>`;
  attachTT(el);
  const partialBuckets = keyDetailUsageMode==='cost' ? list.filter(r=>costStateOf(r)!=='complete').length : 0;
  setText('kd-trend-summary', t('key_detail.trend_summary',{
    count:list.length,
    bucket:bucket||'-',
    value:totalLabel,
    partial:fmtFull(partialBuckets)
  }));
  setText('kd-trend-left', fmtShort(list[0].timestamp));
  setText('kd-trend-right', totalLabel);
}

async function loadKeyDetail() {
  if(!selectedKeyHash){
    hideKeyDetail();
    return;
  }
  showKeyDetailShell();
  const gen = ++keyDetailGeneration;
  if(keyDetailAbort){
    try { keyDetailAbort.abort(); } catch (_) {}
  }
  const controller = (typeof AbortController !== 'undefined') ? new AbortController() : null;
  keyDetailAbort = controller;
  const signal = controller ? controller.signal : undefined;
  const hash = selectedKeyHash;
  const range = getRange();
  const bucket = bucketFor(range);
  const keyParam = 'key_hash='+encodeURIComponent(hash);

  const row = (latestKeysRows||[]).find(r=>String(r.key_hash||'')===hash) || null;
  const emptyEl=$('key-detail-empty');
  const bodyEl=$('key-detail-body');

  if(!row){
    renderKeyDetailHeader({
      key_hash:hash,
      label:selectedKeySnapshot && selectedKeySnapshot.label ? selectedKeySnapshot.label : ''
    }, 'empty');
    setKeyDetailEmptyState('empty');
    // still clear stale section bodies
    lastKeyTSRows=[]; lastKeyTSBucket='';
    return;
  }

  selectedKeySnapshot = row;
  renderKeyDetailHeader(row);
  setKeyDetailEmptyState('hide');
  if(bodyEl){ bodyEl.classList.remove('hidden'); bodyEl.hidden=false; }
  // provisional summary before scoped fetches complete
  renderKeySummary(row, null);
  setText('kd-models-summary', t('state.loading'));
  setText('kd-issues-summary', t('state.loading'));
  setText('kd-requests-summary', t('state.loading'));
  setText('kd-trend-summary', t('state.loading'));
  const modelsBody=$('kd-models-table'); if(modelsBody) modelsBody.innerHTML=emptyRow(6,t('state.loading'));
  const reqBody=$('kd-requests-table'); if(reqBody) reqBody.innerHTML=emptyRow(8,t('state.loading'));
  const issuesList=$('kd-issues-list'); if(issuesList) issuesList.innerHTML=`<div class="empty-state">${esc(t('state.loading'))}</div>`;
  const trendEl=$('kd-trend-chart'); if(trendEl) trendEl.innerHTML=`<div class="empty-state">${esc(t('state.loading'))}</div>`;

  const tasks = {
    models: fetchJSONSignal('models?range='+encodeURIComponent(range)+'&'+keyParam, signal),
    timeseries: fetchJSONSignal('timeseries?range='+encodeURIComponent(range)+'&bucket='+encodeURIComponent(bucket)+'&'+keyParam, signal),
    activity: fetchJSONSignal('activity?range='+encodeURIComponent(range)+'&'+keyParam, signal),
    issues: fetchJSONSignal('issues?range='+encodeURIComponent(range)+'&limit=20&'+keyParam, signal),
    requests: fetchJSONSignal('requests?range='+encodeURIComponent(range)+'&limit=100&'+keyParam, signal)
  };
  const entries = Object.entries(tasks);
  const settled = await Promise.all(entries.map(async ([name, promise])=>{
    try { return [name, {ok:true, value:await promise}]; }
    catch(err){ return [name, {ok:false, error:err}]; }
  }));
  if(gen !== keyDetailGeneration || selectedKeyHash !== hash) return;

  const results = Object.fromEntries(settled);
  const activity = results.activity.ok ? results.activity.value : null;
  renderKeySummary(row, activity);
  if(!results.activity.ok && !isAbortError(results.activity.error)){
    setText('kd-latency-detail', t('key_detail.activity_unavailable'));
  }
  renderKeyModels(results.models.ok ? results.models.value : null, results.models.ok ? null : results.models.error);
  if(results.timeseries.ok){
    lastKeyTSRows = Array.isArray(results.timeseries.value)?results.timeseries.value:[];
    lastKeyTSBucket = bucket;
    renderKeyTrend(lastKeyTSRows, lastKeyTSBucket, null);
  } else if(!isAbortError(results.timeseries.error)){
    lastKeyTSRows=[]; lastKeyTSBucket='';
    renderKeyTrend([], bucket, results.timeseries.error);
  }
  renderKeyIssues(results.issues.ok ? results.issues.value : null, results.issues.ok ? null : results.issues.error);
  renderKeyRequests(results.requests.ok ? results.requests.value : null, results.requests.ok ? null : results.requests.error);
}


async function loadImages() {
  const [summaryResp, modelResp] = await Promise.all([
    fetchJSON('images/summary?range='+encodeURIComponent(getRange())),
    fetchJSON('images/models?range='+encodeURIComponent(getRange()))
  ]);
  const s = summaryResp && summaryResp.summary ? summaryResp.summary : {};
  const rows = Array.isArray(modelResp) ? modelResp : [];
  const totalTokens = Number(s.total_tokens || 0);
  const missing = Number(s.missing_usage_count || 0);
  setText('images-summary', t('summary.images', {requests:fmtFull(s.request_count||0), images:fmtFull(s.image_count||0)}));
  setText('image-count', fmtFull(s.image_count || 0));
  setText('image-count-detail', t('metric.partial_images', {partials:fmtFull(s.partial_image_count||0), inputs:fmtFull(s.input_image_count||0)}));
  setText('image-tokens', fmtNum(totalTokens));
  setText('image-tokens-detail', t('metric.image_token_mix', {
    text:fmtNum(s.input_text_tokens||0),
    image:fmtNum(s.input_image_tokens||0),
    cached:fmtNum(Number(s.cached_text_tokens||0)+Number(s.cached_image_tokens||0)+Number(s.cached_mixed_tokens||0)),
    output:fmtNum(s.output_image_tokens||0)
  }));
  setText('image-cost', summaryResp && summaryResp.cost_known ? fmtCost(summaryResp.cost) : (Number(summaryResp && summaryResp.cost || 0) > 0 ? fmtCost(summaryResp.cost) : '-'));
  setText('image-cost-detail', summaryResp && summaryResp.cost_known ? t('metric.full_estimate') : t('metric.partial_estimate'));
  setText('image-capture', missing ? fmtFull(missing) : t('badge.healthy'));
  setText('image-capture-detail', t('metric.capture_missing_usage', {count:fmtFull(missing)}));
  const tbody=$('images-models-table');
  if(!tbody) return;
  if(!rows.length) {
    tbody.innerHTML=emptyRow(9,t('state.no_image_data'),t('state.image_hint'));
    return;
  }
  tbody.innerHTML=rows.map(r=>{
    const missing=Number(r.missing_usage_count||0);
    const cap=missing?`<span class="badge warn">${esc(t('badge.skipped'))}</span>`:`<span class="badge ok">${esc(t('badge.captured'))}</span>`;
    return `<tr>
      <td><div class="model-name" title="${esc(modelName(r.model))}">${esc(modelName(r.model))}</div></td>
      <td>${esc(r.operation||'-')}</td>
      <td class="numeric mono">${fmtFull(r.request_count||0)}</td>
      <td class="numeric mono">${fmtFull(r.image_count||0)}</td>
      <td class="numeric mono">${fmtNum(r.input_text_tokens||0)}</td>
      <td class="numeric mono">${fmtNum(r.input_image_tokens||0)}</td>
      <td class="numeric mono">${fmtNum(r.output_image_tokens||0)}</td>
      <td class="numeric mono">${r.cost_known?fmtCost(r.cost):'-'}</td>
      <td>${cap}</td>
    </tr>`;
  }).join('');
}

/* --- Requests ---------------------------------------------- */
async function loadRequests() {
  const p=new URLSearchParams({limit:'100',range:getRange()});
  const st=$('filter-status').value,md=$('filter-model').value,ep=$('filter-endpoint').value;
  if(st)p.set('status',st); if(md)p.set('model',md); if(ep)p.set('endpoint',ep);
  if(currentIssueClassFilter)p.set('error_class',currentIssueClassFilter);
  const data=await fetchJSON('requests?'+p.toString());
  let rows=Array.isArray(data)?data:[];
  if(currentIssueClassFilter) rows=rows.filter(r=>r.error_class===currentIssueClassFilter);
  const tbody=$('requests-table');
  if(!rows.length){tbody.innerHTML=emptyRow(11,t('state.no_matching_requests'),t('state.adjust_filters'));return;}
  tbody.innerHTML=rows.map(r=>{
    const sc=r.status<400?'ok':r.status<500?'warn':'err';
    const diag=errorDiagnosticLabel(r);
    const guide=diagnosticGuide(r.error_class||r.error||'',r.error_code||diag||'');
    const statusTitle=[diag&&`error:${diag}`,r.error_type&&`type:${r.error_type}`,r.error_param&&`param:${r.error_param}`,guide&&`next:${guide}`].filter(Boolean).join(' / ');
    const statusCell=`<span class="badge ${sc}" title="${esc(statusTitle)}">${esc(r.status)}</span>${diag?`<div class="request-error-code">${esc(diag)}</div>`:''}`;
    const md2=modelName(r.model_returned||r.model_requested||'unidentified');
    const sourceBits=[r.model_returned_source&&`model:${r.model_returned_source}`,r.usage_source&&`usage:${r.usage_source}`,r.terminal_event&&`terminal:${r.terminal_event}`,r.side_usage_event_id&&`side:${r.side_usage_event_id}`].filter(Boolean);
    const capTitle=[r.capture_reason&&`reason:${r.capture_reason}`,r.terminal_reason&&`terminal_reason:${r.terminal_reason}`].concat(sourceBits).filter(Boolean).join(' / ');
    const cap=r.capture_outcome==='captured'?`<span class="badge ok" title="${esc(capTitle)}">${esc(t('badge.captured'))}</span>`:r.capture_outcome==='failed'?`<span class="badge err" title="${esc(capTitle)}">${esc(t('badge.failed'))}</span>`:r.capture_outcome==='skipped'?`<span class="badge warn" title="${esc(capTitle)}">${esc(t('badge.skipped'))}</span>`:`<span class="badge neutral" title="${esc(capTitle)}">${esc(r.capture_reason||t('badge.not_recorded'))}</span>`;
    const ep2=r.stream?`${esc(r.endpoint)} <span class="badge info">SSE</span>`:esc(r.endpoint);
    return `<tr><td class="mono">${esc(fmtTime(r.created_at))}</td><td>${statusCell}</td><td>${ep2}</td><td><div class="model-name" title="${esc(sourceBits.join(' / ')||md2)}">${esc(md2)}</div>${sourceBits.length?`<div class="model-source">${esc(sourceBits.slice(0,2).join(' / '))}</div>`:''}</td><td class="numeric mono">${fmtNum(r.input_tokens)}</td><td class="numeric mono">${fmtNum(r.output_tokens)}</td><td class="numeric mono">${fmtNum(r.total_tokens)}</td><td class="numeric mono">${fmtLat(r.ttfb_ms)}</td><td class="numeric mono">${fmtLat(r.latency_ms)}</td><td>${cap}</td><td><code>${esc(shortHash(r.api_key_hash))}</code></td></tr>`;
  }).join('');
}

/* --- Timeseries -------------------------------------------- */
async function loadTimeseries() {
  const range=getRange(), bucket=bucketFor(range);
  const data=await fetchJSON('timeseries?range='+encodeURIComponent(range)+'&bucket='+encodeURIComponent(bucket));
  const rows=Array.isArray(data)?data:[];
  lastTSRows=rows; lastTSBucket=bucket;
  renderUsagePanel();
}

/* --- Chart: shared ----------------------------------------- */
function chartDims(container) {
  const rect=container.getBoundingClientRect();
  const w=Math.max(300,Math.round(rect.width||800)), h=Math.max(160,Math.round(rect.height||240));
  return {w,h,l:48,r:12,t:14,b:24};
}
function gridLines(maxVal,dims,format=fmtNum) {
  const pH=dims.h-dims.t-dims.b;
  return [0,.5,1].map(f=>{
    const val=maxVal*f, y=dims.h-dims.b-pH*f;
    return `<line class="chart-grid-line" x1="${dims.l}" y1="${y.toFixed(1)}" x2="${dims.w-dims.r}" y2="${y.toFixed(1)}"/>
      <text class="chart-axis-text" x="${dims.l-6}" y="${(y+3).toFixed(1)}">${esc(format(val))}</text>`;
  }).join('');
}
function emptyChart(title) { return `<div class="empty-state"><strong>${esc(title)}</strong></div>`; }

/* --- Usage mode panel -------------------------------------- */
const modelColors = ['var(--chart-1)','var(--chart-2)','var(--chart-3)','var(--chart-4)','var(--chart-5)','var(--chart-6)','var(--chart-7)','var(--chart-8)','var(--chart-9)'];

function usageMeta(mode=currentUsageMode) {
  if(mode==='tokens') return {
    mode, label:t('usage.mode.tokens'), tooltip:t('tooltip.tokens'),
    value:r=>Number(r.total_tokens||0), modelValue:r=>Number(r.total_tokens||0),
    fmt:fmtNum, fmtFull:fmtFull, empty:t('state.no_token_data')
  };
  if(mode==='requests') return {
    mode, label:t('usage.mode.requests'), tooltip:t('tooltip.requests'),
    value:r=>Number(r.count||0), modelValue:r=>Number(r.request_count||0),
    fmt:fmtNum, fmtFull:fmtFull, empty:t('state.no_request_data')
  };
  return {
    mode:'cost', label:t('usage.mode.cost'), tooltip:t('usage.mode.cost'),
    value:r=>Number(r.cost||0), modelValue:r=>r.cost_known?Number(r.cost||0):0,
    fmt:fmtCost, fmtFull:fmtCost, empty:t('state.no_cost_data')
  };
}

function setUsageMode(mode) {
  if(mode!=='cost'&&mode!=='tokens'&&mode!=='requests') return;
  currentUsageMode=mode;
  try { localStorage.setItem(USAGE_MODE_KEY, mode); } catch (_) {}
  renderUsagePanel();
}

function updateUsageTabs() {
  document.querySelectorAll('[data-usage-mode]').forEach(btn=>{
    const active=btn.dataset.usageMode===currentUsageMode;
    btn.classList.toggle('active',active);
    btn.setAttribute('aria-selected',active?'true':'false');
  });
}

function renderUsagePanel() {
  updateUsageTabs();
  renderUsageSummary();
  renderUsageTrend(lastTSRows,lastTSBucket);
  renderModelDistribution();
}

function renderUsageSummary() {
  const meta=usageMeta();
  const selected=latestOverview&&latestOverview.selected&&latestOverview.selected.data?latestOverview.selected.data:{};
  if(currentUsageMode==='tokens') {
    const toks=Number(selected.total_tokens||0), inp=Number(selected.total_input_tokens||0), out=Number(selected.total_output_tokens||0);
    const cached=Number(selected.total_cached_tokens||0), reas=Number(selected.total_reasoning_tokens||0);
    setText('usage-total-value',fmtNum(toks));
    setText('usage-total-sub',inp||out?t('metric.token_mix',{input:fmtNum(inp),output:fmtNum(out),cached:fmtNum(cached),reasoning:fmtNum(reas)}):t('metric.total_token_count',{count:fmtFull(toks)}));
    return;
  }
  if(currentUsageMode==='requests') {
    const recent=latestOverview&&latestOverview.recent_1h&&latestOverview.recent_1h.data?latestOverview.recent_1h.data:{};
    setText('usage-total-value',fmtNum(selected.total_requests||0));
    setText('usage-total-sub',t('metric.recent_failed',{count:fmtFull(recent.failed_requests||0)}));
    return;
  }
  const partial=latestOverview&&latestOverview.cost&&latestOverview.cost.data&&latestOverview.cost.data.partial;
  const unpriced=latestOverview&&latestOverview.cost&&latestOverview.cost.data?Number(latestOverview.cost.data.unpriced_models||0):0;
  setText('usage-total-value',meta.fmt(selected.total_cost||0));
  setText('usage-total-sub',partial?t('metric.partial_estimate')+(unpriced?' / '+t('metric.unpriced_models',{count:fmtFull(unpriced)}):''):t('metric.full_estimate'));
}

function renderUsageLegend(mode=currentUsageMode) {
  const el=$('usage-legend'); if(!el) return;
  if(mode==='tokens') {
    el.innerHTML=[
      ['dot-cyan','legend.cached_input'],
      ['dot-accent','legend.uncached_input'],
      ['dot-green','legend.output'],
      ['dot-violet','legend.reasoning']
    ].map(([cls,key])=>`<span class="legend-item"><span class="legend-dot ${cls}"></span><span>${esc(t(key))}</span></span>`).join('');
    return;
  }
  if(mode==='requests') {
    el.innerHTML=[
      ['dot-accent','legend.requests'],
      ['dot-red','legend.failed']
    ].map(([cls,key])=>`<span class="legend-item"><span class="legend-dot ${cls}"></span><span>${esc(t(key))}</span></span>`).join('');
    return;
  }
  el.innerHTML=`<span class="legend-item"><span class="legend-dot dot-accent"></span><span>${esc(t('usage.mode.cost'))}</span></span>`;
}

function renderUsageTrend(rows,bucket) {
  const el=$('usage-trend-chart'); if(!el) return;
  renderUsageLegend(currentUsageMode);
  if(currentUsageMode==='tokens') renderTokenTrend(el,rows,bucket);
  else if(currentUsageMode==='requests') renderRequestTrend(el,rows,bucket,usageMeta());
  else renderSingleTrend(el,rows,bucket,usageMeta());
}

function renderSingleTrend(el,rows,bucket,meta) {
  if(!rows.length){el.innerHTML=emptyChart(meta.empty);setText('usage-chart-summary','');setText('usage-chart-left','-');setText('usage-chart-right','-');return;}
  const dims=chartDims(el);
  const pW=dims.w-dims.l-dims.r, pH=dims.h-dims.t-dims.b;
  const values=rows.map(meta.value);
  const maxV=Math.max(...values,1);
  const total=values.reduce((s,v)=>s+v,0);
  const slot=pW/rows.length;
  const barW=Math.max(2,Math.min(28,slot*.6));
  const yFor=v=>dims.h-dims.b-pH*Number(v||0)/maxV;
  const bars=rows.map((r,i)=>{
    const cx=dims.l+slot*(i+.5), x=cx-barW/2, v=values[i];
    const y=yFor(v), h=Math.max(0,dims.h-dims.b-y);
    const mainKind='cost';
    const tt=ttHtml(fmtShort(r.timestamp),'',[
      [mainKind,meta.tooltip,meta.fmtFull(v)],
      ['tokens',t('tooltip.tokens'),fmtFull(r.total_tokens||0)]
    ]);
    return `<g class="chart-hover-group" tabindex="0" data-tooltip="${esc(tt)}">
      <rect class="chart-hover-band" x="${(dims.l+slot*i).toFixed(1)}" y="${dims.t}" width="${slot.toFixed(1)}" height="${pH}"/>
      <line class="chart-hover-ruler" x1="${cx.toFixed(1)}" y1="${dims.t}" x2="${cx.toFixed(1)}" y2="${dims.h-dims.b}"/>
      ${h>0?`<rect class="chart-bar ${mainKind} chart-bar-rect" x="${x.toFixed(1)}" y="${y.toFixed(1)}" width="${barW.toFixed(1)}" height="${h.toFixed(1)}"/>`:''}
    </g>`;
  }).join('');
  el.innerHTML=`<svg viewBox="0 0 ${dims.w} ${dims.h}" role="img" aria-label="${esc(meta.label)}">
    ${gridLines(maxV,dims,meta.fmt)}
    <line stroke="var(--chart-grid)" stroke-width="1" x1="${dims.l}" y1="${dims.h-dims.b}" x2="${dims.w-dims.r}" y2="${dims.h-dims.b}"/>
    ${bars}
  </svg>`;
  attachTT(el);
  const summaryKey=currentUsageMode==='cost'?'summary.cost_chart':currentUsageMode==='requests'?'summary.requests_chart':'summary.tokens_chart';
  setText('usage-chart-summary',currentUsageMode==='requests'
    ?t(summaryKey,{count:rows.length,bucket,peak:fmtFull(maxV)})
    :t(summaryKey,{count:rows.length,bucket,value:meta.fmtFull(total),peak:meta.fmtFull(maxV)}));
  setText('usage-chart-left',fmtShort(rows[0].timestamp));
  setText('usage-chart-right',meta.fmtFull(total));
}

function renderRequestTrend(el,rows,bucket,meta) {
  if(!rows.length){el.innerHTML=emptyChart(meta.empty);setText('usage-chart-summary','');setText('usage-chart-left','-');setText('usage-chart-right','-');return;}
  const dims=chartDims(el);
  const pW=dims.w-dims.l-dims.r, pH=dims.h-dims.t-dims.b;
  const values=rows.map(meta.value), failed=rows.map(r=>Number(r.failed_count||0));
  const maxV=Math.max(...values,...failed,1);
  const total=values.reduce((s,v)=>s+v,0);
  const xFor=i=>rows.length===1?dims.l+pW/2:dims.l+pW*i/(rows.length-1);
  const yFor=v=>dims.h-dims.b-pH*Number(v||0)/maxV;
  const linePath=pointsToSmoothPath(values.map((v,i)=>[xFor(i),yFor(v)]));
  const failPath=pointsToSmoothPath(failed.map((v,i)=>[xFor(i),yFor(v)]));
  const dots=rows.map((r,i)=>`<g class="chart-hover-group" tabindex="0" data-tooltip="${esc(reqTooltip(r))}">
    <rect class="chart-hover-band" x="${(dims.l+pW*i/rows.length).toFixed(1)}" y="${dims.t}" width="${(pW/rows.length).toFixed(1)}" height="${pH}"/>
    <line class="chart-hover-ruler" x1="${xFor(i).toFixed(1)}" y1="${dims.t}" x2="${xFor(i).toFixed(1)}" y2="${dims.h-dims.b}"/>
    <circle class="chart-point" cx="${xFor(i).toFixed(1)}" cy="${yFor(values[i]).toFixed(1)}" r="3"/>
  </g>`).join('');
  el.innerHTML=`<svg viewBox="0 0 ${dims.w} ${dims.h}" role="img" aria-label="${esc(meta.label)}">
    ${gridLines(maxV,dims,meta.fmt)}
    <line stroke="var(--chart-grid)" stroke-width="1" x1="${dims.l}" y1="${dims.h-dims.b}" x2="${dims.w-dims.r}" y2="${dims.h-dims.b}"/>
    <path class="chart-fill-area" d="${linePath} L ${xFor(rows.length-1).toFixed(1)} ${dims.h-dims.b} L ${xFor(0).toFixed(1)} ${dims.h-dims.b} Z"/>
    <path class="chart-line requests" d="${linePath}"/>
    <path class="chart-line failed" d="${failPath}"/>
    ${dots}
  </svg>`;
  attachTT(el);
  setText('usage-chart-summary',t('summary.requests_chart',{count:rows.length,bucket,peak:fmtFull(maxV)}));
  setText('usage-chart-left',fmtShort(rows[0].timestamp));
  setText('usage-chart-right',fmtFull(total));
}

function pointsToSmoothPath(points) {
  if(!points.length) return '';
  if(points.length===1) return `M ${points[0][0].toFixed(1)} ${points[0][1].toFixed(1)}`;
  let d=`M ${points[0][0].toFixed(1)} ${points[0][1].toFixed(1)}`;
  for(let i=1;i<points.length;i++){
    const [x0,y0]=points[i-1], [x1,y1]=points[i];
    const mx=(x0+x1)/2;
    d+=` C ${mx.toFixed(1)} ${y0.toFixed(1)}, ${mx.toFixed(1)} ${y1.toFixed(1)}, ${x1.toFixed(1)} ${y1.toFixed(1)}`;
  }
  return d;
}

function renderTokenTrend(el,rows,bucket) {
  if(!rows.length){el.innerHTML=emptyChart(t('state.no_token_data'));setText('usage-chart-summary','');setText('usage-chart-left','-');setText('usage-chart-right','-');return;}
  const dims=chartDims(el);
  const pW=dims.w-dims.l-dims.r, pH=dims.h-dims.t-dims.b;
  const totals=rows.map(r=>{
    const reas=Number(r.reasoning_tokens||0),rawOut=Number(r.output_tokens||0);
    const cached=Number(r.cached_tokens||0),uncached=Math.max(0,Number(r.input_tokens||0)-Number(r.cached_tokens||0)),output=Math.max(0,rawOut-reas);
    const stack=cached+uncached+output+reas;
    return {cached,uncached,output,reasoning:reas,total:Math.max(Number(r.total_tokens||0),stack)};
  });
  const maxTotal=Math.max(...totals.map(r=>r.total),1);
  const totalTok=totals.reduce((s,r)=>s+r.total,0);
  const slot=pW/rows.length;
  const barW=Math.max(2,Math.min(24,slot*.6));
  const bars=rows.map((r,i)=>{
    const cx=dims.l+slot*(i+.5), x=cx-barW/2;
    let cursor=dims.h-dims.b;
    let parts=[['cached',totals[i].cached],['uncached',totals[i].uncached],['output',totals[i].output],['reasoning',totals[i].reasoning]].filter(([,v])=>v>0);
    let hidden=0;
    const visibleParts=[];
    parts.forEach((p)=>{ if(pH*p[1]/maxTotal<.5) hidden+=p[1]; else visibleParts.push(p); });
    parts=visibleParts;
    if(hidden>0 && parts.length){
      const largest=parts.reduce((best,p,idx)=>p[1]>parts[best][1]?idx:best,0);
      parts[largest]=[parts[largest][0],parts[largest][1]+hidden];
    } else if(hidden>0) {
      const fallback=[['uncached',hidden]];
      parts=fallback;
    }
    const rects=parts.map(([kind,val])=>{
      if(val<=0)return'';
      const h=pH*val/maxTotal; if(h<.5)return'';
      cursor-=h;
      return `<rect class="chart-bar ${kind} chart-bar-rect" x="${x.toFixed(1)}" y="${cursor.toFixed(1)}" width="${barW.toFixed(1)}" height="${h.toFixed(1)}"/>`;
    }).join('');
    return `<g class="chart-hover-group" tabindex="0" data-tooltip="${esc(tokTooltip(r,totals[i]))}">
      <rect class="chart-hover-band" x="${(dims.l+slot*i).toFixed(1)}" y="${dims.t}" width="${slot.toFixed(1)}" height="${pH}"/>
      <line class="chart-hover-ruler" x1="${cx.toFixed(1)}" y1="${dims.t}" x2="${cx.toFixed(1)}" y2="${dims.h-dims.b}"/>
      ${rects}
    </g>`;
  }).join('');
  el.innerHTML=`<svg viewBox="0 0 ${dims.w} ${dims.h}" role="img" aria-label="${esc(t('panel.tokens'))}">
    ${gridLines(maxTotal,dims)}
    <line stroke="var(--chart-grid)" stroke-width="1" x1="${dims.l}" y1="${dims.h-dims.b}" x2="${dims.w-dims.r}" y2="${dims.h-dims.b}"/>
    ${bars}
  </svg>`;
  attachTT(el);
  setText('usage-chart-summary',t('summary.tokens_chart',{count:rows.length,bucket,tokens:fmtFull(totalTok)}));
  setText('usage-chart-left',fmtShort(rows[0].timestamp));
  setText('usage-chart-right',t('summary.peak_tokens',{tokens:fmtFull(Math.max(...totals.map(r=>r.total)))}));
}

function renderModelDistribution() {
  const chart=$('model-distribution-chart'), list=$('model-distribution-list');
  if(!chart||!list) return;
  const meta=usageMeta();
  const rows=currentModels.map(r=>({...r,_value:meta.modelValue(r)})).filter(r=>r._value>0);
  rows.sort((a,b)=>b._value-a._value);
  const total=rows.reduce((s,r)=>s+r._value,0);
  if(!rows.length||total<=0) {
    chart.innerHTML=emptyChart(meta.mode==='cost'?t('state.no_cost_data'):t('state.no_model_data'));
    list.innerHTML='';
    return;
  }
  const display=rows.slice(0,5);
  const rest=rows.slice(5).reduce((s,r)=>s+r._value,0);
  if(rest>0) display.push({model:t('model.other'),_value:rest,cost_known:true});
  chart.innerHTML=donutSvg(display,total,meta);
  attachTT(chart);
  list.innerHTML=display.map((r,i)=>{
    const color=modelColors[i%modelColors.length];
    return `<div class="dist-row" title="${esc(modelName(r.model))}">
      <span class="dist-swatch" style="--color:${color}"></span>
      <span class="dist-name">${esc(modelName(r.model))}</span>
    </div>`;
  }).join('')+(meta.mode==='cost'&&currentModels.some(r=>!r.cost_known)?`<div class="dist-note">${esc(t('model.unpriced_excluded'))}</div>`:'');
}

function donutTooltip(row,share,meta) {
  return `<div class="tt-title"><span>${esc(modelName(row.model))}</span><small>${esc(share.toFixed(1))}%</small></div>
    <div class="tt-row"><span><i class="tt-dot cost"></i>${esc(meta.label)}</span><strong>${esc(meta.fmtFull(row._value))}</strong></div>`;
}
function donutSvg(rows,total,meta) {
  const cx=180, cy=180, r=116, c=2*Math.PI*r;
  let offset=0;
  const rings=rows.map((row,i)=>{
    const share=row._value/total*100;
    const len=c*(row._value/total);
    const gap=rows.length>1?3.2:0;
    const dash=Math.max(0,len-gap);
    const ring=`<circle class="distribution-slice chart-hover-group" tabindex="0" cx="${cx}" cy="${cy}" r="${r}" stroke="${modelColors[i%modelColors.length]}" stroke-dasharray="${dash.toFixed(2)} ${(c-dash).toFixed(2)}" stroke-dashoffset="${(-offset).toFixed(2)}" data-tooltip="${esc(donutTooltip(row,share,meta))}"/>`;
    offset+=len;
    return ring;
  }).join('');
  return `<svg viewBox="0 0 360 360" role="img" aria-label="${esc(t('usage.distribution'))}">
    <g transform="rotate(-90 ${cx} ${cy})">
      <circle class="distribution-ring-track" cx="${cx}" cy="${cy}" r="${r}"/>
      ${rings}
    </g>
  </svg>`;
}

function reqTooltip(r) {
  const c=Number(r.count||0),f=Number(r.failed_count||0);
  return ttHtml(fmtShort(r.timestamp),'',[
    ['requests',t('tooltip.requests'),fmtFull(c)],
    ['failed',t('tooltip.failed'),fmtFull(f)],
    ['requests',t('tooltip.avg_ttfb'),fmtLat(r.avg_ttfb_ms)],
    ['requests',t('tooltip.avg_latency'),fmtLat(r.avg_latency_ms)],
    ['tokens',t('tooltip.tokens'),fmtFull(r.total_tokens)]
  ]);
}

function tokTooltip(row,vals) {
  return ttHtml(fmtShort(row.timestamp),t('tooltip.total_tokens',{value:fmtFull(row.total_tokens)}),[
    ['cached',t('tooltip.cached_input'),fmtFull(vals.cached)],
    ['uncached',t('tooltip.uncached_input'),fmtFull(vals.uncached)],
    ['output',t('tooltip.output'),fmtFull(vals.output)],
    ['reasoning',t('tooltip.reasoning'),fmtFull(vals.reasoning)]
  ]);
}

function ttHtml(title,meta,rows) {
  return `<div class="tt-title"><span>${esc(title)}</span>${meta?`<small>${esc(meta)}</small>`:''}</div>`+
    rows.map(([k,label,val])=>`<div class="tt-row"><span><i class="tt-dot ${k}"></i>${esc(label)}</span><strong>${esc(val)}</strong></div>`).join('');
}

/* --- Tooltip system ---------------------------------------- */
function attachTT(container) {
  container.querySelectorAll('.chart-hover-group').forEach(el=>{
    el.addEventListener('mouseenter', e=>activateHover(container,el,e));
    el.addEventListener('mousemove', e=>moveTT(e));
    el.addEventListener('mouseleave',()=>deactivateHover(container,el));
    el.addEventListener('focus', e=>activateHover(container,el,e));
    el.addEventListener('blur',()=>deactivateHover(container,el));
  });
}
function activateHover(c,el,e) {
  c.querySelectorAll('.chart-hover-group.active').forEach(a=>{if(a!==el)a.classList.remove('active');});
  c.classList.add('has-active'); el.classList.add('active');
  showTT(e,el.dataset.tooltip||el.dataset.tt);
}
function deactivateHover(c,el) {
  el.classList.remove('active');
  if(!c.querySelector('.chart-hover-group.active')) c.classList.remove('has-active');
  hideTT();
}
function showTT(e,html) {
  const tt=$('chart-tooltip'); tt.innerHTML=html||''; tt.classList.remove('hidden'); moveTT(e);
}
function moveTT(e) {
  const tt=$('chart-tooltip'); if(!tt||tt.classList.contains('hidden'))return;
  const pad=10, rect=tt.getBoundingClientRect();
  let x=(e.clientX||0)-rect.width/2, y=(e.clientY||0)-rect.height-12;
  if(y<pad)y=(e.clientY||0)+16;
  if(x+rect.width+pad>window.innerWidth)x=window.innerWidth-rect.width-pad;
  tt.style.left=Math.max(pad,x)+'px'; tt.style.top=Math.max(pad,y)+'px';
}
function hideTT() { const tt=$('chart-tooltip'); if(tt)tt.classList.add('hidden'); }

/* --- Issues ------------------------------------------------ */
async function loadIssues() {
  const data = await fetchJSON('issues?range='+encodeURIComponent(getRange())+'&limit=20');
  if(!data) return;
  const reqItems = Array.isArray(data.items)?data.items:[];
  const sysItems = data.system&&Array.isArray(data.system.items)?data.system.items.map(i=>({...i,system:true})):[];
  const items = reqItems.concat(sysItems);
  const sevRank={error:0,warning:1,info:2};
  items.sort((a,b)=>{
    const d=(sevRank[a.severity]??3)-(sevRank[b.severity]??3); if(d)return d;
    const at=Date.parse(a.latest_at||'')||0, bt=Date.parse(b.latest_at||'')||0;
    if(bt!==at)return bt-at; return Number(b.count||0)-Number(a.count||0);
  });
  latestIssueItems=items;
  setText('issues-summary',items.length?t('issues.summary',{count:fmtFull(items.length)}):t('issues.empty'));
  const state=$('issues-state'), overview=$('issues-overview'), list=$('issues-list');
  if(!items.length){
    state.classList.remove('hidden');
    state.innerHTML=`<div class="issues-empty"><strong>${esc(t('issues.empty'))}</strong><span>${esc(t('issues.empty_detail'))}</span></div>`;
    if(overview) overview.innerHTML='';
    list.innerHTML=`<div class="issue-class-placeholder">${esc(t('issues.no_issue_types'))}</div>`; return;
  }
  state.classList.add('hidden');
  const issueSevLabel=sev=>{const k='issues.severity.'+(sev||'info'),fb=I18N.en||{},d=I18N[currentLang]||fb;return d[k]||fb[k]||sev||'';};
  const countsBySeverity={error:0,warning:0,info:0};
  items.forEach(item=>{const sev=item.severity||'info'; countsBySeverity[sev]=(countsBySeverity[sev]||0)+Number(item.count||0);});
  if(selectedIssueSeverity && !countsBySeverity[selectedIssueSeverity]) selectedIssueSeverity='';
  const statHtml=['error','warning','info'].map(sev=>{
    const total=countsBySeverity[sev]||0;
    const active=selectedIssueSeverity===sev;
    return `<button class="issue-stat ${active?'active':''}" type="button" data-issue-severity="${sev}" ${total?'':'disabled'}>
      <div class="issue-stat-label">${esc(issueSevLabel(sev))}</div>
      <div class="issue-stat-value mono">${esc(fmtFull(total))}</div>
    </button>`;
  }).join('');
  if(overview) overview.innerHTML=`<div class="issue-summary-strip">${statHtml}</div>`;
  const renderClassPanel=()=>{
    if(!selectedIssueSeverity){
      list.innerHTML=`<div class="issue-class-placeholder">${esc(t('issues.select_severity_hint'))}</div>`;
      return;
    }
    const groups=new Map();
    items.filter(item=>(item.severity||'info')===selectedIssueSeverity).forEach(item=>{
      const cls=item.class||'unknown';
      const source=item.source_group||item.scope||'system';
      const key=source+'::'+cls;
      const g=groups.get(key)||{class:cls,source,label:issueClassLabel(cls,item.label),count:0,latestAt:'',messages:[],codes:new Set(),models:new Set(),rawModels:new Set(),endpoints:new Set(),statuses:new Set(),systemOnly:true};
      g.count+=Number(item.count||0);
      if((Date.parse(item.latest_at||'')||0)>(Date.parse(g.latestAt||'')||0)) g.latestAt=item.latest_at||g.latestAt;
      const diag=errorDiagnosticLabel(item);
      if(diag) g.codes.add(diag);
      if(item.message||item.error_code||item.error_type) g.messages.push(item.message||item.error_code||item.error_type);
      if(item.model) {
        g.models.add(modelName(item.model));
        g.rawModels.add(item.model);
      }
      if(item.endpoint) g.endpoints.add(item.endpoint);
      if(item.status) g.statuses.add(String(item.status));
      if(!item.system) g.systemOnly=false;
      groups.set(key,g);
    });
    const rows=[...groups.values()].sort((a,b)=>b.count-a.count);
    list.classList.remove('hidden');
    const issueGroupByKey=new Map();
    list.innerHTML=`<div class="issue-class-head">${esc(issueSevLabel(selectedIssueSeverity))}${esc(t('issues.class_breakdown_suffix'))}</div>
      <div class="issue-filter-grid">${rows.map(g=>{
        const issueKey=`${g.source}::${g.class}`;
        issueGroupByKey.set(issueKey,g);
        const bits=[...g.codes].slice(0,2).concat([...g.statuses].slice(0,1).map(s=>'HTTP '+s),[...g.models].slice(0,1),[...g.endpoints].slice(0,1)).filter(Boolean);
        const detail=[g.source,bits.join(' / ') || (g.systemOnly?t('issues.scope_process'):t('issues.no_message'))].filter(Boolean).join(' / ');
        const msg=(g.messages[0]&&!g.codes.has(g.messages[0]))?g.messages[0]:'';
        const guide=diagnosticGuide(g.class,[...g.codes][0]||'');
        const active=currentIssueClassFilter===g.class;
        const cardClass=g.systemOnly?'issue-filter-card':`issue-filter-card clickable ${active?'active':''}`;
        const attrs=g.systemOnly?'':` role="button" tabindex="0" data-issue-key="${esc(issueKey)}" data-issue-class="${esc(g.class)}"`;
        return `<div class="${cardClass}"${attrs}>
          <div class="issue-filter-top">
            <span class="issue-sev ${selectedIssueSeverity}"></span>
            <span class="issue-label">${esc(g.label)}</span>
            <span class="issue-count mono">${esc(fmtFull(g.count))}</span>
          </div>
          <div class="issue-detail">${esc(detail)}${msg?' - '+esc(msg):''}</div>
          ${guide?`<div class="issue-guide">${esc(guide)}</div>`:''}
          <div class="issue-filter-foot"><span class="mono">${esc(g.latestAt?fmtShort(g.latestAt):'-')}</span>${g.systemOnly?'':`<span>${esc(t('action.show_matching_requests'))}</span>`}</div>
        </div>`;
      }).join('')}</div>`;
    list.querySelectorAll('.issue-filter-card[data-issue-class]').forEach(row=>{
      const open=()=>showReqForIssueClass(row.dataset.issueClass, issueGroupByKey.get(row.dataset.issueKey));
      row.addEventListener('click',open);
      row.addEventListener('keydown',e=>{
        if(e.key==='Enter'||e.key===' '){e.preventDefault();open();}
      });
    });
  };
  if(overview) overview.querySelectorAll('[data-issue-severity]').forEach(card=>{
    const open=()=>{
      const sev=card.dataset.issueSeverity;
      selectedIssueSeverity=selectedIssueSeverity===sev?'':sev;
      currentIssueClassFilter='';
      closeRequestDetails();
      loadIssues();
    };
    card.addEventListener('click',open);
  });
  renderClassPanel();
}

/* --- Health / Diagnostics ---------------------------------- */
async function loadHealth() {
  const data=await fetchJSON('health');
  latestHealth=data;
  setText('queue-depth',fmtFull(data.queue_depth));
  setText('dropped-events',fmtFull(data.dropped_events));
  setText('parse-errors',fmtFull(data.parse_errors));
  setText('db-errors',fmtFull(data.db_write_errors));
  const hasIssue=Number(data.dropped_events||0)>0||Number(data.parse_errors||0)>0||Number(data.db_write_errors||0)>0;
  const hs=$('health-summary');
  if(hs) {
    if(data.capture_disabled||!data.metering_enabled) hs.innerHTML=`<span class="badge warn">${esc(t('badge.capture_off'))}</span>`;
    else if(hasIssue) hs.innerHTML=`<span class="badge warn">${esc(t('badge.attention'))}</span>`;
    else hs.innerHTML=`<span class="badge ok">${esc(t('badge.healthy'))}</span>`;
  }
}

async function loadQuota() {
  const data=await fetchJSON('quota');
  latestQuota=data;
  const phase=data.phase||'-';
  const items=(Array.isArray(data.items)?data.items:[]).slice().sort((a,b)=>{
    const pc=String(a.provider||'').localeCompare(String(b.provider||''));
    if(pc) return pc;
    const ac=String(a.credential_hash||a.auth_index_hash||a.label_hash||'').localeCompare(String(b.credential_hash||b.auth_index_hash||b.label_hash||''));
    if(ac) return ac;
    return quotaRowCompare(a,b);
  });
  const quotaItems=(Array.isArray(data.quota_items)?data.quota_items:(items.some(isCredentialHealthRow)?[]:items)).slice().sort(quotaRowCompare);
  const credentialRowsFromAPI=Array.isArray(data.credential_items)?data.credential_items:[];
  const credentialHealthMode=phase==='credential_health' && !data.full_quota_available && items.some(isCredentialHealthRow);
  const credentialItems=(credentialRowsFromAPI.length?credentialRowsFromAPI:(credentialHealthMode?items:[])).slice().sort(credentialHealthCompare);
  const groups=quotaGroups(quotaItems);
  if(quotaPage<1) quotaPage=1;
  const moduleStatus=data.module_status||'disabled';
  const moduleLabel=moduleStatusLabel(moduleStatus);
  const diagnostic=quotaDiagnosticText(data);
  setText('quota-state',moduleLabel);
  setText('quota-detail',[data.full_quota_available?t('obs.full_quota'):(credentialHealthMode?t('obs.credential_fallback'):phase),diagnostic].filter(Boolean).join(' / '));
  setText('observability-summary',t('obs.summary',{phase:moduleLabel,quota:credentialItems.length||groups.length}));
  if(!items.length){
    const empty = moduleStatus==='disabled'?t('state.quota_disabled'):moduleStatus==='unavailable'?t('state.quota_unavailable'):moduleStatus==='unsupported'?t('state.quota_unsupported'):t('state.no_quota_data');
    renderQuotaSummary([], diagnostic?[empty,diagnostic].join(' · '):empty);
    return;
  }
  if(credentialItems.length){
    renderCredentialHealthSummary(credentialItems,undefined,quotaItems);
    return;
  }
  renderQuotaSummary(groups);
}

async function loadObservability() {
  const data=await fetchJSON('observability');
  latestObservability=data;
  const side=data.side_channel||{}, cred=data.credential_health||{}, quota=data.quota||{}, capture=data.request_capture||{};
  setText('side-channel-state',side.enabled?(side.connected?t('obs.connected'):t('obs.disconnected')):t('obs.disabled'));
  setText('side-channel-detail',side.enabled?`${side.merge_mode||'-'} / ${side.last_error||t('obs.no_error')}`:'-');
  setText('credential-state',cred.enabled?t('obs.enabled'):t('obs.disabled'));
  setText('credential-detail',cred.enabled?t('obs.credential_counts',{warnings:fmtFull(cred.warning_count||0),unavailable:fmtFull(cred.unavailable_count||0),errors:fmtFull(cred.error_count||0)}):'-');
  const quotaStatus=quota.enabled?(quota.module_status||(quota.full_quota_available?'available':'partial')):'disabled';
  const quotaPhase=quota.full_quota_available?t('obs.full_quota'):(quota.credential_fallback?t('obs.credential_fallback'):(quota.phase||'-'));
  setText('quota-state',moduleStatusLabel(quotaStatus));
  const quotaDiag=quota.last_error?` / ${quota.last_error}`:'';
  setText('quota-detail',quota.enabled?`${quotaPhase} / ${t('obs.stale_errors',{stale:fmtFull(quota.stale_count||0),errors:fmtFull(quota.error_count||0)})}${quotaDiag}`:'-');
  setText('capture-state',fmtFull(Number(capture.captured_1h||0)));
  setText('capture-state-detail',t('obs.capture_counts',{skipped:fmtFull(capture.skipped_1h||0),failed:fmtFull(capture.failed_1h||0)}));
}

async function loadQuotaDiagnostics() {
  const data=await fetchJSON('quota/diagnostics?limit=50');
  latestQuotaDiagnostics=data;
  renderQuotaDiagnostics(data);
}

function renderQuotaDiagnostics(data) {
  const summary=$('quota-diagnostics-summary');
  const cards=$('quota-diagnostics-cards');
  const table=$('quota-diagnostics-table');
  if(!summary || !cards || !table) return;
  const cred=data&&data.credentials||{};
  const quota=data&&data.quota||{};
  const events=Array.isArray(data&&data.events)?data.events:[];
  const apiState=data&&data.quota_enabled?(data.api_call_available?t('quota_diag.api_reachable'):t('quota_diag.api_unreachable')):t('obs.disabled');
  summary.textContent=[
    apiState,
    t('quota_diag.credentials_summary',{count:fmtFull(cred.total||0),warning:fmtFull(cred.warning||0),errors:fmtFull((cred.errors||0)+(cred.unavailable||0)+(cred.disabled||0))}),
    t('quota_diag.events_summary',{count:fmtFull(events.length)})
  ].join(' / ');
  cards.innerHTML=[
    quotaDiagnosticCard(t('quota_diag.api_call'),apiState,data&&data.checked_at?fmtShort(data.checked_at):'-',data&&data.api_call_available?'ok':'warn'),
    quotaDiagnosticCard(t('quota_diag.credentials'),fmtFull(cred.total||0),t('quota_diag.credentials_detail',{ready:fmtFull(cred.ready||0),warning:fmtFull(cred.warning||0),errors:fmtFull((cred.errors||0)+(cred.unavailable||0)+(cred.disabled||0))}),Number((cred.errors||0)+(cred.unavailable||0)+(cred.disabled||0))>0?'warn':'ok'),
    quotaDiagnosticCard(t('quota_diag.quota_rows'),fmtFull(quota.total||0),t('quota_diag.quota_detail',{supported:fmtFull(quota.supported||0),unsupported:fmtFull(quota.unsupported||0),stale:fmtFull(quota.stale||0)}),Number((quota.unsupported||0)+(quota.stale||0)+(quota.errors||0))>0?'warn':'ok'),
    quotaDiagnosticCard(t('quota_diag.last_event'),events[0]?quotaDiagnosticEventLabel(events[0]):'-',events[0]?fmtShort(events[0].checked_at):'-',events[0]&&events[0].status==='error'?'err':'ok')
  ].join('');
  if(!events.length){
    table.innerHTML=`<tr><td colspan="9" class="empty-state">${esc(t('quota_diag.no_events'))}</td></tr>`;
    return;
  }
  table.innerHTML=events.map(row=>{
    const err=row.error_class||row.adapter_status||row.probe_error_class||'';
    return `<tr>
      <td class="mono">${esc(fmtShort(row.checked_at))}</td>
      <td>${esc(row.provider||'-')}</td>
      <td>${esc(row.phase||'-')}</td>
      <td><span class="badge ${statusBadgeClass(row.status)}">${esc(row.status||'-')}</span></td>
      <td>${esc(row.adapter_status||'-')}</td>
      <td class="numeric mono">${esc(row.probe_http_status?String(row.probe_http_status):'-')}</td>
      <td>${esc(row.probe_error_class||'-')}</td>
      <td>${esc(err||'-')}</td>
      <td class="numeric mono">${esc(row.duration_ms?String(row.duration_ms):'-')}</td>
    </tr>`;
  }).join('');
}

function quotaDiagnosticCard(label, value, detail, cls) {
  return `<div class="quota-diag-card ${cls||'neutral'}">
    <div class="rh-label">${esc(label)}</div>
    <div class="rh-value mono">${esc(value)}</div>
    <div class="rh-detail">${esc(detail||'-')}</div>
  </div>`;
}

function quotaDiagnosticEventLabel(row) {
  if(!row) return '-';
  return [row.provider,row.phase,row.status].filter(Boolean).join(' / ')||'-';
}

async function refreshQuotaNow() {
  const btn=$('quota-refresh');
  const diagBtn=$('quota-refresh-diagnostics');
  if(btn) btn.disabled=true;
  if(diagBtn) diagBtn.disabled=true;
  try {
    await fetchJSON('quota/refresh',{method:'POST'});
    await Promise.allSettled([loadQuota(),loadObservability(),loadQuotaDiagnostics()]);
  } finally {
    if(btn) btn.disabled=false;
    if(diagBtn) diagBtn.disabled=false;
  }
}

async function loadErrors() {
  const data=await fetchJSON('errors?range='+encodeURIComponent(getRange())+'&nonzero=true');
  const rows=Array.isArray(data.timeline)?data.timeline:[];
  if(!rows.length){
    $('errors-table-wrap').classList.add('hidden');
    $('errors-state').classList.remove('hidden');
    $('errors-state').innerHTML=`<div class="empty-state"><strong>${esc(t('state.no_errors'))}</strong>${esc(t('state.all_error_buckets_zero'))}</div>`;
    return;
  }
  $('errors-state').classList.add('hidden');
  $('errors-table-wrap').classList.remove('hidden');
  $('errors-table').innerHTML=rows.map(r=>`<tr><td class="mono">${esc(fmtTime(r.timestamp))}</td><td class="numeric mono">${fmtFull(r.count)}</td><td class="numeric mono">${fmtFull(r.parse_errors)}</td><td class="numeric mono">${fmtFull(r.db_errors)}</td><td class="numeric mono">${fmtFull(r.dropped_events)}</td></tr>`).join('');
}

/* --- Refresh ----------------------------------------------- */
async function refresh() {
  if(isRefreshing)return; isRefreshing=true;
  setRefreshing(true);
  try {
    const tasks=[]; if(!metadata) tasks.push(['metadata',loadMetadata]);
    tasks.push(['overview',loadOverview],['issues',loadIssues],['activity',loadActivity],['models',loadModels],['images',loadImages],['keys',loadKeys],['timeseries',loadTimeseries],['errors',loadErrors],['health',loadHealth],['quota',loadQuota],['quotaDiagnostics',loadQuotaDiagnostics],['observability',loadObservability]);
    if(requestsExpanded) tasks.push(['requests',loadRequests]);
    const settled=await Promise.allSettled(tasks.map(([,fn])=>fn()));
    const failures=settled.map((res,i)=>({res,name:tasks[i][0]})).filter(x=>x.res.status==='rejected').map(x=>({name:x.name,error:x.res.reason}));
    if(!failures.length) setStatus('live',t('status.dashboard_live'),t('status.all_panels'));
    else if(failures.length===tasks.length) setStatus('error',t('status.refresh_failed'),failures.map(f=>f.name).join(', '));
    else setStatus('partial',t('status.partial_refresh'),failures.map(f=>f.name).join(', '));
    markFailed(failures);
    setLastRefresh(new Date());
  } finally { setRefreshing(false); isRefreshing=false; }
}
function markFailed(failures) {
  const fm=new Map(failures.map(f=>[f.name,f.error]));
  if(fm.has('models')){const mt=$('models-table');if(mt)mt.innerHTML=errorRow(7,fm.get('models').message);const dc=$('model-distribution-chart');if(dc)dc.innerHTML=`<div class="empty-state error-text">${esc(fm.get('models').message)}</div>`;}
  if(fm.has('images')){const it=$('images-models-table');if(it)it.innerHTML=errorRow(9,fm.get('images').message);setText('images-summary',fm.get('images').message);}
  if(fm.has('keys')){const keysErr=fm.get('keys');const kt=$('keys-table');if(kt)kt.innerHTML=errorRow(7,keysErr&&keysErr.message);if(selectedKeyHash) markKeyDetailUnavailable(keysErr&&keysErr.message);}
  if(fm.has('requests'))$('requests-table').innerHTML=errorRow(11,fm.get('requests').message);
  if(fm.has('issues')){$('issues-state').classList.remove('hidden');$('issues-state').innerHTML=`<div class="issues-empty error-text">${esc(fm.get('issues').message)}</div>`;const io=$('issues-overview');if(io)io.innerHTML='';$('issues-list').innerHTML=`<div class="issue-class-placeholder">${esc(t('issues.select_severity_hint'))}</div>`;}
  if(fm.has('timeseries')){const el=$('usage-trend-chart');if(el)el.innerHTML=`<div class="empty-state error-text">${esc(fm.get('timeseries').message)}</div>`;}
  if(fm.has('quota'))renderQuotaSummary([],fm.get('quota').message);
  if(fm.has('quotaDiagnostics'))setText('quota-diagnostics-summary',fm.get('quotaDiagnostics').message);
  if(fm.has('observability'))setText('observability-summary',fm.get('observability').message);
}

/* --- Toggle requests / nav --------------------------------- */
async function toggleRequests() {
  const next=!requestsExpanded;
  if(!next){
    selectedIssueSeverity='';
    currentIssueClassFilter='';
    closeRequestDetails();
    loadIssues();
    return;
  }
  requestsExpanded=true;
  $('request-details').classList.remove('hidden');
  updateToggleLabels();
  $('requests-table').innerHTML=emptyRow(11,t('state.loading_requests'));
  await reloadReqFilter();
}
function updateToggleLabels() {
  document.querySelectorAll('[data-toggle-requests]').forEach(b=>{b.textContent=requestsExpanded?t('action.hide_requests'):t('action.show_requests');});
}
function closeRequestDetails() {
  requestsExpanded=false;
  const details=$('request-details');
  if(details) details.classList.add('hidden');
  const table=$('requests-table');
  if(table) table.innerHTML=emptyRow(11,t('state.not_loaded'));
  updateToggleLabels();
}
async function reloadReqFilter() { if(!requestsExpanded)return; try{await loadRequests();}catch(e){$('requests-table').innerHTML=errorRow(11,e.message);} }
async function showReqForIssueClass(errorClass, group) {
  if(!errorClass)return;
  if(currentIssueClassFilter===errorClass && requestsExpanded){
    currentIssueClassFilter='';
    closeRequestDetails();
    loadIssues();
    return;
  }
  currentIssueClassFilter=errorClass;
  syncRequestFiltersForIssue(group);
  if(!requestsExpanded){requestsExpanded=true;$('request-details').classList.remove('hidden');updateToggleLabels();}
  $('requests-table').innerHTML=emptyRow(11,t('state.loading_requests'));
  await reloadReqFilter();
  loadIssues();
  $('request-details').scrollIntoView({block:'start',behavior:'smooth'});
}
function bindNav() {
  const links=document.querySelectorAll('.nav-link');
  const setA=item=>{links.forEach(l=>l.classList.toggle('active',l===item));};
  links.forEach(l=>l.addEventListener('click',()=>setA(l)));
  const cur=[...links].find(l=>l.hash&&l.hash===location.hash);
  if(cur)setA(cur);
}
function configAutoRefresh() {
  if(autoRefreshTimer){clearInterval(autoRefreshTimer);autoRefreshTimer=null;}
  if($('auto-refresh').checked) autoRefreshTimer=setInterval(refresh,30000);
}
function debounce(fn,ms){let t;return()=>{clearTimeout(t);t=setTimeout(fn,ms);};}
function rerenderCharts(){renderUsagePanel(); const kdBody=$('key-detail-body'); if(selectedKeyHash && kdBody && !kdBody.hidden) renderKeyTrend(lastKeyTSRows,lastKeyTSBucket);}

/* --- Init -------------------------------------------------- */
document.addEventListener('DOMContentLoaded', async ()=>{
  applyTheme(currentTheme);
  applyI18N(); applyMeta();
  updateLayoutPanels();
  setStatus('live',t('status.ready'),t('status.waiting'));
  setLastRefresh(null);
  $('language-select').addEventListener('change',e=>setLang(e.target.value));
  $('refresh-btn').addEventListener('click',refresh);
  const qr=$('quota-refresh'); if(qr)qr.addEventListener('click',refreshQuotaNow);
  const qrd=$('quota-refresh-diagnostics'); if(qrd)qrd.addEventListener('click',refreshQuotaNow);
  $('range-select').addEventListener('change',()=>{resetIssueSelection();refresh();});
  $('filter-status').addEventListener('change',()=>{currentIssueClassFilter='';reloadReqFilter();});
  $('filter-model').addEventListener('change',()=>{currentIssueClassFilter='';reloadReqFilter();});
  $('filter-endpoint').addEventListener('change',()=>{currentIssueClassFilter='';reloadReqFilter();});
  document.querySelectorAll('[data-usage-mode]').forEach(b=>b.addEventListener('click',()=>setUsageMode(b.dataset.usageMode)));
  document.querySelectorAll('[data-toggle-requests]').forEach(b=>b.addEventListener('click',toggleRequests));
  $('auto-refresh').addEventListener('change',configAutoRefresh);
  const tb=$('theme-toggle'); if(tb)tb.addEventListener('click',toggleTheme);
  window.addEventListener('focus',debounce(()=>{if(Date.now()-lastRefreshAt<10000)return;refresh();},2000));
  window.addEventListener('resize',debounce(rerenderCharts,160));
  bindNav();
  bindKeyDetailControls();
  await refresh();
});
