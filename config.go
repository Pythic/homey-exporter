package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the exporter configuration: one JSON file, a few environment overrides.
type Config struct {
	// HomeyURL is the base URL of the Homey Web API, e.g. http://192.168.1.10 or the
	// cloud relay https://<cloud-id>.connect.athom.com.
	HomeyURL string `json:"homey_url"`
	// Token is a Homey Personal Access Token (read-only scopes suffice).
	Token string `json:"token"`
	// TokenFile is read instead of Token when set (keeps the secret out of the config file).
	TokenFile string `json:"token_file"`
	// Listen is the address the exporter serves /metrics on.
	Listen string `json:"listen"`
	// CacheSeconds is how long one fetch from Homey is reused for consecutive scrapes.
	CacheSeconds float64 `json:"cache_seconds"`
	// TimeoutSeconds is the HTTP timeout per Homey API call.
	TimeoutSeconds float64 `json:"timeout_seconds"`
}

// Defaults for fields left empty in the config file.
const (
	defaultListen  = ":9414"
	defaultCache   = 10.0
	defaultTimeout = 10.0
)

// LoadConfig reads the JSON file at path (optional when path is empty or the file does not
// exist), then applies the environment overrides HOMEY_URL, HOMEY_TOKEN, HOMEY_TOKEN_FILE
// and HOMEY_EXPORTER_LISTEN and resolves TokenFile.
func LoadConfig(path string) (Config, error) {
	var c Config
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(b, &c); err != nil {
				return c, fmt.Errorf("config %s: %w", path, err)
			}
		case errors.Is(err, os.ErrNotExist):
			// No file: environment only.
		default:
			return c, err
		}
	}
	if v := os.Getenv("HOMEY_URL"); v != "" {
		c.HomeyURL = v
	}
	if v := os.Getenv("HOMEY_TOKEN"); v != "" {
		c.Token = v
	}
	if v := os.Getenv("HOMEY_TOKEN_FILE"); v != "" {
		c.TokenFile = v
	}
	if v := os.Getenv("HOMEY_EXPORTER_LISTEN"); v != "" {
		c.Listen = v
	}
	if c.TokenFile != "" {
		b, err := os.ReadFile(c.TokenFile)
		if err != nil {
			return c, fmt.Errorf("token_file: %w", err)
		}
		c.Token = strings.TrimSpace(string(b))
	}
	if c.Listen == "" {
		c.Listen = defaultListen
	}
	if c.CacheSeconds <= 0 {
		c.CacheSeconds = defaultCache
	}
	if c.TimeoutSeconds <= 0 {
		c.TimeoutSeconds = defaultTimeout
	}
	c.HomeyURL = strings.TrimRight(c.HomeyURL, "/")
	return c, c.validate()
}

func (c Config) validate() error {
	if c.HomeyURL == "" {
		return errors.New("homey_url is required (config file or HOMEY_URL)")
	}
	if !strings.HasPrefix(c.HomeyURL, "http://") && !strings.HasPrefix(c.HomeyURL, "https://") {
		return fmt.Errorf("homey_url %q must start with http:// or https://", c.HomeyURL)
	}
	if c.Token == "" {
		return errors.New("token is required (config file, token_file, HOMEY_TOKEN or HOMEY_TOKEN_FILE)")
	}
	return nil
}

// Cache returns the cache TTL as a duration.
func (c Config) Cache() time.Duration { return time.Duration(c.CacheSeconds * float64(time.Second)) }

// Timeout returns the per-call HTTP timeout as a duration.
func (c Config) Timeout() time.Duration {
	return time.Duration(c.TimeoutSeconds * float64(time.Second))
}
