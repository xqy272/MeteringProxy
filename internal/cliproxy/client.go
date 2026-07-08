package cliproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const maxUsageQueueResponseBytes = 4 * 1024 * 1024

type Client struct {
	baseURL    string
	key        string
	httpClient *http.Client
	component  string
}

func NewClient(cfg CLIProxyConfig) (*Client, error) {
	baseURL, err := validateBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(cfg.Key)
	if key == "" {
		var err error
		key, err = ReadKeyFile(cfg.KeyFile)
		if err != nil {
			return nil, err
		}
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		baseURL:   baseURL,
		key:       key,
		component: cfg.Component,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

// setManagementHeaders stamps User-Agent and X-Metering-Component on every
// management request so CPA can distinguish them from LLM traffic (4.2.6).
func (c *Client) setManagementHeaders(req *http.Request) {
	req.Header.Set("User-Agent", managementUserAgent)
	if c.component != "" {
		req.Header.Set("X-Metering-Component", c.component)
	}
}

func ReadKeyFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read management key file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func validateBaseURL(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("cliproxy management base_url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse cliproxy management base_url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("cliproxy management base_url must use http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("cliproxy management base_url must include a host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("cliproxy management base_url must not include query or fragment")
	}
	if strings.TrimRight(parsed.Path, "/") != "/v0/management" {
		return "", fmt.Errorf("cliproxy management base_url must end with /v0/management")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

type CLIProxyConfig struct {
	Enabled bool          `yaml:"enabled"`
	BaseURL string        `yaml:"base_url"`
	KeyFile string        `yaml:"key_file"`
	Key     string        `yaml:"-"`
	Timeout time.Duration `yaml:"timeout"`
	// Component identifies which subsystem owns this client. It is sent as
	// X-Metering-Component on every management request so CPA logs can
	// distinguish credential_health, usage_queue, and quota_refresh traffic
	// from LLM request traffic. Empty means "unspecified".
	Component string `yaml:"-"`
}

const managementUserAgent = "MeteringProxy/1.0 (management)"

type AuthFileEntry struct {
	ID                   string                    `json:"id"`
	Provider             string                    `json:"provider"`
	AuthType             string                    `json:"auth_type"`
	AuthIndex            string                    `json:"auth_index"`
	Label                string                    `json:"label"`
	Name                 string                    `json:"name"`
	DisplayName          string                    `json:"display_name"`
	Email                string                    `json:"email"`
	Status               string                    `json:"status"`
	StatusMessage        string                    `json:"status_message"`
	SuccessCount         int64                     `json:"success_count"`
	FailedCount          int64                     `json:"failed_count"`
	RecentSuccessCount   int64                     `json:"recent_success_count"`
	RecentFailedCount    int64                     `json:"recent_failed_count"`
	RecentRequests       []AuthRecentRequestBucket `json:"recent_requests"`
	Available            bool                      `json:"available"`
	AvailableSet         bool                      `json:"-"`
	Disabled             bool                      `json:"disabled"`
	Unavailable          bool                      `json:"unavailable"`
	Key                  string                    `json:"key"`
	Source               string                    `json:"source"`
	Plan                 string                    `json:"plan"`
	NextRetryAfter       string                    `json:"next_retry_after"`
	NextRetryAfterUnix   int64                     `json:"next_retry_after_unix"`
	QuotaExceeded        bool                      `json:"quota_exceeded"`
	QuotaReason          string                    `json:"quota_reason"`
	QuotaNextRecoverAt   string                    `json:"quota_next_recover_at"`
	QuotaNextRecoverUnix int64                     `json:"quota_next_recover_unix"`
	ErrorClass           string                    `json:"error_class"`
	ErrorType            string                    `json:"error_type"`
	ErrorCode            string                    `json:"error_code"`
	ErrorMessage         string                    `json:"error_message"`
}

type AuthFilesResponse struct {
	AuthFiles []AuthFileEntry `json:"auth_files"`
}

type AuthRecentRequestBucket struct {
	Time    string `json:"time"`
	Success int64  `json:"success"`
	Failed  int64  `json:"failed"`
}

type authErrorDetails struct {
	ErrorType       string
	ErrorCode       string
	ErrorMessage    string
	Plan            string
	NextRecoverAt   string
	NextRecoverUnix int64
}

func (e *AuthFileEntry) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.ID = rawString(raw, "id")
	e.Provider = firstString(raw, "provider", "type")
	e.AuthType = firstString(raw, "auth_type", "type")
	e.AuthIndex = rawString(raw, "auth_index")
	e.Label = rawString(raw, "label")
	e.Name = firstString(raw, "name", "username")
	e.DisplayName = firstString(raw, "display_name", "displayName")
	e.Email = firstString(raw, "email", "account_email", "user_email")
	if e.Email == "" {
		e.Email = firstNonEmpty(
			rawNestedString(raw, "account", "email", "account_email", "user_email"),
			rawNestedString(raw, "profile", "email", "account_email", "user_email"),
			rawNestedString(raw, "id_token", "email", "account_email", "user_email"),
		)
	}
	e.Status = rawString(raw, "status")
	e.StatusMessage = firstString(raw, "status_message", "message", "last_error", "last_error_message")
	e.SuccessCount = firstInt64(raw, "success_count", "success")
	e.FailedCount = firstInt64(raw, "failed_count", "failed")
	e.RecentRequests = rawRecentRequests(raw, "recent_requests")
	e.RecentSuccessCount, e.RecentFailedCount = sumRecentRequests(e.RecentRequests)
	e.Available = rawBool(raw, "available")
	_, e.AvailableSet = raw["available"]
	e.Disabled = rawBool(raw, "disabled")
	e.Unavailable = rawBool(raw, "unavailable")
	if !e.AvailableSet {
		e.Available = !e.Disabled && !e.Unavailable
	}
	e.Key = rawString(raw, "key")
	e.Source = rawString(raw, "source")
	e.Plan = firstString(raw, "plan", "plan_type", "planType")
	if e.Plan == "" {
		e.Plan = rawNestedString(raw, "id_token", "plan_type", "planType")
	}
	e.NextRetryAfter = firstString(raw, "next_retry_after", "nextRetryAfter")
	e.NextRetryAfterUnix = firstUnixTime(raw, "next_retry_after", "nextRetryAfter")
	inlineErr := rawErrorDetails(raw)
	if e.Plan == "" {
		e.Plan = inlineErr.Plan
	}
	if e.NextRetryAfter == "" && inlineErr.NextRecoverAt != "" {
		e.NextRetryAfter = inlineErr.NextRecoverAt
		e.NextRetryAfterUnix = inlineErr.NextRecoverUnix
	}
	e.QuotaExceeded, e.QuotaReason, e.QuotaNextRecoverAt, e.QuotaNextRecoverUnix = rawQuotaDetails(raw)
	if e.NextRetryAfter == "" && e.QuotaNextRecoverAt != "" {
		e.NextRetryAfter = e.QuotaNextRecoverAt
		e.NextRetryAfterUnix = e.QuotaNextRecoverUnix
	}
	e.ErrorClass = firstString(raw, "error_class", "error_type", "error_code")
	e.ErrorType = rawString(raw, "error_type")
	e.ErrorCode = rawString(raw, "error_code")
	e.ErrorMessage = rawString(raw, "error_message")
	if e.ErrorType == "" {
		e.ErrorType = inlineErr.ErrorType
	}
	if e.ErrorCode == "" {
		e.ErrorCode = inlineErr.ErrorCode
	}
	if e.ErrorMessage == "" {
		e.ErrorMessage = inlineErr.ErrorMessage
	} else if looksLikeJSONObject(e.ErrorMessage) && inlineErr.ErrorMessage != "" {
		e.ErrorMessage = inlineErr.ErrorMessage
	}
	if e.ErrorClass == "" {
		e.ErrorClass = firstNonEmpty(inlineErr.ErrorType, inlineErr.ErrorCode)
	}
	if e.ErrorClass == "" && e.QuotaExceeded {
		e.ErrorClass = firstNonEmpty(e.QuotaReason, "quota")
	}
	if e.StatusMessage == "" {
		e.StatusMessage = inlineErr.ErrorMessage
	} else if looksLikeJSONObject(e.StatusMessage) && inlineErr.ErrorMessage != "" {
		e.StatusMessage = inlineErr.ErrorMessage
	}
	return nil
}

func (r *AuthFilesResponse) UnmarshalJSON(data []byte) error {
	var raw struct {
		AuthFiles []AuthFileEntry `json:"auth_files"`
		Files     []AuthFileEntry `json:"files"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw.AuthFiles) > 0 {
		r.AuthFiles = raw.AuthFiles
		return nil
	}
	r.AuthFiles = raw.Files
	return nil
}

func (c *Client) FetchAuthFiles() (*AuthFilesResponse, error) {
	url := c.baseURL + "/auth-files"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create auth-files request: %w", err)
	}
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	c.setManagementHeaders(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch auth-files: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth-files returned %d", resp.StatusCode)
	}
	var result AuthFilesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode auth-files: %w", err)
	}
	return &result, nil
}

func (c *Client) DoAPICall(method, path string, body io.Reader) ([]byte, int, error) {
	url := c.baseURL + path
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, 0, fmt.Errorf("create api-call request: %w", err)
	}
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setManagementHeaders(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("api-call %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read api-call response: %w", err)
	}
	return respBody, resp.StatusCode, nil
}

func (c *Client) FetchUsageQueue(count int) ([][]byte, error) {
	if count <= 0 {
		count = 1
	}
	url := fmt.Sprintf("%s/usage-queue?count=%d", c.baseURL, count)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create usage-queue request: %w", err)
	}
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	c.setManagementHeaders(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch usage-queue: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage-queue returned %d", resp.StatusCode)
	}
	var records []json.RawMessage
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxUsageQueueResponseBytes)).Decode(&records); err != nil {
		return nil, fmt.Errorf("decode usage-queue: %w", err)
	}
	out := make([][]byte, 0, len(records))
	for _, record := range records {
		trimmed := bytes.TrimSpace(record)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			continue
		}
		if trimmed[0] == '"' {
			var s string
			if err := json.Unmarshal(trimmed, &s); err != nil {
				return nil, fmt.Errorf("decode usage-queue string record: %w", err)
			}
			out = append(out, []byte(s))
			continue
		}
		out = append(out, append([]byte(nil), trimmed...))
	}
	return out, nil
}

func firstString(raw map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := rawString(raw, key); value != "" {
			return value
		}
	}
	return ""
}

func rawNestedString(raw map[string]json.RawMessage, objectKey string, keys ...string) string {
	obj := rawObject(raw, objectKey)
	if obj == nil {
		return ""
	}
	return firstString(obj, keys...)
}

func rawObject(raw map[string]json.RawMessage, objectKey string) map[string]json.RawMessage {
	data, ok := raw[objectKey]
	if !ok || len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil
	}
	return obj
}

func rawRecentRequests(raw map[string]json.RawMessage, key string) []AuthRecentRequestBucket {
	data, ok := raw[key]
	if !ok || len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	var rows []AuthRecentRequestBucket
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil
	}
	return rows
}

func sumRecentRequests(rows []AuthRecentRequestBucket) (success, failed int64) {
	for _, row := range rows {
		success += row.Success
		failed += row.Failed
	}
	return success, failed
}

func firstUnixTime(raw map[string]json.RawMessage, keys ...string) int64 {
	for _, key := range keys {
		if value := rawUnixTime(raw, key); value != 0 {
			return value
		}
	}
	return 0
}

func rawUnixTime(raw map[string]json.RawMessage, key string) int64 {
	data, ok := raw[key]
	if !ok || len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return 0
	}
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		return n
	}
	var f float64
	if err := json.Unmarshal(data, &f); err == nil {
		return int64(f)
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.Unix()
		}
		var parsed json.Number = json.Number(s)
		n, _ := parsed.Int64()
		return n
	}
	return 0
}

func rawErrorDetails(raw map[string]json.RawMessage) authErrorDetails {
	for _, key := range []string{"error", "last_error", "status_message", "message", "error_message", "last_error_message"} {
		data, ok := raw[key]
		if !ok || len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			continue
		}
		if details, ok := decodeAuthErrorDetails(data); ok {
			return details
		}
	}
	return authErrorDetails{}
}

func decodeAuthErrorDetails(data json.RawMessage) (authErrorDetails, bool) {
	var message string
	if err := json.Unmarshal(data, &message); err == nil {
		message = strings.TrimSpace(message)
		if looksLikeJSONObject(message) {
			if details, ok := decodeAuthErrorDetails(json.RawMessage(message)); ok {
				return details, true
			}
		}
		return authErrorDetails{ErrorMessage: message}, message != ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return authErrorDetails{}, false
	}
	if inner := rawObject(obj, "error"); inner != nil {
		details := authErrorDetailsFromObject(inner)
		outer := authErrorDetailsFromObject(obj)
		if details.Plan == "" {
			details.Plan = outer.Plan
		}
		if details.NextRecoverAt == "" {
			details.NextRecoverAt = outer.NextRecoverAt
			details.NextRecoverUnix = outer.NextRecoverUnix
		}
		return details, details.hasValue()
	}
	details := authErrorDetailsFromObject(obj)
	return details, details.hasValue()
}

func authErrorDetailsFromObject(obj map[string]json.RawMessage) authErrorDetails {
	nextRecoverAt, nextRecoverUnix := rawRecoverTime(obj)
	return authErrorDetails{
		ErrorType:       firstString(obj, "type", "error_type"),
		ErrorCode:       firstString(obj, "code", "error_code"),
		ErrorMessage:    firstString(obj, "message", "error_message", "detail"),
		Plan:            firstString(obj, "plan", "plan_type", "planType"),
		NextRecoverAt:   nextRecoverAt,
		NextRecoverUnix: nextRecoverUnix,
	}
}

func (d authErrorDetails) hasValue() bool {
	return d.ErrorType != "" || d.ErrorCode != "" || d.ErrorMessage != "" || d.Plan != "" || d.NextRecoverAt != ""
}

func rawRecoverTime(raw map[string]json.RawMessage) (string, int64) {
	if unix := firstUnixTime(raw, "next_recover_at", "nextRecoverAt", "reset_at", "resetAt", "resets_at", "resetsAt"); unix > 0 {
		return time.Unix(normalizeUnixSeconds(unix), 0).UTC().Format(time.RFC3339), normalizeUnixSeconds(unix)
	}
	if seconds := firstInt64(raw, "resets_in_seconds", "reset_in_seconds", "retry_after_seconds", "retryAfterSeconds"); seconds > 0 {
		t := time.Now().UTC().Add(time.Duration(seconds) * time.Second)
		return t.Format(time.RFC3339), t.Unix()
	}
	return "", 0
}

func normalizeUnixSeconds(unix int64) int64 {
	if unix > 100000000000 {
		return unix / 1000
	}
	return unix
}

func looksLikeJSONObject(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}")
}

func rawQuotaDetails(raw map[string]json.RawMessage) (exceeded bool, reason, nextRecoverAt string, nextRecoverUnix int64) {
	obj := rawObject(raw, "quota")
	if obj == nil {
		return false, "", "", 0
	}
	exceeded = rawBool(obj, "exceeded")
	reason = firstString(obj, "reason", "status", "status_message")
	nextRecoverAt = firstString(obj, "next_recover_at", "nextRecoverAt", "reset_at", "resetAt")
	nextRecoverUnix = firstUnixTime(obj, "next_recover_at", "nextRecoverAt", "reset_at", "resetAt")
	return exceeded, reason, nextRecoverAt, nextRecoverUnix
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func rawString(raw map[string]json.RawMessage, key string) string {
	data, ok := raw[key]
	if !ok || len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return ""
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		return strings.TrimSpace(n.String())
	}
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		if b {
			return "true"
		}
		return "false"
	}
	return ""
}

func firstInt64(raw map[string]json.RawMessage, keys ...string) int64 {
	for _, key := range keys {
		if value := rawInt64(raw, key); value != 0 {
			return value
		}
	}
	return 0
}

func rawInt64(raw map[string]json.RawMessage, key string) int64 {
	data, ok := raw[key]
	if !ok || len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return 0
	}
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		return n
	}
	var f float64
	if err := json.Unmarshal(data, &f); err == nil {
		return int64(f)
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		var parsed json.Number = json.Number(strings.TrimSpace(s))
		n, _ := parsed.Int64()
		return n
	}
	return 0
}

func rawBool(raw map[string]json.RawMessage, key string) bool {
	data, ok := raw[key]
	if !ok || len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return false
	}
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		return b
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "1", "yes", "y":
			return true
		}
	}
	var n float64
	if err := json.Unmarshal(data, &n); err == nil {
		return n != 0
	}
	return false
}
