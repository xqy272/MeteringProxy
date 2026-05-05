package cliproxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

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
	key, err := ReadKeyFile(cfg.KeyFile)
	if err != nil {
		return nil, err
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
	Timeout time.Duration `yaml:"timeout"`
}

type AuthFileEntry struct {
	Provider     string `json:"provider"`
	AuthType     string `json:"auth_type"`
	AuthIndex    int    `json:"auth_index"`
	Label        string `json:"label"`
	Status       string `json:"status"`
	SuccessCount int64  `json:"success_count"`
	FailedCount  int64  `json:"failed_count"`
	Available    bool   `json:"available"`
	Key          string `json:"key"`
	Source       string `json:"source"`
}

type AuthFilesResponse struct {
	AuthFiles []AuthFileEntry `json:"auth_files"`
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
