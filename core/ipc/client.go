package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Client communicates with a running Ferry daemon over loopback HTTP.
type Client struct {
	info DaemonInfo
	http *http.Client
}

// TryConnect probes <stateDir>/daemon.json and verifies liveness via GET /v1/status
// within 800 ms. Any error indicates no live compatible daemon is running.
func TryConnect(stateDir string) (*Client, error) {
	if stateDir == "" {
		return nil, errors.New("ipc: empty state dir")
	}
	raw, err := os.ReadFile(filepath.Join(stateDir, discoveryFileName))
	if err != nil {
		return nil, err
	}
	var info DaemonInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	if info.Addr == "" || info.Token == "" {
		return nil, errors.New("ipc: incomplete daemon info")
	}
	cl := &Client{
		info: info,
		http: &http.Client{Timeout: 10 * time.Minute},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	if _, err := cl.Status(ctx); err != nil {
		return nil, err
	}
	return cl, nil
}

// Info returns the daemon metadata read from daemon.json.
func (c *Client) Info() DaemonInfo {
	return c.info
}

// Status queries GET /v1/status.
func (c *Client) Status(ctx context.Context) (StatusResponse, error) {
	var out StatusResponse
	err := c.doJSON(ctx, http.MethodGet, "/v1/status", nil, &out)
	return out, err
}

// Peers queries GET /v1/peers.
func (c *Client) Peers(ctx context.Context) (PeersResponse, error) {
	var out PeersResponse
	err := c.doJSON(ctx, http.MethodGet, "/v1/peers", nil, &out)
	return out, err
}

// Send dispatches POST /v1/send.
func (c *Client) Send(ctx context.Context, req SendRequest) (SendResponse, error) {
	var out SendResponse
	err := c.doJSON(ctx, http.MethodPost, "/v1/send", req, &out)
	if err != nil && out.Error == "" {
		return out, err
	}
	if !out.OK && out.Error != "" {
		return out, errors.New(out.Error)
	}
	return out, err
}

// SendText dispatches POST /v1/send-text.
func (c *Client) SendText(ctx context.Context, req SendTextRequest) (SendResponse, error) {
	var out SendResponse
	err := c.doJSON(ctx, http.MethodPost, "/v1/send-text", req, &out)
	if err != nil && out.Error == "" {
		return out, err
	}
	if !out.OK && out.Error != "" {
		return out, errors.New(out.Error)
	}
	return out, err
}

// Open asks the daemon to surface its GUI window (if any).
func (c *Client) Open(ctx context.Context) error {
	var out map[string]any
	return c.doJSON(ctx, http.MethodPost, "/v1/open", map[string]any{}, &out)
}

func (c *Client) doJSON(ctx context.Context, method, path string, in any, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+c.info.Addr+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.info.Token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("ipc: unauthorized")
	}
	if len(bytes.TrimSpace(raw)) > 0 && out != nil {
		_ = json.Unmarshal(raw, out)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ipc: %s returned %d", path, resp.StatusCode)
	}
	return nil
}
