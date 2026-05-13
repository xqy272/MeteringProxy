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
		baseURL: baseURL,
		key:     key,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
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
}

type AuthFileEntry struct {
	ID            string `json:"id"`
	Provider      string `json:"provider"`
	AuthType      string `json:"auth_type"`
	AuthIndex     string `json:"auth_index"`
	Label         string `json:"label"`
	Status        string `json:"status"`
	StatusMessage string `json:"status_message"`
	SuccessCount  int64  `json:"success_count"`
	FailedCount   int64  `json:"failed_count"`
	Available     bool   `json:"available"`
	Disabled      bool   `json:"disabled"`
	Unavailable   bool   `json:"unavailable"`
	Key           string `json:"key"`
	Source        string `json:"source"`
	ErrorClass    string `json:"error_class"`
}

type AuthFilesResponse struct {
	AuthFiles []AuthFileEntry `json:"auth_files"`
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
	e.Status = rawString(raw, "status")
	e.StatusMessage = rawString(raw, "status_message")
	e.SuccessCount = firstInt64(raw, "success_count", "success")
	e.FailedCount = firstInt64(raw, "failed_count", "failed")
	e.Available = rawBool(raw, "available")
	e.Disabled = rawBool(raw, "disabled")
	e.Unavailable = rawBool(raw, "unavailable")
	if _, ok := raw["available"]; !ok {
		e.Available = !e.Disabled && !e.Unavailable
	}
	e.Key = rawString(raw, "key")
	e.Source = rawString(raw, "source")
	e.ErrorClass = rawString(raw, "error_class")
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
