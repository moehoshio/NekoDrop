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

// Limits bound NekoDrop's in-memory footprint to prevent untrusted traffic from
// exhausting server memory. A non-positive value uses a safe built-in default.
type Limits struct {
	// MaxMessagesPerChannel is the most recent messages kept in memory per
	// channel (older ones are evicted with their file payloads).
	MaxMessagesPerChannel int `json:"maxMessagesPerChannel"`
	// MaxFileBytesPerChannel caps in-memory uploaded-file bytes per channel.
	MaxFileBytesPerChannel int64 `json:"maxFileBytesPerChannel"`
	// MaxBytesPerChannel caps a channel's total resident memory (message text
	// plus file payloads); the oldest messages are evicted when it is hit.
	MaxBytesPerChannel int64 `json:"maxBytesPerChannel"`
	// MaxSubscribersPerChannel caps concurrent live connections per channel.
	MaxSubscribersPerChannel int `json:"maxSubscribersPerChannel"`
	// MaxChannels caps the number of simultaneously live channels.
	MaxChannels int `json:"maxChannels"`
	// MaxUsers caps the number of identities retained in memory.
	MaxUsers int `json:"maxUsers"`
}

// Admin configures the built-in administrator panel. The panel is disabled by
// default; enabling it additionally requires a non-empty access token, so a
// bare `"enabled": true` can never expose an unauthenticated panel.
type Admin struct {
	// Enabled turns the admin panel (and its API) on. Default false.
	Enabled bool `json:"enabled"`
	// Token is the secret an administrator must present to use the panel.
	// The panel stays disabled while the token is empty.
	Token string `json:"token"`
}

// Active reports whether the admin panel should actually be served: it must be
// explicitly enabled and have a non-empty token.
func (a Admin) Active() bool { return a.Enabled && a.Token != "" }

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
	// Limits bound in-memory resource usage.
	Limits Limits `json:"limits"`
	// Admin configures the administrator panel (disabled by default).
	Admin Admin `json:"admin"`
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
	if v := os.Getenv("NEKODROP_MAX_MESSAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Limits.MaxMessagesPerChannel = n
		}
	}
	if v := os.Getenv("NEKODROP_MAX_FILE_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.Limits.MaxFileBytesPerChannel = n
		}
	}
	if v := os.Getenv("NEKODROP_MAX_CHANNEL_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.Limits.MaxBytesPerChannel = n
		}
	}
	if v := os.Getenv("NEKODROP_MAX_SUBSCRIBERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Limits.MaxSubscribersPerChannel = n
		}
	}
	if v := os.Getenv("NEKODROP_MAX_LIVE_CHANNELS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Limits.MaxChannels = n
		}
	}
	if v := os.Getenv("NEKODROP_MAX_USERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Limits.MaxUsers = n
		}
	}
	if v := os.Getenv("NEKODROP_ADMIN_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.Admin.Enabled = b
		}
	}
	if v := os.Getenv("NEKODROP_ADMIN_TOKEN"); v != "" {
		c.Admin.Token = v
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
