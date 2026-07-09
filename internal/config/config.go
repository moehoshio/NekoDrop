// Package config loads NekoDrop's runtime configuration. Settings may come from
// a JSON config file, environment variables, or command-line flags, applied in
// increasing order of precedence: file < environment < flags. Every setting has
// a sensible default, so NekoDrop runs with no configuration at all.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultMaxUploadBytes mirrors the server's default per-file upload limit
// (32 MiB). It is duplicated here to avoid an import cycle.
const DefaultMaxUploadBytes = 32 << 20

// Limits bound NekoDrop's in-memory footprint to prevent untrusted traffic from
// exhausting server memory. A non-positive value uses a safe built-in default.
type Limits struct {
	// MaxMessagesPerChannel is the most recent messages kept in memory per
	// channel (older ones are evicted with their file payloads).
	MaxMessagesPerChannel int `json:"maxMessagesPerChannel" yaml:"maxMessagesPerChannel"`
	// MaxFileBytesPerChannel caps in-memory uploaded-file bytes per channel.
	MaxFileBytesPerChannel int64 `json:"maxFileBytesPerChannel" yaml:"maxFileBytesPerChannel"`
	// MaxBytesPerChannel caps a channel's total resident memory (message text
	// plus file payloads); the oldest messages are evicted when it is hit.
	MaxBytesPerChannel int64 `json:"maxBytesPerChannel" yaml:"maxBytesPerChannel"`
	// MaxSubscribersPerChannel caps concurrent live connections per channel.
	MaxSubscribersPerChannel int `json:"maxSubscribersPerChannel" yaml:"maxSubscribersPerChannel"`
	// MaxChannels caps the number of simultaneously live channels.
	MaxChannels int `json:"maxChannels" yaml:"maxChannels"`
	// MaxUsers caps the number of identities retained in memory.
	MaxUsers int `json:"maxUsers" yaml:"maxUsers"`
}

// Admin configures the built-in administrator panel. The panel is disabled by
// default; enabling it additionally requires a non-empty access token, so a
// bare `"enabled": true` can never expose an unauthenticated panel.
type Admin struct {
	// Enabled turns the admin panel (and its API) on. Default false.
	Enabled bool `json:"enabled" yaml:"enabled"`
	// Token is the secret an administrator must present to use the panel.
	// The panel stays disabled while the token is empty.
	Token string `json:"token" yaml:"token"`
}

// Active reports whether the admin panel should actually be served: it must be
// explicitly enabled and have a non-empty token.
func (a Admin) Active() bool { return a.Enabled && a.Token != "" }

// Storage configures the durable persistence backend.
type Storage struct {
	// Backend is one of "memory" (default), "sqlite" or "mysql".
	Backend string `json:"backend" yaml:"backend"`
	// DSN is the data source name for the backend: a file path for sqlite
	// (e.g. "nekodrop.db") or a Go MySQL DSN for mysql
	// (e.g. "user:pass@tcp(127.0.0.1:3306)/nekodrop"). Ignored for memory.
	DSN string `json:"dsn" yaml:"dsn"`
}

