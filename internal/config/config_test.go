package config

import (
	"os"
	"path/filepath"
	"testing"
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
