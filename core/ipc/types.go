// Package ipc provides a loopback control channel so a running Ferry daemon
// (GUI tray or headless `ferry daemon`) can serve CLI, MCP, and shell menu
// requests without duplicating signaling sessions or losing LAN peer state.
package ipc

import (
	"context"
	"time"
)

const discoveryFileName = "daemon.json"

// DaemonInfo is persisted as 0600 JSON inside the Ferry state directory so local
// tools can discover and authenticate against the active daemon.
type DaemonInfo struct {
	Addr       string `json:"addr"`
	Token      string `json:"token"`
	PID        int    `json:"pid"`
	KeyPath    string `json:"key_path,omitempty"`
	PairedPath string `json:"paired_path,omitempty"`
}

// StatusResponse is returned by GET /v1/status.
type StatusResponse struct {
	OK          bool              `json:"ok"`
	Version     string            `json:"version,omitempty"`
	DeviceID    string            `json:"device_id,omitempty"`
	Name        string            `json:"name,omitempty"`
	SignalState string            `json:"signal_state,omitempty"`
	LANPort     int               `json:"lan_port,omitempty"`
	ReceiveDir  string            `json:"receive_dir,omitempty"`
	KeyPath     string            `json:"key_path,omitempty"`
	PairedPath  string            `json:"paired_path,omitempty"`
	OnlinePeers []string          `json:"online_peers,omitempty"`
	LANPeers    map[string]string `json:"lan_peers,omitempty"`
}

// PairedPeerInfo mirrors a trusted peer entry for IPC callers.
type PairedPeerInfo struct {
	DeviceID string    `json:"device_id"`
	Name     string    `json:"name"`
	PubKey   string    `json:"pub_key"`
	PairedAt time.Time `json:"paired_at"`
	Online   bool      `json:"online,omitempty"`
	LANAddr  string    `json:"lan_addr,omitempty"`
}

// LANPeerInfo describes an active LAN-discovered receiver.
type LANPeerInfo struct {
	DeviceID string `json:"device_id,omitempty"`
	Name     string `json:"name"`
	Addr     string `json:"addr"`
	Iface    string `json:"iface,omitempty"`
}

// PeersResponse is returned by GET /v1/peers.
type PeersResponse struct {
	Paired []PairedPeerInfo `json:"paired"`
	LAN    []LANPeerInfo    `json:"lan"`
}

// SendRequest is passed to POST /v1/send.
type SendRequest struct {
	Peer        string   `json:"peer,omitempty"`
	Files       []string `json:"files"`
	Mode        string   `json:"mode,omitempty"` // "" | "auto" | "lan" | "punch" | "relay"
	Interactive bool     `json:"interactive,omitempty"`
}

// SendTextRequest is passed to POST /v1/send-text.
type SendTextRequest struct {
	Peer     string `json:"peer"`
	Text     string `json:"text"`
	Filename string `json:"filename,omitempty"`
	Mode     string `json:"mode,omitempty"`
}

// SendResponse is returned by POST /v1/send and POST /v1/send-text.
type SendResponse struct {
	OK         bool     `json:"ok"`
	Path       string   `json:"path,omitempty"`
	Peer       string   `json:"peer,omitempty"`
	TransferID string   `json:"transfer_id,omitempty"`
	Files      []string `json:"files,omitempty"`
	Bytes      int64    `json:"bytes"`
	Chunks     int      `json:"chunks"`
	DurationMS int64    `json:"duration_ms"`
	Error      string   `json:"error,omitempty"`
}

// Handler executes local IPC control actions inside the host daemon.
type Handler interface {
	Status(ctx context.Context) StatusResponse
	Peers(ctx context.Context) (PeersResponse, error)
	Send(ctx context.Context, req SendRequest) SendResponse
	SendText(ctx context.Context, req SendTextRequest) SendResponse
	OpenUI(ctx context.Context) error
}