// Config is the fully-resolved server configuration.
type Config struct {
	// Host is the IP address to listen on (empty = all interfaces).
	Host string `json:"host" yaml:"host"`
	// Port is the TCP port to listen on.
	Port int `json:"port" yaml:"port"`
	// MaxUploadBytes is the maximum accepted size of a single uploaded file.
	MaxUploadBytes int64 `json:"maxUploadBytes" yaml:"maxUploadBytes"`
	// MaxChannelsPerUser caps how many channels a single user may own
	// (0 = unlimited).
	MaxChannelsPerUser int `json:"maxChannelsPerUser" yaml:"maxChannelsPerUser"`
	// Storage selects and configures the persistence backend.
	Storage Storage `json:"storage" yaml:"storage"`
	// Limits bound in-memory resource usage.
	Limits Limits `json:"limits" yaml:"limits"`
	// Admin configures the administrator panel (disabled by default).
	Admin Admin `json:"admin" yaml:"admin"`
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

// ExampleYAML is the annotated default configuration. It is shipped as
// config.example.yaml and written verbatim on first run when no config file
// exists. Every value below is the built-in default, so it documents the knobs
// without changing behaviour. Keep it in sync with Default() and the limits'
// runtime fallbacks (guarded by TestExampleYAMLMatchesDefaults).
const ExampleYAML = `# NekoDrop configuration.
#
# Every setting is optional: the values below are the built-in defaults, so an
# empty file — or no file at all — runs a working server. Settings are resolved
# in increasing order of precedence: this file < environment variables <
# command-line flags.
#
# Both YAML (this file) and JSON are accepted; the format is chosen by the file
# extension (.yaml/.yml vs .json). The keys are identical in either format.

# IP address to listen on. Empty ("") means all interfaces.
host: ""

# TCP port to listen on.
port: 8080

# Maximum size of a single uploaded file, in bytes. Default: 32 MiB.
maxUploadBytes: 33554432

# How many channels one user may own at the same time. 0 means unlimited.
maxChannelsPerUser: 5

# Durable persistence backend.
storage:
  # backend: one of "memory", "sqlite" or "mysql".
  #   memory — nothing is persisted; fastest and simplest (default).
  #   sqlite — embedded single-file database; set dsn to a file path.
  #   mysql  — external MySQL/MariaDB; set dsn to a Go MySQL DSN.
  backend: memory
  # dsn: data source name for the backend (ignored for "memory").
  #   sqlite: a file path, e.g. "nekodrop.db"
  #   mysql:  "user:pass@tcp(127.0.0.1:3306)/nekodrop"
  dsn: ""

# In-memory resource limits. Each caps what is held resident in memory so that
# untrusted traffic cannot exhaust it; a non-positive value falls back to the
# safe default shown (a non-positive value never means "unlimited").
limits:
  # Recent messages kept per channel; older ones are evicted with their files.
  maxMessagesPerChannel: 1000
  # In-memory uploaded-file bytes per channel. Default: 128 MiB.
  maxFileBytesPerChannel: 134217728
  # Total resident memory per channel (message text + file payloads). 160 MiB.
  maxBytesPerChannel: 167772160
  # Concurrent live connections per channel.
  maxSubscribersPerChannel: 512
  # Simultaneously live channels; idle ownerless ones are reclaimed for room.
  maxChannels: 10000
  # Identities retained in memory; the oldest unnamed ones are evicted.
  maxUsers: 100000

# Operator admin panel at /admin. Disabled by default. BOTH keys are required:
# a bare "enabled: true" with an empty token never exposes the panel.
admin:
  enabled: false
  # Secret an administrator presents to unlock the panel. Keep it long/random.
  token: ""
`

// DefaultConfigFiles are the config filenames searched, in order, when the
// operator does not name one explicitly. The first that exists is loaded.
var DefaultConfigFiles = []string{"config.yaml", "config.yml", "config.json"}

// GeneratedConfigFile is the file written on first run when no config file is
// found: a commented YAML document capturing the built-in defaults.
const GeneratedConfigFile = "config.yaml"

// LoadFile reads and parses a config file, returning the defaults merged with
// whatever the file specifies. The format is chosen by file extension: a
// ".yaml"/".yml" file is parsed as YAML, anything else as JSON. A missing path
// returns the defaults with no error only when optional is true.
func LoadFile(path string, optional bool) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if optional && os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := unmarshal(path, data, &cfg); err != nil {
		return cfg, fmt.Errorf("config: parse %s: %w", path, err)
	}
	cfg.normalize()
	return cfg, nil
}

// unmarshal decodes config bytes into cfg, choosing YAML or JSON by the file's
// extension. JSON remains the default so unsuffixed files behave as before.
func unmarshal(path string, data []byte, cfg *Config) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return yaml.Unmarshal(data, cfg)
	default:
		return json.Unmarshal(data, cfg)
	}
}

// Resolve loads the server configuration, honouring an explicitly-named file or
// searching DefaultConfigFiles when explicit is empty. When no config file is
// found (and none was named), it writes a commented default config file so the
// operator has something to edit, then runs with the built-in defaults.
//
// It returns the resolved configuration, the path that was loaded or generated
// (empty if generation was not possible), and whether a file was generated.
func Resolve(explicit string) (cfg Config, path string, generated bool, err error) {
	if explicit != "" {
		cfg, err = LoadFile(explicit, false)
		return cfg, explicit, false, err
	}
	for _, candidate := range DefaultConfigFiles {
		if _, statErr := os.Stat(candidate); statErr == nil {
			cfg, err = LoadFile(candidate, false)
			return cfg, candidate, false, err
		}
	}
	// No config file anywhere: seed one with the documented defaults so the
	// operator can discover and tweak the knobs. Writing is best-effort — a
	// read-only working directory must not stop the server from starting.
	cfg = Default()
	if writeErr := os.WriteFile(GeneratedConfigFile, []byte(ExampleYAML), 0o644); writeErr == nil {
		return cfg, GeneratedConfigFile, true, nil
	}
	return cfg, "", false, nil
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
