package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a minimal read-only client for the Homey Web API (local or cloud relay).
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient returns a client with the given per-call timeout.
func NewClient(baseURL, token string, timeout time.Duration) *Client {
	return &Client{BaseURL: baseURL, Token: token, HTTP: &http.Client{Timeout: timeout}}
}

// Device is the subset of a Homey device the exporter uses.
type Device struct {
	ID              string                `json:"id"`
	Name            string                `json:"name"`
	Zone            string                `json:"zone"`
	Class           string                `json:"class"`
	Available       bool                  `json:"available"`
	CapabilitiesObj map[string]Capability `json:"capabilitiesObj"`
}

// Capability is one capability of a device. Value is a number, bool, string or null.
type Capability struct {
	ID    string `json:"id"`
	Type  string `json:"type"` // number | boolean | string | enum
	Units string `json:"units"`
	Value any    `json:"value"`
}

// Zone is a Homey zone (room).
type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Variable is a Homey Logic variable.
type Variable struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"` // number | boolean | string
	Value any    `json:"value"`
}

// System is the subset of GET /api/manager/system/ the exporter exports.
// Network fields (SSID, addresses, MACs) are deliberately not read.
type System struct {
	HomeyVersion          string    `json:"homeyVersion"`
	HomeyModelID          string    `json:"homeyModelId"`
	HomeyModelName        string    `json:"homeyModelName"`
	HomeyPlatform         string    `json:"homeyPlatform"`
	NodeVersion           string    `json:"nodeVersion"`
	LoadAvg               []float64 `json:"loadavg"`
	Uptime                float64   `json:"uptime"`
	CPUs                  []CPU     `json:"cpus"`
	VideoCoreTemperature  *float64  `json:"videoCoreTemperature"`
	VideoCoreThrottleNow  *bool     `json:"videoCoreThrottleCurrently"`
	VideoCoreUnderVoltNow *bool     `json:"videoCoreUnderVoltageCurrently"`
}

// CPU is one core with cumulative times in milliseconds (Node os.cpus()).
type CPU struct {
	Times map[string]float64 `json:"times"`
}

// Usage is GET /api/manager/system/memory or /storage: totals plus a per-component breakdown.
type Usage struct {
	Total float64              `json:"total"`
	Free  float64              `json:"free"`
	Used  float64              `json:"used"`
	Swap  float64              `json:"swap"`
	Types map[string]Component `json:"types"`
}

// Component is one entry of the memory/storage breakdown (Homey core, an app, insights, ...).
type Component struct {
	Name string  `json:"name"`
	Size float64 `json:"size"`
}

func (c *Client) get(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/manager/"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: HTTP %d: %s", path, resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("GET %s: decode: %w", path, err)
	}
	return nil
}

// Devices returns all devices keyed by id.
func (c *Client) Devices(ctx context.Context) (map[string]Device, error) {
	var m map[string]Device
	return m, c.get(ctx, "devices/device/", &m)
}

// Zones returns all zones keyed by id.
func (c *Client) Zones(ctx context.Context) (map[string]Zone, error) {
	var m map[string]Zone
	return m, c.get(ctx, "zones/zone/", &m)
}

// Variables returns all Logic variables keyed by id.
func (c *Client) Variables(ctx context.Context) (map[string]Variable, error) {
	var m map[string]Variable
	return m, c.get(ctx, "logic/variable/", &m)
}

// System returns system information.
func (c *Client) System(ctx context.Context) (System, error) {
	var s System
	return s, c.get(ctx, "system/", &s)
}

// Memory returns memory usage with a per-app breakdown.
func (c *Client) Memory(ctx context.Context) (Usage, error) {
	var u Usage
	return u, c.get(ctx, "system/memory", &u)
}

// Storage returns storage usage with a per-component breakdown.
func (c *Client) Storage(ctx context.Context) (Usage, error) {
	var u Usage
	return u, c.get(ctx, "system/storage", &u)
}
