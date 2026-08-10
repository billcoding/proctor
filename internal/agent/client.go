package agent

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/billcoding/proctor/internal/model"
)

type Client struct {
	base       string
	http       *http.Client
	agentID    string
	agentToken string
}

func NewClient(baseURL, agentID, agentToken string, insecureSkipVerify bool) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecureSkipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // intentional for lab/self-signed
	}
	return &Client{
		base:       strings.TrimRight(baseURL, "/"),
		agentID:    agentID,
		agentToken: agentToken,
		http: &http.Client{
			Timeout:   60 * time.Second,
			Transport: tr,
		},
	}
}

func (c *Client) Heartbeat(payload model.HeartbeatPayload) (*model.Policy, []model.Command, []model.FSJob, []model.ShellOffer, error) {
	var resp struct {
		OK             bool               `json:"ok"`
		Policy         model.Policy       `json:"policy"`
		Commands       []model.Command    `json:"commands"`
		FSJobs         []model.FSJob      `json:"fs_jobs"`
		ShellSessions  []model.ShellOffer `json:"shell_sessions"`
		Error          string             `json:"error,omitempty"`
	}
	if err := c.postJSON("/api/agent/heartbeat", payload, &resp); err != nil {
		return nil, nil, nil, nil, err
	}
	if !resp.OK {
		return nil, nil, nil, nil, fmt.Errorf("heartbeat rejected: %s", resp.Error)
	}
	return &resp.Policy, resp.Commands, resp.FSJobs, resp.ShellSessions, nil
}

func (c *Client) ReportCommand(result model.CommandResult) error {
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := c.postJSON("/api/agent/command/result", result, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("command result rejected: %s", resp.Error)
	}
	return nil
}

func (c *Client) ReportFSJob(result model.FSJobResult) error {
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := c.postJSON("/api/agent/fs/result", result, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("fs result rejected: %s", resp.Error)
	}
	return nil
}

func (c *Client) CheckUpdate(goos, goarch, version, target string) (*UpdateInfo, error) {
	q := url.Values{}
	q.Set("os", goos)
	q.Set("arch", goarch)
	q.Set("version", version)
	if strings.TrimSpace(target) != "" {
		q.Set("target", strings.TrimSpace(target))
	}
	var info UpdateInfo
	if err := c.getJSON("/api/agent/update/check?"+q.Encode(), &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// DownloadUpdate fetches the binary to a temp file and returns its path.
// Caller must remove the temp file.
func (c *Client) DownloadUpdate(downloadURL string) (string, error) {
	path := downloadURL
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		// absolute — use as-is
	} else {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		path = c.base + path
	}
	req, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Agent-ID", c.agentID)
	if c.agentToken != "" {
		req.Header.Set("X-Agent-Token", c.agentToken)
	}
	// Large binaries: extend timeout via a dedicated client clone.
	httpClient := *c.http
	httpClient.Timeout = 10 * time.Minute
	res, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		return "", fmt.Errorf("http %d: %s", res.StatusCode, string(data))
	}
	tmp, err := os.CreateTemp("", "proctor-agent-update-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	_, copyErr := io.Copy(tmp, res.Body)
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", closeErr
	}
	return tmpPath, nil
}

func (c *Client) getJSON(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Agent-ID", c.agentID)
	if c.agentToken != "" {
		req.Header.Set("X-Agent-Token", c.agentToken)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", res.StatusCode, string(data))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func (c *Client) postJSON(path string, in any, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-ID", c.agentID)
	if c.agentToken != "" {
		req.Header.Set("X-Agent-Token", c.agentToken)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 12<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", res.StatusCode, string(data))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}
