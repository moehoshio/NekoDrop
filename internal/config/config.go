// Package config loads NekoDrop's runtime configuration. Settings may come from
// a JSON config file, environment variables, or command-line flags, applied in
// increasing order of precedence: file < environment < flags. Every setting has
// a sensible default, so NekoDrop runs with no configuration at all.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DefaultMaxUploadBytes mirrors the server's default per-file upload limit
// (32 MiB). It is duplicated here to avoid an import cycle.
const DefaultMaxUploadBytes = 32 << 20

// Storage configures the durable persistence backend.
type Storage struct {
	// Backend is one of "memory" (default), "sqlite" or "mysql".
	Backend string `json:"backend"`
	// DSN is the data source name for the backend: a file path for sqlite
	// (e.g. "nekodrop.db") or a Go MySQL DSN for mysql
	// (e.g. "user:pass@tcp(127.0.0.1:3306)/nekodrop"). Ignored for memory.
	DSN string `json:"dsn"`
}

// Config is the fully-resolved server configuration.
type Config struct {
	// Host is the IP address to listen on (empty = all interfaces).
	Host string `json:"host"`
	// Port is the TCP port to listen on.
	Port int `json:"port"`
	// MaxUploadBytes is the maximum accepted size of a single uploaded file.
	MaxUploadBytes int64 `json:"maxUploadBytes"`
	// MaxChannelsPerUser caps how many channels a single user may own
	// (0 = unlimited).
	MaxChannelsPerUser int `json:"maxChannelsPerUser"`
	// Storage selects and configures the persistence backend.
	Storage Storage `json:"storage"`
}

// Default returns the configuration used when nothing is specified.
func Default() Config {
	return Config{
		Host:               "",
		Port:               8080,
		MaxUploadBytes:     DefaultMaxUploadBytes,
		MaxChannelsPerUser: 5,
		Storage:            Storage{Backend: "memory"},
	}
}

// Addr returns the "host:port" listen address.
func (c Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// LoadFile reads and parses a JSON config file, returning the defaults merged
// with whatever the file specifies. A missing path returns the defaults with
// no error only when optional is true.
func LoadFile(path string, optional bool) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if optional && os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("config: parse %s: %w", path, err)
	}
	cfg.normalize()
	return cfg, nil
}

// ApplyEnv overlays NEKODROP_* environment variables onto the config.
func (c *Config) ApplyEnv() {
	if v := os.Getenv("NEKODROP_HOST"); v != "" {
		c.Host = v
	}
	if v := os.Getenv("NEKODROP_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Port = n
		}
	}
	// NEKODROP_ADDR ("host:port" or ":port") remains supported for compatibility.
	if v := os.Getenv("NEKODROP_ADDR"); v != "" {
		c.ApplyAddr(v)
	}
	if v := os.Getenv("NEKODROP_MAX_UPLOAD"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.MaxUploadBytes = n
		}
	}
	if v := os.Getenv("NEKODROP_MAX_CHANNELS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxChannelsPerUser = n
		}
	}
	if v := os.Getenv("NEKODROP_STORAGE_BACKEND"); v != "" {
		c.Storage.Backend = v
	}
	if v := os.Getenv("NEKODROP_STORAGE_DSN"); v != "" {
		c.Storage.DSN = v
	}
	c.normalize()
}

// ApplyAddr parses a "host:port" or ":port" string into Host/Port.
func (c *Config) ApplyAddr(addr string) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return
	}
	host, port := addr, ""
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host, port = addr[:i], addr[i+1:]
	}
	c.Host = host
	if port != "" {
		if n, err := strconv.Atoi(port); err == nil {
			c.Port = n
		}
	}
}

func (c *Config) normalize() {
	if c.Port == 0 {
		c.Port = 8080
	}
	if c.MaxUploadBytes <= 0 {
		c.MaxUploadBytes = DefaultMaxUploadBytes
	}
	if c.Storage.Backend == "" {
		c.Storage.Backend = "memory"
	}
}
