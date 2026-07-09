package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAdminDisabledByDefault(t *testing.T) {
	cfg := Default()
	if cfg.Admin.Enabled || cfg.Admin.Token != "" || cfg.Admin.Active() {
		t.Fatalf("admin should default to off: %+v", cfg.Admin)
	}
}

func TestAdminActiveRequiresToken(t *testing.T) {
	if (Admin{Enabled: true}).Active() {
		t.Fatal("admin without a token must not be active")
	}
	if (Admin{Token: "secret"}).Active() {
		t.Fatal("admin token alone must not activate the panel")
	}
	if !(Admin{Enabled: true, Token: "secret"}).Active() {
		t.Fatal("enabled admin with a token should be active")
	}
}

func TestLoadFileParsesAdmin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"port": 9000, "admin": {"enabled": true, "token": "hunter2"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path, false)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Port != 9000 || !cfg.Admin.Enabled || cfg.Admin.Token != "hunter2" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if !cfg.Admin.Active() {
		t.Fatal("admin should be active")
	}
}

func TestApplyEnvAdmin(t *testing.T) {
	t.Setenv("NEKODROP_ADMIN_ENABLED", "true")
	t.Setenv("NEKODROP_ADMIN_TOKEN", "env-secret")
	cfg := Default()
	cfg.ApplyEnv()
	if !cfg.Admin.Enabled || cfg.Admin.Token != "env-secret" {
		t.Fatalf("env admin config not applied: %+v", cfg.Admin)
	}
}

func TestLoadFileParsesYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "port: 9100\n" +
		"maxChannelsPerUser: 3\n" +
		"storage:\n  backend: sqlite\n  dsn: nekodrop.db\n" +
		"limits:\n  maxMessagesPerChannel: 42\n" +
		"admin:\n  enabled: true\n  token: yaml-secret\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path, false)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Port != 9100 || cfg.MaxChannelsPerUser != 3 {
		t.Fatalf("scalars not parsed: %+v", cfg)
	}
	if cfg.Storage.Backend != "sqlite" || cfg.Storage.DSN != "nekodrop.db" {
		t.Fatalf("storage not parsed: %+v", cfg.Storage)
	}
	if cfg.Limits.MaxMessagesPerChannel != 42 {
		t.Fatalf("limits not parsed: %+v", cfg.Limits)
	}
	if !cfg.Admin.Active() || cfg.Admin.Token != "yaml-secret" {
		t.Fatalf("admin not parsed: %+v", cfg.Admin)
	}
	// Unset scalars must still fall back to the built-in defaults.
	if cfg.MaxUploadBytes != DefaultMaxUploadBytes {
		t.Fatalf("maxUploadBytes = %d, want default", cfg.MaxUploadBytes)
	}
}

func TestLoadFileYAMLExtensions(t *testing.T) {
	for _, name := range []string{"config.yml", "config.YAML"} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte("port: 9200\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadFile(path, false)
		if err != nil {
			t.Fatalf("LoadFile(%s): %v", name, err)
		}
		if cfg.Port != 9200 {
			t.Fatalf("LoadFile(%s) port = %d, want 9200", name, cfg.Port)
		}
	}
}

// TestExampleYAMLMatchesDefaults guards the annotated ExampleYAML against
// drifting away from the built-in defaults it claims to document.
func TestExampleYAMLMatchesDefaults(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal([]byte(ExampleYAML), &cfg); err != nil {
		t.Fatalf("ExampleYAML does not parse: %v", err)
	}
	def := Default()
	if cfg.Host != def.Host || cfg.Port != def.Port ||
		cfg.MaxUploadBytes != def.MaxUploadBytes ||
		cfg.MaxChannelsPerUser != def.MaxChannelsPerUser ||
		cfg.Storage.Backend != def.Storage.Backend {
		t.Fatalf("ExampleYAML scalars drifted from Default(): %+v vs %+v", cfg, def)
	}
	if cfg.Admin.Enabled || cfg.Admin.Token != "" {
		t.Fatalf("ExampleYAML must ship the admin panel disabled: %+v", cfg.Admin)
	}
	// The documented limit numbers double as the safe runtime fallbacks.
	want := Limits{
		MaxMessagesPerChannel:    1000,
		MaxFileBytesPerChannel:   134217728,
		MaxBytesPerChannel:       167772160,
		MaxSubscribersPerChannel: 512,
		MaxChannels:              10000,
		MaxUsers:                 100000,
	}
	if cfg.Limits != want {
		t.Fatalf("ExampleYAML limits = %+v, want %+v", cfg.Limits, want)
	}
}

// TestExampleFileMatchesConstant ensures the shipped config.example.yaml stays
// byte-identical to the ExampleYAML constant the server writes on first run.
func TestExampleFileMatchesConstant(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("read config.example.yaml: %v", err)
	}
	if string(data) != ExampleYAML {
		t.Fatal("config.example.yaml differs from the ExampleYAML constant; keep them in sync")
	}
}

func TestResolveGeneratesDefaultOnFirstRun(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg, path, generated, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !generated || path != GeneratedConfigFile {
		t.Fatalf("expected a generated %s, got path=%q generated=%v", GeneratedConfigFile, path, generated)
	}
	data, err := os.ReadFile(GeneratedConfigFile)
	if err != nil {
		t.Fatalf("generated file missing: %v", err)
	}
	if string(data) != ExampleYAML {
		t.Fatal("generated config does not match ExampleYAML")
	}
	if cfg.Port != Default().Port {
		t.Fatalf("generated run should use defaults, got %+v", cfg)
	}
	// A second run must load the file it just wrote rather than regenerate.
	_, path2, generated2, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve (second): %v", err)
	}
	if generated2 || path2 != GeneratedConfigFile {
		t.Fatalf("second run regenerated unexpectedly: path=%q generated=%v", path2, generated2)
	}
}

func TestResolvePrefersExistingFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("config.yaml", []byte("port: 9300\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, path, generated, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if generated || path != "config.yaml" || cfg.Port != 9300 {
		t.Fatalf("expected to load existing config.yaml, got path=%q generated=%v cfg=%+v", path, generated, cfg)
	}
}

func TestResolveExplicitMissingErrors(t *testing.T) {
	_, _, _, err := Resolve(filepath.Join(t.TempDir(), "absent.yaml"))
	if err == nil {
		t.Fatal("Resolve of an explicitly named missing file should error")
	}
}
